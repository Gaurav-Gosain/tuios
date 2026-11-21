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

func splitHostSession(arg string) (host, name string) {
	st := federation.ParseSessionTarget(arg)
	host = st.Host
	if host == federation.LocalHostName {
		host = ""
	}
	return host, st.Session
}

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
	if root := config.HostReposRoot(host); root != "" {
		params["repos_root"] = root
	}
	if clone {
		params["clone"] = true
	}
	return params, nil
}

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
