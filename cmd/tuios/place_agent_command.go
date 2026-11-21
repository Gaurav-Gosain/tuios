package main

import (
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const hostAny = "any"

type placeRequest struct {
	agent, repoURL    string
	clone             bool
	task, taskSession string
}

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

func originIfAny() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	url, _ := worktree.OriginURL(dir)
	return url
}

func placedLine(ans session.PlaceAnswer) string {
	return "Placed on " + plainLine(ans.Name) + ". " + plainLine(ans.Why)
}
