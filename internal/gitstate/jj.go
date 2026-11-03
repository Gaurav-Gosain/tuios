package gitstate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// A jj workspace, colocated with git or not.
//
// jj keeps no checked out branch, so the row's name is worked out from the
// stack instead: the bookmark on the working copy's commit, else the nearest
// one under it with the count of commits above it that no bookmark covers,
// else the commit's description. An empty working-copy commit is skipped,
// because "jj commit" leaves one on top of every finished change.
//
// Everything comes from one jj call, and that call only runs when the
// operation log has moved. jj records every operation by writing a new file
// under op_heads/heads, so the check is one directory read, the same bargain
// Read makes with git's commit hashes.

// jjTemplate prints one line per commit. The description is last because it
// is the only field that can hold a tab. Fields are joined by hand because
// separate() drops empty ones, and a commit without a bookmark has one.
const jjTemplate = `if(self.contained_in("trunk()"), "T",
    if(self.contained_in("trunk()..@"), "A",
      if(self.contained_in("@..trunk()"), "B", "C")))
  ++ "\t" ++ commit_id ++ "\t" ++ parents.map(|c| c.commit_id()).join(" ")
  ++ "\t" ++ change_id.shortest(8) ++ "\t" ++ empty ++ "\t" ++ conflict
  ++ "\t" ++ divergent ++ "\t" ++ root
  ++ "\t" ++ local_bookmarks.map(|b| b.name()).join(" ")
  ++ "\t" ++ local_bookmarks.any(|b| b.conflict())
  ++ "\t" ++ description.first_line() ++ "\n"`

// @- and the stack's parents are asked for too, so a working copy sitting
// empty on trunk, or on an older commit of trunk, can be named by it.
const jjRevset = `(trunk()..@) | (@..trunk()) | @ | @- | parents(trunk()..@) | trunk()`

type jjEntry struct {
	ops   string
	state State
	ok    bool // false: jj failed or timed out, and is not run again until the log moves
}

var jjCache = map[string]jjEntry{}

// locateJJ walks up from dir to the nearest ".jj" directory, and answers the
// workspace root and the repository directory every workspace shares.
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
			// A workspace other than the first: the file names the shared
			// repository, relative to ".jj".
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

// jjWins is whether the jj workspace at jroot is the repository rather than
// the git one at groot: it is when it is as deep or deeper.
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
			// The file can hold anything: only a directory is a git store, and
			// anything else is no jj repository.
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

// readJJ returns the state of the jj workspace at root.
func readJJ(root, repoDir string) (State, bool) {
	heads, err := os.ReadDir(filepath.Join(repoDir, "op_heads", "heads"))
	if err != nil {
		return State{}, false
	}
	names := make([]string, len(heads))
	for i, h := range heads {
		names[i] = h.Name()
	}
	ops := strings.Join(names, " ")

	mu.Lock()
	prev, had := jjCache[root]
	mu.Unlock()
	if had && prev.ops == ops {
		return prev.state, prev.ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	// --ignore-working-copy keeps this a read. Without it every refresh would
	// snapshot the working copy and write an operation into the person's log.
	cmd := childproc.CommandContext(ctx, "jj", "--ignore-working-copy", "--no-pager",
		"--color=never", "log", "--no-graph", "-r", jjRevset, "-T", jjTemplate)
	cmd.Dir = root
	// A wrapper's child that keeps stdout open would hold Output past the timeout.
	childproc.OwnGroup(cmd)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		// A missing or slow jj would otherwise cost a process, and up to
		// countTimeout, on every refresh.
		mu.Lock()
		jjCache[root] = jjEntry{ops: ops}
		mu.Unlock()
		return State{}, false
	}
	st := summarizeJJ(string(out))
	st.Root = root
	// The repository directory is <main workspace>/.jj/repo.
	st.Repo = filepath.Base(filepath.Dir(filepath.Dir(repoDir)))

	mu.Lock()
	jjCache[root] = jjEntry{ops: ops, state: st, ok: true}
	mu.Unlock()
	return st, true
}

type jjCommit struct {
	side      byte
	parents   []string
	change    string
	empty     bool
	bookmarks []string
	// conflictedBookmark: a bookmark here points at more than one commit.
	conflictedBookmark bool
	desc               string
	conflict           bool
	divergent          bool
	root               bool
}

// summarizeJJ turns jjTemplate's lines into the row's name and figures. Only
// commits with changes are counted: an empty one is not work.
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
		// jj lists children before parents, so the first commit that is not
		// trunk's alone is the working copy's.
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
		// The working copy is trunk itself.
		for _, c := range byID {
			if c.side == 'T' {
				at = c
			}
		}
		if at == nil {
			return st
		}
	}

	// Empty commits on top say nothing, however many there are. Trunk ends
	// the walk, so a stack of nothing but empty commits is named by trunk.
	start := at
	for start.empty && start.side == 'A' && len(start.parents) == 1 {
		p := byID[start.parents[0]]
		if p == nil {
			break
		}
		start = p
	}
	st.Branch = jjLabel(start, byID, &st)
	if !trunkIsRoot {
		// trunk() stands in for an upstream. With no remote it is the root
		// commit, and a count against the whole history says nothing.
		st.Upstream = "trunk()"
		st.Ahead, st.Behind = ahead, behind
	}
	return st
}

// jjLabel names the stack start is on, and counts the commits above the
// bookmark it names.
func jjLabel(start *jjCommit, byID map[string]*jjCommit, st *State) string {
	above := 0
	for c := start; c != nil; {
		// Trunk's bookmark is not the stack's: "main +16" would hide what the
		// sixteen commits are.
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
	// No bookmark names the stack: jj's detached HEAD. The label is for the
	// row only and names no revision.
	st.Detached = true
	if start.desc != "" {
		return start.desc
	}
	return start.change
}
