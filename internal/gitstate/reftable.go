package gitstate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
)

// A repository made with the reftable ref backend (git init or clone
// --ref-format=reftable, git 2.45 and later) keeps its refs in binary tables
// under reftable/, and git writes the placeholder below to HEAD so that an
// older git refuses the directory rather than misreads it. A reader of the
// file sees a branch that cannot exist, and finds no loose ref and no
// packed-refs for any other.
//
// The tables can only be read by asking git, which costs a subprocess. The
// cost model of this package still holds: git rewrites tables.list on every
// ref update, so the list's contents are a file read that says whether the
// last answer still stands, and git is only asked again when it does not.

// reftablePlaceholder is the line git writes to HEAD in a reftable repository.
const reftablePlaceholder = "ref: refs/heads/.invalid"

// IsReftableHead reports whether a HEAD line is the reftable placeholder.
func IsReftableHead(line string) bool {
	return strings.TrimSpace(line) == reftablePlaceholder
}

// usesReftable reports whether the repository keeps its refs in reftable.
func usesReftable(commondir string) bool {
	_, err := os.Stat(filepath.Join(commondir, "reftable", "tables.list"))
	return err == nil
}

// tablesListLimit bounds how much of a tables.list is read. git compacts the
// stack as it grows, so the list holds a few names, each under 60 bytes.
const tablesListLimit = 64 << 10

// reftableStamp is the contents of the worktree's and the repository's
// tables.list. Every ref update adds a table and every compaction replaces
// some, and both rename a new list into place, so two equal stamps mean no
// ref in either stack has moved.
func reftableStamp(gitdir, commondir string) string {
	var b strings.Builder
	dirs := []string{gitdir}
	if commondir != gitdir {
		dirs = append(dirs, commondir)
	}
	for _, dir := range dirs {
		f, err := os.Open(filepath.Join(dir, "reftable", "tables.list"))
		if err != nil {
			b.WriteString("-\x00")
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, tablesListLimit))
		_ = f.Close() // opened read-only: nothing to flush
		if err != nil {
			// A read error counts as no list, the same as a stack that is
			// not there.
			b.WriteString("-\x00")
			continue
		}
		b.Write(data)
		b.WriteByte(0)
	}
	return b.String()
}

// reftableAnswers is what git last said about one git directory's refs, and
// the stamp it was said under.
type reftableAnswers struct {
	stamp    string
	headOK   bool
	branch   string
	sha      string
	detached bool
	refs     map[string]string
}

var (
	reftableMu    sync.Mutex
	reftableCache = map[string]*reftableAnswers{}
)

// reftableFor is the answers kept for gitdir, emptied when its stamp has
// moved. The caller holds reftableMu.
func reftableFor(gitdir, stamp string) *reftableAnswers {
	ans := reftableCache[gitdir]
	if ans == nil || ans.stamp != stamp {
		ans = &reftableAnswers{stamp: stamp, refs: map[string]string{}}
		reftableCache[gitdir] = ans
	}
	return ans
}

// askGit runs git in root, the working tree, and answers its trimmed output.
//
// It is a variable so a test can count how often it runs, as countCommits
// is: answering from the cache while no ref moves is what keeps a reftable
// repository as cheap to show as any other.
var askGit = func(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	cmd := childproc.Git(ctx, root, args...)
	cmd.Env = append(cmd.Env, "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// ReftableHead answers what HEAD names in the reftable working tree at root
// whose git directories are gitdir and commondir: the branch and its commit,
// or on a detached HEAD the short hash as the branch and detached true, as
// readHead answers for a HEAD file. A branch with no commit yet answers its
// name and no commit. All empty means git could not say; that answer is not
// kept, so the next read asks again.
func ReftableHead(gitdir, commondir, root string) (branch, sha string, detached bool) {
	stamp := reftableStamp(gitdir, commondir)
	reftableMu.Lock()
	if ans := reftableFor(gitdir, stamp); ans.headOK {
		branch, sha, detached = ans.branch, ans.sha, ans.detached
		reftableMu.Unlock()
		return branch, sha, detached
	}
	reftableMu.Unlock()

	// One git for the usual case: the commit, then the ref HEAD names, which
	// is "HEAD" itself when it is detached.
	if out, err := askGit(root, "rev-parse", "HEAD", "--symbolic-full-name", "HEAD"); err == nil {
		lines := strings.Split(out, "\n")
		if len(lines) == 2 {
			sha = strings.TrimSpace(lines[0])
			if ref := strings.TrimSpace(lines[1]); ref != "HEAD" {
				branch = strings.TrimPrefix(ref, "refs/heads/")
			} else if len(sha) >= 7 {
				branch, detached = sha[:7], true
			}
		}
	} else if ref, err := askGit(root, "symbolic-ref", "-q", "HEAD"); err == nil {
		// A branch with no commit yet, which rev-parse cannot resolve.
		branch = strings.TrimPrefix(ref, "refs/heads/")
	}
	if branch == "" {
		return "", "", false
	}

	reftableMu.Lock()
	if ans := reftableFor(gitdir, stamp); !ans.headOK {
		ans.branch, ans.sha, ans.detached, ans.headOK = branch, sha, detached, true
	}
	reftableMu.Unlock()
	return branch, sha, detached
}

// reftableRef answers the commit ref names in a reftable repository, empty
// when it names none.
func reftableRef(gitdir, commondir, root, ref string) string {
	stamp := reftableStamp(gitdir, commondir)
	reftableMu.Lock()
	if sha, ok := reftableFor(gitdir, stamp).refs[ref]; ok {
		reftableMu.Unlock()
		return sha
	}
	reftableMu.Unlock()

	sha, err := askGit(root, "rev-parse", "-q", "--verify", ref+"^{commit}")
	if err != nil {
		// git exits 1 for a ref that does not exist, which is an answer worth
		// keeping. Anything else, a timeout included, is asked again.
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 {
			return ""
		}
		sha = ""
	}
	reftableMu.Lock()
	reftableFor(gitdir, stamp).refs[ref] = sha
	reftableMu.Unlock()
	return sha
}

// RepoName is the name a repository goes by, from its shared git directory.
//
// A ".git" directory is named for the checkout around it. A bare repository
// is named for itself less a ".git" suffix, so "/srv/api.git" is "api",
// unless its own name says only that it holds git. ".bare" and "git" are the
// usual names for a bare repository kept in a project directory beside the
// worktrees made from it, and such a repository is named for that directory.
func RepoName(commondir string) string {
	commondir = filepath.Clean(commondir)
	base := filepath.Base(commondir)
	switch {
	case base == ".git" || base == ".bare" || base == "git":
		return filepath.Base(filepath.Dir(commondir))
	case strings.HasSuffix(base, ".git") && len(base) > len(".git"):
		return strings.TrimSuffix(base, ".git")
	}
	return base
}
