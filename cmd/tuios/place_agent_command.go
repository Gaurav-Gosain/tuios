package main

import (
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// "Any machine": the host name any, for start-agent -s any:SESSION and fan
// --host any, asks this machine's daemon to place the agent (place-agent),
// then starts it where the daemon picked, as if that host had been named.
// tuios call place-agent asks the same question and starts nothing.

// hostAny is the host name that asks for placement.
const hostAny = "any"

// placeRequest is one place-agent call.
type placeRequest struct {
	agent, repoURL    string
	clone             bool
	task, taskSession string
}

// placeAgent asks this machine's daemon to place an agent.
func placeAgent(r placeRequest) (session.PlaceAnswer, error) {
	client, err := dialVerb()
	if err != nil {
		return session.PlaceAnswer{}, err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{}
	if r.agent != "" {
		params["agent"] = r.agent
	}
	if r.repoURL != "" {
		params["repo_url"] = r.repoURL
	}
	if r.clone {
		params["clone"] = true
	}
	if here := originIfAny(); here != "" {
		params["local_origin"] = here
	}
	if r.task != "" {
		params["task"] = r.task
		if r.taskSession != "" {
			params["task_session"] = r.taskSession
		}
	}
	if path := os.Getenv("PATH"); path != "" {
		params["env"] = map[string]string{"PATH": path}
	}
	var ans session.PlaceAnswer
	raw, err := client.CallWithTimeout("place-agent", params, 20*time.Second)
	if err == nil {
		err = jsonx.Unmarshal(raw, &ans)
	}
	return ans, err
}

// originIfAny is the origin URL of the repository the current directory is
// in, or "" outside one.
func originIfAny() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	url, _ := worktree.OriginURL(dir)
	return url
}

// placedLine is what the command prints before it starts an agent where
// placement put it.
func placedLine(ans session.PlaceAnswer) string {
	return "Placed on " + plainLine(ans.Name) + ". " + plainLine(ans.Why)
}
