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

const reftablePlaceholder = "ref: refs/heads/.invalid"

// IsReftableHead reports whether a HEAD line is the reftable placeholder.
func IsReftableHead(line string) bool {
	return strings.TrimSpace(line) == reftablePlaceholder
}

func usesReftable(commondir string) bool {
	_, err := os.Stat(filepath.Join(commondir, "reftable", "tables.list"))
	return err == nil
}

const tablesListLimit = 64 << 10

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
		_ = f.Close()
		if err != nil {
			b.WriteString("-\x00")
			continue
		}
		b.Write(data)
		b.WriteByte(0)
	}
	return b.String()
}

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

func reftableFor(gitdir, stamp string) *reftableAnswers {
	ans := reftableCache[gitdir]
	if ans == nil || ans.stamp != stamp {
		ans = &reftableAnswers{stamp: stamp, refs: map[string]string{}}
		reftableCache[gitdir] = ans
	}
	return ans
}

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
