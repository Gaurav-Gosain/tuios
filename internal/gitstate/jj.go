package gitstate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const jjTemplate = `if(self.contained_in("trunk()"), "T",
    if(self.contained_in("trunk()..@"), "A",
      if(self.contained_in("@..trunk()"), "B", "C")))
  ++ "\t" ++ commit_id ++ "\t" ++ parents.map(|c| c.commit_id()).join(" ")
  ++ "\t" ++ change_id.shortest(8) ++ "\t" ++ empty ++ "\t" ++ conflict
  ++ "\t" ++ divergent ++ "\t" ++ root
  ++ "\t" ++ local_bookmarks.map(|b| b.name()).join(" ")
  ++ "\t" ++ local_bookmarks.any(|b| b.conflict())
  ++ "\t" ++ description.first_line() ++ "\n"`

const jjRevset = `(trunk()..@) | (@..trunk()) | @ | @- | parents(trunk()..@) | trunk()`

type jjEntry struct {
	ops   string
	state State
	ok    bool
}

var jjCache = map[string]jjEntry{}

func locateJJ(dir string) (root, repoDir string, ok bool) {
	for p := filepath.Clean(dir); ; {
		jj := filepath.Join(p, ".jj")
		if st, err := os.Stat(jj); err == nil && st.IsDir() {
			repo := filepath.Join(jj, "repo")
			st, err := os.Stat(repo)
			switch {
			case err != nil:
				return "", "", false
			case st.IsDir():
				return p, repo, true
			}
			line, err := readFirstLine(repo)
			if err != nil || line == "" {
				return "", "", false
			}
			if !filepath.IsAbs(line) {
				line = filepath.Join(jj, line)
			}
			return p, filepath.Clean(line), true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", "", false
		}
		p = parent
	}
}

func jjWins(jroot, groot string, gok bool) bool {
	return !gok || len(jroot) >= len(groot)
}

// Repo is the repository a directory is in, as files alone tell it. The
// zero Repo is no repository.
type Repo struct {
	Root   string // the working copy: the git worktree or the jj workspace
	Main   string // the main worktree or the main jj workspace
	GitDir string // the git directory: the worktree's own, or the one jj keeps its commits in
	JJ     bool
}

// Find reports the repository dir is in. A jj workspace wins over the git
// directory beside it or above it, and a git repository nested inside a jj
// workspace is git, the same rule Read follows.
func Find(dir string) Repo {
	gitdir, commondir, groot, gok := Locate(dir)
	if root, repoDir, ok := locateJJ(dir); ok && jjWins(root, groot, gok) {
		store := filepath.Join(repoDir, "store")
		target, err := readFirstLine(filepath.Join(store, "git_target"))
		if err == nil && target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(store, target)
			}
			if st, err := os.Stat(target); err == nil && st.IsDir() {
				return Repo{Root: root, Main: filepath.Dir(filepath.Dir(repoDir)), GitDir: filepath.Clean(target), JJ: true}
			}
		}
	}
	if !gok {
		return Repo{}
	}
	main := groot
	if filepath.Base(commondir) == ".git" {
		main = filepath.Dir(commondir)
	}
	return Repo{Root: groot, Main: main, GitDir: gitdir}
}

// GitEnv is what git needs in a jj workspace with no ".git" of its own: the
// git directory jj keeps its commits in, and the workspace as the work
// tree. It is empty for a colocated workspace, whose ".git" git finds by
// itself, and for git. The index there is no workspace's, so a caller that
// needs one brings its own through GIT_INDEX_FILE.
func (r Repo) GitEnv() []string {
	if !r.JJ {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(r.Root, ".git")); err == nil {
		return nil
	}
	return []string{"GIT_DIR=" + r.GitDir, "GIT_WORK_TREE=" + r.Root}
}

// JJTimeout bounds a jj call whose caller set no deadline. jj waits for a
// lock another jj holds, and a verb must not hang on it.
const JJTimeout = time.Minute

// RunJJ runs jj with args in dir and returns its stdout. It ends jj, and
// every process jj started, at ctx's deadline, or after JJTimeout when ctx
// has none. A failure is jj's own stderr, or the context's error when the
// deadline ended the run.
func RunJJ(ctx context.Context, dir string, args ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, JJTimeout)
		defer cancel()
	}
	cmd := childproc.CommandContext(ctx, "jj", append([]string{"--no-pager", "--color=never"}, args...)...)
	cmd.Dir = dir
	childproc.OwnGroup(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

// JJCommit returns the commit the first revision of revset names in the jj
// workspace at root. With snapshot jj first records the working copy as @,
// as any jj command does, which writes an operation; without it nothing is
// written and @ is as jj last recorded it.
func JJCommit(ctx context.Context, root, revset string, snapshot bool) (string, error) {
	args := []string{"log", "--no-graph", "-r", revset, "-T", `commit_id ++ "\n"`}
	if !snapshot {
		args = append([]string{"--ignore-working-copy"}, args...)
	}
	out, err := RunJJ(ctx, root, args...)
	if err != nil {
		return "", errfmt.Errorf("jj log -r %s: %w", revset, err)
	}
	id, _, _ := strings.Cut(out, "\n")
	if id == "" {
		return "", errfmt.Errorf("jj: %s names no commit", revset)
	}
	return id, nil
}

func jjOps(repoDir string) (string, error) {
	heads, err := os.ReadDir(filepath.Join(repoDir, "op_heads", "heads"))
	if err != nil {
		return "", err
	}
	names := make([]string, len(heads))
	for i, h := range heads {
		names[i] = h.Name()
	}
	return strings.Join(names, " "), nil
}

// JJOps is jjOps for the jj repository r is in: a key that changes when
// anything jj records does.
func (r Repo) JJOps() (string, error) {
	return jjOps(filepath.Join(r.Main, ".jj", "repo"))
}

func readJJ(root, repoDir string) (State, bool) {
	ops, err := jjOps(repoDir)
	if err != nil {
		return State{}, false
	}

	mu.Lock()
	prev, had := jjCache[root]
	mu.Unlock()
	if had && prev.ops == ops {
		return prev.state, prev.ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	cmd := childproc.CommandContext(ctx, "jj", "--ignore-working-copy", "--no-pager",
		"--color=never", "log", "--no-graph", "-r", jjRevset, "-T", jjTemplate)
	cmd.Dir = root
	childproc.OwnGroup(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		mu.Lock()
		jjCache[root] = jjEntry{ops: ops}
		mu.Unlock()
		return State{}, false
	}
	st := summarizeJJ(string(out))
	st.Root = root
	st.Repo = filepath.Base(filepath.Dir(filepath.Dir(repoDir)))

	mu.Lock()
	jjCache[root] = jjEntry{ops: ops, state: st, ok: true}
	mu.Unlock()
	return st, true
}

type jjCommit struct {
	side               byte
	parents            []string
	change             string
	empty              bool
	bookmarks          []string
	conflictedBookmark bool
	desc               string
	conflict           bool
	divergent          bool
	root               bool
}

func summarizeJJ(out string) State {
	byID := map[string]*jjCommit{}
	var at *jjCommit
	trunkIsRoot := false
	ahead, behind := 0, 0
	var st State
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		f := strings.SplitN(line, "\t", 11)
		if len(f) < 11 || len(f[0]) != 1 {
			continue
		}
		c := &jjCommit{
			side:               f[0][0],
			parents:            strings.Fields(f[2]),
			change:             f[3],
			empty:              f[4] == "true",
			conflict:           f[5] == "true",
			divergent:          f[6] == "true",
			root:               f[7] == "true",
			bookmarks:          strings.Fields(f[8]),
			conflictedBookmark: f[9] == "true",
			desc:               f[10],
		}
		byID[f[1]] = c
		if at == nil && c.side != 'B' && c.side != 'T' {
			at = c
		}
		switch c.side {
		case 'T':
			trunkIsRoot = c.root
		case 'A':
			if !c.empty {
				ahead++
			}
			if c.conflict {
				st.Conflicts++
			}
			if c.divergent || c.conflictedBookmark {
				st.Divergent++
			}
		case 'B':
			if !c.empty {
				behind++
			}
		}
	}
	if at == nil {
		for _, c := range byID {
			if c.side == 'T' {
				at = c
			}
		}
		if at == nil {
			return st
		}
	}

	start := at
	for start.empty && len(start.bookmarks) == 0 && start.side == 'A' && len(start.parents) == 1 {
		p := byID[start.parents[0]]
		if p == nil {
			break
		}
		start = p
	}
	st.Branch = jjLabel(start, byID, &st)
	if !trunkIsRoot {
		st.Upstream = "trunk()"
		st.Ahead, st.Behind = ahead, behind
	}
	return st
}

func jjLabel(start *jjCommit, byID map[string]*jjCommit, st *State) string {
	above := 0
	for c := start; c != nil; {
		if c != start && c.side != 'A' {
			break
		}
		if len(c.bookmarks) > 0 {
			if c != start {
				st.Unbookmarked = above
			}
			return c.bookmarks[0]
		}
		if len(c.parents) != 1 {
			break
		}
		if !c.empty {
			above++
		}
		c = byID[c.parents[0]]
	}
	st.Detached = true
	if start.desc != "" {
		return start.desc
	}
	return start.change
}
