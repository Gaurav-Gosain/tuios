package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Running the worktree and agent commands on another machine.
//
// fan, worktree new, worktree ls and start-agent take --host (start-agent
// takes -s HOST:SESSION, like every session verb), and worktree rm, fan keep
// and worktree pull take HOST:SESSION. Each dials the host's daemon through
// this machine's daemon and its link, and sends the same verb a local call
// sends. The one parameter that changes is the repository: a directory here
// means nothing there, so it is sent as this checkout's origin URL, and the
// host finds its own checkout of that repository, under [hosts.NAME]
// repos_root when that is set.

// dialHost connects to the daemon on host, or to this machine's for "" or
// "local".
func dialHost(host string) (*verbTarget, error) {
	if host == federation.LocalHostName {
		host = ""
	}
	t := &verbTarget{host: host}
	if host == "" {
		client, err := dialVerb()
		if err != nil {
			return nil, err
		}
		t.client = client
		return t, nil
	}
	if err := ensureDaemon(); err != nil {
		return nil, err
	}
	client, _, err := session.DialVerbClientThroughHost(host, version)
	if err != nil {
		return nil, explainHostConnectError(host, err)
	}
	t.client = client
	return t, nil
}

// splitHostSession reads a HOST:SESSION argument. A name with no host is on
// this machine, and local:NAME is too.
func splitHostSession(arg string) (host, name string) {
	st := federation.ParseSessionTarget(arg)
	host = st.Host
	if host == federation.LocalHostName {
		host = ""
	}
	return host, st.Session
}

// configuredReposRoot is [hosts.NAME] repos_root from this machine's config,
// or "" when it is not set or the config cannot be read.
func configuredReposRoot(host string) string {
	path, err := config.GetConfigPath()
	if err != nil {
		return ""
	}
	hosts, err := config.HostsInFile(path)
	if err != nil {
		return ""
	}
	return hosts[host].ReposRoot
}

// repoParams is the repository part of a worktree or agent call. On this
// machine it is the directory, --repo or the current one. On a host it is
// --repo as a directory on the host, or else the origin URL of the
// repository the current directory is in, with the host's repos_root and
// clone.
func repoParams(host, repo string, clone bool) (map[string]any, error) {
	if host == "" {
		if clone {
			return nil, errors.New("--clone clones on another machine and needs --host. Here, name the checkout with --repo")
		}
		dir, err := repoArg(repo)
		if err != nil {
			return nil, err
		}
		return map[string]any{"repo": dir}, nil
	}
	if repo != "" {
		if clone {
			return nil, errors.New("--repo names a directory on " + host + " and --clone makes one. Pass one or the other")
		}
		return map[string]any{"repo": repo}, nil
	}
	url, err := originHere()
	if err != nil {
		return nil, err
	}
	params := map[string]any{"repo_url": url}
	if root := configuredReposRoot(host); root != "" {
		params["repos_root"] = root
	}
	if clone {
		params["clone"] = true
	}
	return params, nil
}

// originHere is the origin URL of the repository the current directory is
// in, the name a host knows it by.
func originHere() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	url, err := worktree.OriginURL(dir)
	if err != nil {
		return "", &diagnosticError{
			What:  "this directory is not in a git repository, so there is no repository to name on the other machine.",
			Cause: err.Error(),
			Fix:   "run the command inside the repository, or pass --repo with its directory on the other machine.",
		}
	}
	if url == "" {
		return "", &diagnosticError{
			What:  "the repository here has no origin remote, so the other machine cannot find its copy by name.",
			Cause: "a repository is matched across machines by its origin URL.",
			Fix:   "pass --repo with the checkout's directory on the other machine.",
		}
	}
	return url, nil
}

// explainHostedVerb is t.explain with the two refusals a host adds to the
// worktree and agent verbs: a daemon too old to find a repository by its
// origin, and a repository it has no checkout of.
func explainHostedVerb(t *verbTarget, verb string, err error) error {
	var call *session.VerbCallError
	if t.host != "" && errors.As(err, &call) {
		switch {
		case call.Code == session.ErrVerbInvalidParams && call.Hint != nil && (call.Hint.Param == "repo_url" || call.Hint.Param == "repos_root" || call.Hint.Param == "clone") && strings.Contains(call.Message, "has no parameter"):
			return &diagnosticError{
				What:  fmt.Sprintf("tuios on %s is too old to find a repository by its origin.", t.host),
				Cause: "repo_url is newer than the tuios running there.",
				Fix:   fmt.Sprintf("upgrade tuios on %s, or pass --repo with the checkout's directory on %s.", t.host, t.host),
			}
		case call.Code == session.ErrVerbRepoNotFound:
			return &diagnosticError{
				What:  fmt.Sprintf("%s has no checkout of this repository.", t.host),
				Cause: call.Message,
				Fix: fmt.Sprintf("pass --clone to clone it there, set where %s keeps its checkouts with 'tuios hosts add %s <addr> --repos-root ~/src', or pass --repo with the directory on %s.",
					t.host, t.host, t.host),
			}
		}
	}
	return t.explain(verb, err)
}
