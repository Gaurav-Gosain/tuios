package worktree

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

// Finding a repository on this machine by the remote it was cloned from.
//
// A path means nothing between two machines, and the one name a repository
// has on both is its origin URL. So a caller on another machine that wants a
// worktree of "the repository I am in" sends that URL, and this file finds the
// checkout here whose origin is the same repository, or clones one when asked
// to.
//
// Matching is exact after normalising the spellings git accepts for one
// remote: https://github.com/o/r, git@github.com:o/r.git and
// ssh://git@github.com/o/r all name github.com/o/r. Nothing is guessed past
// that. Two checkouts of one repository are both returned, and the caller
// says which, because picking one quietly is how an agent ends up working in
// the wrong copy.

// SearchRoot is one directory FindByOrigin looks under, and how deep.
type SearchRoot struct {
	Dir   string
	Depth int
}

// searchVisitCap bounds how many directories one search reads. A search runs
// inside a verb, and a root pointed at a large tree must cost a bounded time.
const searchVisitCap = 20000

// cloneTimeout bounds one clone. It is under the CLI's own read timeout for
// the verbs that can clone.
const cloneTimeout = 4 * time.Minute

// NormalizeRemote turns a remote URL into the form two spellings of one
// repository share: host and path, the host lowercased, without a user, a
// port, a trailing slash or a trailing ".git". A local path, or a file URL,
// becomes "file:" and the cleaned absolute path. An empty string stays empty.
func NormalizeRemote(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	trim := func(p string) string {
		p = strings.TrimRight(p, "/")
		p = strings.TrimSuffix(p, ".git")
		return strings.TrimRight(p, "/")
	}
	if strings.HasPrefix(s, "file://") {
		return "file:" + filepath.Clean(trim(strings.TrimPrefix(s, "file://")))
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return trim(s)
		}
		return strings.ToLower(u.Hostname()) + "/" + strings.TrimLeft(trim(u.Path), "/")
	}
	if host, path, ok := scpLike(s); ok {
		return strings.ToLower(host) + "/" + strings.TrimLeft(trim(path), "/")
	}
	return "file:" + filepath.Clean(trim(s))
}

// scpLike splits git's scp form, [user@]host:path, which git reads as ssh
// when the part before the first colon holds no slash.
func scpLike(s string) (host, path string, ok bool) {
	colon := strings.Index(s, ":")
	if colon <= 0 {
		return "", "", false
	}
	if slash := strings.Index(s, "/"); slash >= 0 && slash < colon {
		return "", "", false
	}
	host = s[:colon]
	// C:\src\api is a drive letter on Windows, which git reads as a local
	// path, not a host named c.
	if len(host) == 1 {
		return "", "", false
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if host == "" {
		return "", "", false
	}
	return host, s[colon+1:], true
}

// OriginURL is the url of the remote named origin in the repository dir is
// in, as git reads it. An empty result with no error means the repository has
// no origin.
func OriginURL(dir string) (string, error) {
	out, err := run(dir, "config", "--get", "remote.origin.url")
	if err != nil {
		// git config exits 1 for a key that is not set, which is not a
		// failure of the repository.
		if _, rerr := Root(dir); rerr != nil {
			return "", rerr
		}
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// originFromConfig reads remote.origin.url from a .git/config file without
// running git. The search reads one per candidate directory, and a
// subprocess for each would make a search of a large tree slow.
func originFromConfig(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	inOrigin := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section := strings.ToLower(strings.Join(strings.Fields(strings.Trim(line, "[]")), " "))
			inOrigin = section == `remote "origin"`
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "url") {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		return value
	}
	return ""
}

// FindByOrigin returns every main checkout under the roots whose origin is
// the repository remote names, sorted. It reads each candidate's .git/config
// rather than running git. Hidden directories are skipped, and so is anything
// under a checkout: a repository is not searched for other repositories.
func FindByOrigin(roots []SearchRoot, remote string) []string {
	want := NormalizeRemote(remote)
	if want == "" {
		return nil
	}
	seen := map[string]bool{}
	var found []string
	visited := 0
	type item struct {
		dir   string
		depth int
	}
	for _, root := range roots {
		if root.Dir == "" {
			continue
		}
		queue := []item{{filepath.Clean(root.Dir), 0}}
		for len(queue) > 0 && visited < searchVisitCap {
			it := queue[0]
			queue = queue[1:]
			visited++
			gitPath := filepath.Join(it.dir, ".git")
			if st, err := os.Stat(gitPath); err == nil {
				if st.IsDir() && !seen[it.dir] {
					seen[it.dir] = true
					if NormalizeRemote(originFromConfig(filepath.Join(gitPath, "config"))) == want {
						found = append(found, it.dir)
					}
				}
				continue
			}
			if it.depth >= root.Depth {
				continue
			}
			entries, err := os.ReadDir(it.dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
					continue
				}
				queue = append(queue, item{filepath.Join(it.dir, name), it.depth + 1})
			}
		}
	}
	sort.Strings(found)
	return found
}

// CloneDir is where Clone puts a repository when the caller names no
// directory: $XDG_DATA_HOME/tuios/repos.
func CloneDir() string {
	return filepath.Join(xdg.DataHome, "tuios", "repos")
}

// DefaultSearchRoots are the directories searched when the caller names none:
// the clone directory, the usual source directories under home three levels
// deep, and home itself one level deep.
func DefaultSearchRoots(home string) []SearchRoot {
	roots := []SearchRoot{{Dir: CloneDir(), Depth: 1}}
	if home == "" {
		return roots
	}
	for _, name := range []string{"src", "dev", "code", "projects", "repos", "git", "work", "go/src"} {
		roots = append(roots, SearchRoot{Dir: filepath.Join(home, name), Depth: 3})
	}
	return append(roots, SearchRoot{Dir: home, Depth: 1})
}

// ErrCloneURL reports a URL Clone refuses to fetch.
var ErrCloneURL = errors.New("not a network git URL")

// ValidCloneURL reports whether Clone may fetch from raw: an https, ssh or
// git URL, or git's scp form user@host:path. Everything else is refused: a
// local path or a file URL, which would copy a directory of this machine the
// caller may not otherwise read, an ext:: or other transport helper, which
// runs a command, and anything starting with a hyphen, which git would read
// as an option.
func ValidCloneURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" || s != raw || strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t\r\n\x00") || strings.Contains(s, "::") {
		return fmt.Errorf("%w: %q", ErrCloneURL, raw)
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") {
			return fmt.Errorf("%w: %q", ErrCloneURL, raw)
		}
		switch u.Scheme {
		case "https", "ssh", "git":
			return nil
		}
		return fmt.Errorf("%w: %q uses %s. Only https, ssh and git URLs are cloned", ErrCloneURL, raw, u.Scheme)
	}
	host, path, ok := scpLike(s)
	if !ok || path == "" || strings.HasPrefix(host, "-") {
		return fmt.Errorf("%w: %q", ErrCloneURL, raw)
	}
	return nil
}

// RepoName is the directory name a clone of remote gets: the last element of
// its path without ".git".
func RepoName(remote string) string {
	n := NormalizeRemote(remote)
	n = strings.TrimPrefix(n, "file:")
	base := filepath.Base(strings.ReplaceAll(n, "\\", "/"))
	if base == "." || base == "/" || base == "" {
		return "repo"
	}
	return Slug(base)
}

// cloneProtocols is what GIT_ALLOW_PROTOCOL is set to for a clone: the
// network transports and nothing else, so a submodule or redirect cannot
// reach a local path or a transport helper even if the URL checks passed.
const cloneProtocols = "https:ssh:git"

// Clone clones remote into parent/<name> and returns the new checkout. The
// URL must pass ValidCloneURL. An existing directory at the destination is
// refused, since it is somebody's. Nothing interactive runs: no credential
// prompt and no ssh password prompt, so a clone that needs one fails with
// git's message instead of hanging.
func Clone(remote, parent string) (string, error) {
	if err := ValidCloneURL(remote); err != nil {
		return "", err
	}
	return clone(remote, parent, cloneProtocols)
}

func clone(remote, parent, protocols string) (string, error) {
	dest := filepath.Join(parent, RepoName(remote))
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("%s already exists and its origin is not %s", dest, remote)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cloneTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--", remote, dest)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL="+protocols)
	if os.Getenv("GIT_SSH_COMMAND") == "" && os.Getenv("GIT_SSH") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if ctx.Err() != nil {
			msg = "the clone took longer than " + cloneTimeout.String()
		}
		if msg == "" {
			msg = err.Error()
		}
		_ = os.RemoveAll(dest)
		return "", fmt.Errorf("git clone: %s", msg)
	}
	return dest, nil
}
