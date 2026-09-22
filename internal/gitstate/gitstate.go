// Package gitstate reads the git facts a sidebar can usefully carry in a narrow
// column: which branch a directory is on, and how far that branch has drifted
// from its upstream.
//
// It does not report whether the tree is dirty, and that is a decision rather
// than an omission. Dirtiness is the one git fact that cannot be had without
// walking the working tree, so it is the one that makes a sidebar stutter on a
// large repository, and it is also the fact a person is least likely to be
// surprised by: you know whether you have edited something. Branch and
// divergence are the two you cannot know without being told.
//
// The cost model is the point of this package. Everything except the divergence
// counts is a file read: HEAD, one ref, and the config. Counting how far two
// commits have diverged needs the commit graph, so that part shells out to git,
// and it only shells out when the two commits have actually moved. Their hashes
// are read from files, so the check that decides whether to spend a subprocess
// is itself two file reads.
package gitstate

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is what a rail row can say about a directory's repository.
type State struct {
	// Root is the working tree's own root directory.
	Root string
	// Repo is the repository's name, which is the base name of its main
	// checkout. Every linked worktree of a repository reports the same Repo.
	Repo string
	// Branch is the checked out branch. On a detached HEAD it is the short
	// hash and Detached is true, because a row still has to say something and
	// the hash is the only name there is.
	Branch   string
	Detached bool
	// Upstream is the remote-tracking ref the branch is set to follow, empty
	// when it follows nothing. A branch with no upstream is not behind by
	// zero, it is not comparable at all, and a row must not print an equals
	// sign for it.
	Upstream string
	// Ahead and Behind are commits on one side of the fork point and not the
	// other. Both are zero when Upstream is empty.
	Ahead, Behind int
}

// HasUpstream reports whether the divergence counts mean anything.
func (s State) HasUpstream() bool { return s.Upstream != "" }

// countTimeout bounds the one subprocess this package runs. A repository whose
// object store is on a stalled network mount must cost a sidebar one refresh,
// not the session.
const countTimeout = 2 * time.Second

// missTTL is how long a directory that is not in a repository is remembered as
// not being in one. Without it every refresh walks to the filesystem root for
// every pane sitting in a home directory.
const missTTL = 30 * time.Second

type entry struct {
	headSHA, upSHA string
	state          State
	ok             bool
}

var (
	mu     sync.Mutex
	cache  = map[string]entry{}
	misses = map[string]time.Time{}
)

// Read returns the state of the repository containing dir.
//
// The second return is false when dir is not in a repository, which is the
// common case for a pane sitting in a home directory and is therefore cached
// for a while rather than rediscovered by walking to the root every time.
//
// It is safe to call from several goroutines. It is not safe to call on the
// render path: the divergence counts can cost a subprocess.
func Read(dir string) (State, bool) {
	if dir == "" {
		return State{}, false
	}
	mu.Lock()
	missedAt, missed := misses[dir]
	mu.Unlock()
	if missed && time.Since(missedAt) < missTTL {
		return State{}, false
	}

	gitdir, commondir, root, ok := Locate(dir)
	if !ok {
		// Remember it. Otherwise every refresh walks from a home directory to
		// the filesystem root, stat-ing a ".git" at every level, for a pane
		// that is never going to be in a repository.
		mu.Lock()
		misses[dir] = time.Now()
		mu.Unlock()
		return State{}, false
	}
	if missed {
		mu.Lock()
		delete(misses, dir)
		mu.Unlock()
	}

	mu.Lock()
	prev, hadPrev := cache[gitdir]
	mu.Unlock()

	branch, headSHA, detached := readHead(gitdir, commondir)
	if branch == "" && headSHA == "" {
		return State{}, false
	}

	st := State{
		Root:     root,
		Repo:     filepath.Base(strings.TrimSuffix(filepath.Dir(commondir), string(filepath.Separator))),
		Branch:   branch,
		Detached: detached,
	}
	if !detached {
		st.Upstream = upstreamOf(commondir, branch)
	}

	upSHA := ""
	if st.Upstream != "" {
		upSHA = resolveRef(gitdir, commondir, st.Upstream)
	}

	// The whole point. Two commits that have not moved cannot have diverged
	// differently, so the counts from last time still hold and the subprocess
	// is not run.
	if hadPrev && prev.ok && prev.headSHA == headSHA && prev.upSHA == upSHA &&
		prev.state.Upstream == st.Upstream {
		st.Ahead, st.Behind = prev.state.Ahead, prev.state.Behind
		return st, true
	}

	if st.Upstream != "" && upSHA != "" && headSHA != "" && upSHA != headSHA {
		if a, b, err := countCommits(root, headSHA, upSHA); err == nil {
			st.Ahead, st.Behind = a, b
		}
	}

	mu.Lock()
	cache[gitdir] = entry{headSHA: headSHA, upSHA: upSHA, state: st, ok: true}
	mu.Unlock()
	return st, true
}

// Forget drops everything remembered about every repository. Tests use it so
// one case cannot see another's answers.
func Forget() {
	mu.Lock()
	clear(cache)
	clear(misses)
	mu.Unlock()
}

// Locate walks up from dir looking for the ".git" that marks a working tree,
// and answers where that tree's own git directory is, where the repository's
// shared one is, and where the tree starts. It only reads files.
//
// The two git directories are the same thing in a plain checkout and different
// in a linked worktree, where HEAD is per worktree and the remote-tracking refs
// are shared. Reading the branch from the shared directory would report the
// main checkout's branch on every worktree of the repository, which is exactly
// the case this is for. A ".git" file whose git directory has no commondir
// pointer also reports the two as the same.
func Locate(dir string) (gitdir, commondir, root string, ok bool) {
	dir = filepath.Clean(dir)
	for p := dir; ; {
		entry := filepath.Join(p, ".git")
		st, err := os.Lstat(entry)
		switch {
		case err != nil:
			// keep walking
		case st.IsDir():
			return entry, entry, p, true
		case st.Mode().IsRegular():
			// A linked worktree: the file names the git directory.
			data, err := os.ReadFile(entry)
			if err != nil {
				return "", "", "", false
			}
			line := strings.TrimSpace(string(data))
			target, found := strings.CutPrefix(line, "gitdir:")
			if !found {
				return "", "", "", false
			}
			gd := strings.TrimSpace(target)
			if !filepath.IsAbs(gd) {
				gd = filepath.Join(p, gd)
			}
			gd = filepath.Clean(gd)
			return gd, commonDirOf(gd), p, true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", "", "", false
		}
		p = parent
	}
}

// commonDirOf reads a worktree git directory's "commondir" pointer, which names
// the repository's shared git directory. Without it every worktree looks like a
// repository of its own with no remotes.
func commonDirOf(gitdir string) string {
	data, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return gitdir
	}
	rel := strings.TrimSpace(string(data))
	if rel == "" {
		return gitdir
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(gitdir, rel))
}

// readHead reads the checked out branch and the commit it points at.
func readHead(gitdir, commondir string) (branch, sha string, detached bool) {
	data, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return "", "", false
	}
	line := strings.TrimSpace(string(data))
	ref, isSymbolic := strings.CutPrefix(line, "ref:")
	if !isSymbolic {
		// Detached. The row still needs a name, and the short hash is the only
		// one there is.
		if len(line) >= 7 {
			return line[:7], line, true
		}
		return "", "", false
	}
	ref = strings.TrimSpace(ref)
	return strings.TrimPrefix(ref, "refs/heads/"), resolveRef(gitdir, commondir, ref), false
}

// resolveRef answers the commit a ref names, reading the loose file first and
// falling back to packed-refs, which is where a ref goes once git has packed it
// and is therefore where most remote-tracking refs live in a repository that
// has been fetched more than a few times.
func resolveRef(gitdir, commondir, ref string) string {
	for _, base := range []string{gitdir, commondir} {
		if data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(ref))); err == nil {
			if sha := strings.TrimSpace(string(data)); sha != "" {
				return sha
			}
		}
	}
	f, err := os.Open(filepath.Join(commondir, "packed-refs"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		sha, name, found := strings.Cut(line, " ")
		if found && name == ref {
			return sha
		}
	}
	return ""
}

// upstreamOf reads the remote-tracking ref a branch follows out of the config.
//
// This is a small hand-rolled read of the two keys that matter rather than a
// general INI parser, because the alternative is asking git, and asking git is
// a subprocess for a fact that is three lines of a file.
func upstreamOf(commondir, branch string) string {
	f, err := os.Open(filepath.Join(commondir, "config"))
	if err != nil {
		return ""
	}
	defer f.Close()

	want := `[branch "` + branch + `"]`
	inSection := false
	remote, merge := "", ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inSection = line == want
			continue
		}
		if !inSection {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "remote":
			remote = strings.TrimSpace(value)
		case "merge":
			merge = strings.TrimSpace(value)
		}
	}
	if remote == "" || merge == "" {
		return ""
	}
	// A branch can track another branch in the same repository, which git spells
	// with a remote of ".". There is no remote-tracking ref in that case: the
	// upstream is the local branch itself.
	if remote == "." {
		return merge
	}
	return "refs/remotes/" + remote + "/" + strings.TrimPrefix(merge, "refs/heads/")
}

// countCommits asks git how far the two commits have diverged. This is the one
// place this package spends a subprocess, and Read only reaches it when the two
// commits have moved since the last answer.
//
// It is a variable so a test can count how often it runs. Not spending this
// subprocess is the reason the package exists, and a cost model nothing checks
// is a cost model that quietly stops holding.
var countCommits = func(root, head, upstream string) (ahead, behind int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-list", "--left-right", "--count", head+"..."+upstream)
	cmd.Dir = root
	// A repository is read with the user's git but never with the user's
	// pager, editor or prompt: this runs unattended behind a sidebar.
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, nil
	}
	ahead, _ = strconv.Atoi(fields[0])
	behind, _ = strconv.Atoi(fields[1])
	return ahead, behind, nil
}

// Dirty is how many paths are staged, changed in the working tree, and not
// tracked at all.
type Dirty struct {
	Staged, Modified, Untracked int
}

// Any reports whether there is anything to say.
func (d Dirty) Any() bool { return d.Staged > 0 || d.Modified > 0 || d.Untracked > 0 }

// ReadDirty counts the paths git would report as changed.
//
// This is deliberately a separate call from Read, and it is the expensive one.
// Everything Read answers comes from a handful of file reads because a branch
// and a divergence are recorded facts. Dirtiness is not recorded anywhere: the
// only way to know is to compare the working tree against the index, which
// means walking the tree. On a large repository that is the thing that makes a
// sidebar stutter, so the caller decides whether to pay for it rather than
// getting it folded into an answer it did not ask for.
//
// Not cached here either, for the same reason the hash check does not help: a
// file changes without any commit moving, so there is no cheap fingerprint to
// gate it on. The caller's refresh interval is the only bound.
func ReadDirty(root string) (Dirty, bool) {
	if root == "" {
		return Dirty{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	// Porcelain v1 because its two status columns are fixed width and the
	// format is frozen by git's own compatibility promise. Untracked files are
	// counted but not walked into: a directory of a thousand new files is one
	// line and one number, not a thousand.
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain",
		"--untracked-files=normal", "--no-renames")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return Dirty{}, false
	}

	var d Dirty
	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) < 2 {
			continue
		}
		if line[0] == '?' && line[1] == '?' {
			d.Untracked++
			continue
		}
		if line[0] != ' ' {
			d.Staged++
		}
		if line[1] != ' ' {
			d.Modified++
		}
	}
	return d, true
}
