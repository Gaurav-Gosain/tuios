package session

import (
	"encoding/json"
	"strings"
	"time"
)

// start-agent: one agent in a new pane, and the answer once it is ready.
//
// fan starts agents in worktrees it makes and returns at once. An orchestrator
// that wants one helper next to it had to open a window with the command,
// then poll list-agents until the helper looked ready, and then type at it,
// which is where a first prompt landed in a first-run screen. start-agent is
// that composition done by the daemon: the pane is opened with the agent in
// it, the verb waits for positive evidence the agent is at its prompt (see
// agent_launch.go), types the first prompt when one is given, and answers with
// the pane and what it showed.
//
// A pane on needs_input ends the wait at once with ready false: a trust
// question or a login is the person's to answer, the Inbox shows it, and the
// pane is kept with its name so the caller can wait for it and ask later.

// startAgentDefaultReadyTimeout bounds the wait. It is shorter than fan's,
// because a caller is blocked on it.
const startAgentDefaultReadyTimeout = 2 * time.Minute

func (d *Daemon) verbStartAgent(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session      string            `json:"session"`
		Agent        string            `json:"agent"`
		Name         string            `json:"name"`
		Cwd          string            `json:"cwd"`
		Workspace    int               `json:"workspace"`
		Focus        bool              `json:"focus"`
		Prompt       string            `json:"prompt"`
		ReadyTimeout int               `json:"ready_timeout"`
		Env          map[string]string `json:"env"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Agent) == "" {
		return nil, invalidParam("agent", "agent is required: the harness or program to start, with its arguments, such as claude or \"codex --model o5\"")
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	if verr := checkWindowCwd(p.Cwd); verr != nil {
		return nil, verr
	}
	env, pathList, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return nil, verr
	}
	launch, verr := d.resolveAgentLaunch("agent", p.Agent, pathList)
	if verr != nil {
		return nil, verr
	}

	onExit := func(ptyID string) { d.notifyPTYClosed(sess.ID, ptyID) }
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Title:     p.Name,
		Name:      p.Name,
		Cwd:       p.Cwd,
		Workspace: p.Workspace,
		// Not focused unless asked: an agent starting a helper should not
		// pull the person out of the pane they are in.
		Focus:   p.Focus,
		Command: launch.argv,
		Env:     env,
	}, onExit)
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}

	timeout := durationOr(p.ReadyTimeout, startAgentDefaultReadyTimeout)
	w, outcome := d.waitAgentStart(sess, win.ID, launch.harness, timeout, true, nil)
	out := map[string]any{
		"type":      "agent_started",
		"session":   sess.Name,
		"window_id": win.ID,
		"pty_id":    win.PTYID,
		"name":      windowLabelOf(win),
		"agent":     launch.harness,
		"command":   launch.command(),
		"state":     w.AgentState.Name(),
		"ready":     outcome == agentStartReady,
		"outcome":   string(outcome),
	}
	if w.ID != "" {
		out["name"] = windowLabelOf(w)
		if h := w.AgentHarness; h != "" {
			out["agent"] = h
		}
	}
	switch outcome {
	case agentStartReady:
		out["ready_by"] = readyBy(w)
	case agentStartBlocked:
		out["blocked_by"] = agentBlockedBy(w)
		out["reason"] = "The agent is waiting on a prompt of its own, and the person has to answer it first. The pane is kept: read the prompt with peek-prompt, and wait for the agent with wait-for agent-state --until idle."
	case agentStartTimeout:
		out["reason"] = "The agent showed no sign of being at its prompt before ready_timeout. The pane is kept: look at it with capture-pane."
	case agentStartWindowClosed:
		out["reason"] = "The agent's window closed before it was ready: the program exited. Look at what it printed by starting it in a shell pane."
	default:
		out["reason"] = "The wait ended with the " + string(outcome) + "."
	}
	if p.Prompt != "" {
		if outcome != agentStartReady {
			out["prompt_status"] = PromptNotSent
			out["prompt_note"] = "The agent was not ready, so the prompt was not typed."
			return out, nil
		}
		status, note, _ := d.typeFirstPrompt(sess, win.ID, p.Prompt)
		out["prompt_status"] = status
		if note != "" {
			out["prompt_note"] = note
		}
	}
	return out, nil
}
