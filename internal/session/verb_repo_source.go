package session

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// ErrVerbRepoNotFound reports a repo_url that matches no checkout on this
// machine, and no clone was asked for. Its remedy is clone, a repos_root, or
// a directory, which is why it does not share a code with git_failed.
const ErrVerbRepoNotFound = "repo_not_found"

const (
	linkCapSpawn   = config.LinkAllowOpen
	linkCapReadOut = config.LinkAllowWrite
)

func (d *Daemon) checkLinkPolicy(cs *connState, capability, verb string) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	return d.checkLinkCaps(cs, verb, []string{capability})
}

type repoSource struct {
	Repo      string `json:"repo"`
	RepoURL   string `json:"repo_url"`
	ReposRoot string `json:"repos_root"`
	Clone     bool   `json:"clone"`
}

func (r repoSource) named() bool {
	return strings.TrimSpace(r.Repo) != "" || strings.TrimSpace(r.RepoURL) != ""
}

var repoSourceParams = []verbParam{
	{Name: "repo_url", Type: "string", Description: "The repository by its origin URL, for a caller on another machine. This daemon finds its own checkout whose origin is the same repository. Pass repo or repo_url, not both."},
	{Name: "repos_root", Type: "string", Description: "With repo_url, the directory on this machine to look under, and to clone into. Absolute, or starting with ~/. Omit to look under ~/src, ~/dev, ~/code, ~/projects, ~/repos, ~/git, ~/work, ~/go/src, the home directory itself and tuios's own clone directory."},
	{Name: "clone", Type: "bool", Description: "With repo_url, clone the repository when no checkout is found. Only https, ssh and git URLs are cloned.", Default: "false"},
}

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

func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
