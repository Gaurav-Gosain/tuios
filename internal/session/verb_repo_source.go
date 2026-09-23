package session

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Naming a repository to a daemon that may be on another machine.
//
// new-worktree, fan and start-agent all take a repository. On one machine the
// caller names it by a directory, repo. From another machine a directory
// means nothing, so the caller names it by its origin URL, repo_url, and this
// daemon finds its own checkout of that repository: under repos_root when the
// caller names one, else under the usual source directories of this
// machine's home. With clone and no checkout found, it clones one.
//
// What this lets a caller do that it could not before: read which of this
// machine's directories hold a checkout of a URL it already knows, and, with
// clone, make git fetch a network URL into a new directory. Both are less
// than what the same caller can already do with new-window and a command, so
// neither widens a link or pane caller's reach. clone is still checked
// against the link policy's open capability below.

// ErrVerbRepoNotFound reports a repo_url that matches no checkout on this
// machine, and no clone was asked for. Its remedy is clone, a repos_root, or
// a directory, which is why it does not share a code with git_failed.
const ErrVerbRepoNotFound = "repo_not_found"

// Link capabilities the repository and worktree verbs check at the point they
// act, on top of the check every verb gets before its handler runs
// (link_policy.go). A clone starts a program, git, so it is open, the
// capability that starts sessions and windows. Reading a worktree's files
// out is write: a machine that may type into a shell here can read every one
// of them already, and list, which only reads screens, must not.
const (
	linkCapSpawn = config.LinkAllowOpen
	linkCapFiles = config.LinkAllowWrite
)

// checkLinkPolicy holds a call from another machine to that machine's link
// policy for one capability. A call that did not come over a link passes.
// The verbs that reach it are also in verbCapabilities, so the check before
// the handler already covers them; this one stays at the point of the action,
// so a verb that gains a path to it later cannot skip it.
func (d *Daemon) checkLinkPolicy(cs *connState, capability, verb string) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	return d.checkLinkCaps(cs, verb, []string{capability})
}

// repoSource is the repository parameters new-worktree, fan and start-agent
// share.
type repoSource struct {
	Repo      string `json:"repo"`
	RepoURL   string `json:"repo_url"`
	ReposRoot string `json:"repos_root"`
	Clone     bool   `json:"clone"`
}

// named reports whether the caller named a repository at all.
func (r repoSource) named() bool {
	return strings.TrimSpace(r.Repo) != "" || strings.TrimSpace(r.RepoURL) != ""
}

// repoSourceParams are the verb table entries for repo_url, repos_root and
// clone, shared by every verb that takes them.
var repoSourceParams = []verbParam{
	{Name: "repo_url", Type: "string", Description: "The repository by its origin URL, for a caller on another machine. This daemon finds its own checkout whose origin is the same repository. Pass repo or repo_url, not both."},
	{Name: "repos_root", Type: "string", Description: "With repo_url, the directory on this machine to look under, and to clone into. Absolute, or starting with ~/. Omit to look under ~/src, ~/dev, ~/code, ~/projects, ~/repos, ~/git, ~/work, ~/go/src, the home directory itself and tuios's own clone directory."},
	{Name: "clone", Type: "bool", Description: "With repo_url, clone the repository when no checkout is found. Only https, ssh and git URLs are cloned.", Default: "false"},
}

// resolveRepoSource turns the repository parameters into the main checkout on
// this machine. cloned says the checkout was made by this call.
//
// beforeClone, when not nil, runs just before a clone and can refuse it: it
// is where a verb checks the rest of its call, so a call that would fail
// anyway does not clone first.
func (d *Daemon) resolveRepoSource(cs *connState, verb string, src repoSource, beforeClone func() *verbError) (root string, cloned bool, verr *verbError) {
	repo := strings.TrimSpace(src.Repo)
	url := strings.TrimSpace(src.RepoURL)
	switch {
	case repo != "" && url != "":
		return "", false, invalidParam("repo_url", "pass repo or repo_url, not both: one names a directory on this machine, the other a repository by its origin")
	case url == "" && (src.ReposRoot != "" || src.Clone):
		return "", false, invalidParam("repo_url", "repos_root and clone only mean something with repo_url")
	case url == "":
		root, verr := repoRootParam(repo)
		return root, false, verr
	}

	roots := worktree.DefaultSearchRoots(userHome())
	cloneInto := worktree.CloneDir()
	if src.ReposRoot != "" {
		dir, verr := reposRootParam(src.ReposRoot)
		if verr != nil {
			return "", false, verr
		}
		roots = []worktree.SearchRoot{{Dir: dir, Depth: 3}}
		cloneInto = dir
	}
	found := worktree.FindByOrigin(roots, url)
	switch {
	case len(found) == 1:
		return found[0], false, nil
	case len(found) > 1:
		return "", false, hintedVerbError(ErrVerbInvalidParams, "more than one checkout on this machine has the origin "+echoName(url), &VerbHint{
			Param:     "repo",
			Available: found,
			Detail:    "Name the one to use with repo. Nothing is picked for you, so an agent does not end up working in the wrong copy.",
		})
	}
	if !src.Clone {
		where := "the usual source directories under " + userHome()
		if src.ReposRoot != "" {
			where = src.ReposRoot
		}
		return "", false, hintedVerbError(ErrVerbRepoNotFound, "no checkout on this machine has the origin "+echoName(url)+". Looked under "+where+".", &VerbHint{
			Param:  "repo_url",
			Detail: "Pass clone to clone it, repos_root to look somewhere else, or repo to name the directory.",
		})
	}
	if verr := d.checkLinkPolicy(cs, linkCapSpawn, verb); verr != nil {
		return "", false, verr
	}
	if err := worktree.ValidCloneURL(url); err != nil {
		return "", false, invalidParam("repo_url", err.Error()+". Only https, ssh and git URLs are cloned.")
	}
	if beforeClone != nil {
		if verr := beforeClone(); verr != nil {
			return "", false, verr
		}
	}
	dest, err := worktree.Clone(url, cloneInto)
	if err != nil {
		return "", false, hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{
			Param:  "repo_url",
			Detail: "git could not clone the repository. Nothing was left behind. A clone that needs a password fails here, since nothing can type one: use a URL whose key or token this machine already has.",
		})
	}
	root, verr = repoRootParam(dest)
	return root, true, verr
}

// reposRootParam checks repos_root: an absolute directory, or one under the
// daemon's home written with ~/.
func reposRootParam(dir string) (string, *verbError) {
	dir = strings.TrimSpace(dir)
	if strings.HasPrefix(dir, "~/") || dir == "~" {
		dir = filepath.Join(userHome(), strings.TrimPrefix(dir, "~"))
	}
	if !filepath.IsAbs(dir) {
		return "", invalidParam("repos_root", "repos_root must be absolute, or start with ~/, since it is a directory on the machine this daemon runs on")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", invalidParam("repos_root", "repos_root "+echoName(dir)+" is not a directory on this machine")
	}
	return filepath.Clean(dir), nil
}

// userHome is the daemon's home directory, or "" when it cannot be read.
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
