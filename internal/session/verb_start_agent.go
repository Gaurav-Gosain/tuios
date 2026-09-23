package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
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
//
// From another machine it is reached like every verb, with -s HOST:SESSION,
// and the repository is named by its origin URL (verb_repo_source.go), so "an
// agent for this repository on build" is one call. A named session that does
// not exist is made, so the caller does not have to make the somewhere first.
//
// It grants nothing new-window with a command does not already grant: the
// argv is exec'd directly, never a shell line.
//
// With protocol, the agent runs headless over ACP or the Codex app-server
// protocol, under `tuios agent-proto` as the pane's process, which shows it as
// a transcript and reports its state (agent_protocol.go). The pane is ready on
// that report alone, and the first prompt is typed into it the same way.

// startAgentDefaultReadyTimeout bounds the wait. It is shorter than fan's,
// because a caller is blocked on it.
const startAgentDefaultReadyTimeout = 2 * time.Minute

func (d *Daemon) verbStartAgent(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		repoSource
		Session      string            `json:"session"`
		Agent        string            `json:"agent"`
		Args         []string          `json:"args"`
		Name         string            `json:"name"`
		Cwd          string            `json:"cwd"`
		Workspace    int               `json:"workspace"`
		Focus        bool              `json:"focus"`
		Prompt       string            `json:"prompt"`
		ReadyTimeout int               `json:"ready_timeout"`
		Env          map[string]string `json:"env"`
		Protocol     string            `json:"protocol"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Protocol != "" {
		if verr := checkProtocol(p.Protocol); verr != nil {
			return nil, verr
		}
	}
	if strings.TrimSpace(p.Agent) == "" {
		return nil, invalidParam("agent", "agent is required: the harness or program to start, with its arguments, such as claude or \"codex --model o5\"")
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the current one")
	}
	if p.Cwd != "" && p.named() {
		return nil, invalidParam("cwd", "pass cwd or a repository, not both: the agent starts in the repository's main checkout")
	}
	if verr := checkWindowCwd(p.Cwd); verr != nil {
		return nil, verr
	}
	name := strings.TrimSpace(p.Session)
	if name != "" {
		if err := ValidateSessionName(name); err != nil {
			return nil, invalidParam("session", err.Error())
		}
	}
	env, pathList, verr := callerEnv(cs, p.Env)
	if verr != nil {
		return nil, verr
	}
	var launch agentLaunch
	resolveAgent := func() *verbError {
		var verr *verbError
		launch, verr = d.resolveAgentLaunch("agent", p.Agent, pathList)
		return verr
	}

	// The repository, when one is named. The agent is checked before a
	// clone, so a missing agent does not cost one.
	cwd, cloned := p.Cwd, false
	if p.named() || p.ReposRoot != "" || p.Clone {
		root, didClone, verr := d.resolveRepoSource(cs, "start-agent", p.repoSource, resolveAgent)
		if verr != nil {
			return nil, verr
		}
		cwd, cloned = root, didClone
	}
	if launch.argv == nil {
		if verr := resolveAgent(); verr != nil {
			return nil, verr
		}
	}
	argv := append(append([]string{}, launch.argv...), p.Args...)
	// A protocol pane runs the pane program with the agent's argv after it.
	// The agent is still exec'd directly, by the pane program, with no shell.
	command := argv
	if p.Protocol != "" {
		argv = protocolAgentArgv(p.Protocol, launch.argv, p.Args)
		var verr *verbError
		if command, verr = d.protocolArgv(p.Protocol, launch.harness, argv); verr != nil {
			return nil, verr
		}
	}

	// The session: the named one, made when it does not exist, or the most
	// recently active one, made when there is none at all.
	sess := d.findTargetSession(name)
	created := false
	if sess == nil {
		if name == "" {
			name = startAgentSessionName(launch)
		}
		var err error
		sess, err = d.manager.CreateSession(name, &SessionConfig{}, defaultVerbSessionWidth, defaultVerbSessionHeight)
		if err != nil {
			return nil, hintedVerbError(ErrVerbSessionExists, err.Error(), &VerbHint{
				Param:     "session",
				Available: d.sessionNames(),
			})
		}
		created = true
	}

	sessionID := sess.ID
	// The window id is known only once the window exists, and the process
	// can exit before then, so the mark is set and dropped under one lock:
	// a pane whose process already exited is never marked.
	var (
		markMu   sync.Mutex
		markedID string
		exited   bool
	)
	onExit := func(ptyID string) {
		d.notifyPTYClosed(sessionID, ptyID)
		markMu.Lock()
		exited = true
		if markedID != "" {
			d.unmarkProtocolPane(markedID)
		}
		markMu.Unlock()
	}
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Title:     p.Name,
		Name:      p.Name,
		Cwd:       cwd,
		Workspace: p.Workspace,
		// Not focused unless asked: an agent starting a helper should not
		// pull the person out of the pane they are in.
		Focus:   p.Focus,
		Command: command,
		Env:     env,
	}, onExit)
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}
	if p.Protocol != "" {
		markMu.Lock()
		if !exited {
			markedID = win.ID
			d.markProtocolPane(win.ID, p.Protocol)
		}
		markMu.Unlock()
	}

	timeout := durationOr(p.ReadyTimeout, startAgentDefaultReadyTimeout)
	// A protocol pane is ready only on its own report: its screen is a
	// transcript no manifest rule reads, so unknown never means quiet.
	w, outcome := d.waitAgentStart(sess, win.ID, launch.harness, timeout, true, p.Protocol != "", nil)
	out := map[string]any{
		"type":            "agent_started",
		"session":         sess.Name,
		"session_id":      sess.ID,
		"created_session": created,
		"window_id":       win.ID,
		"pty_id":          win.PTYID,
		"workspace":       win.Workspace,
		"name":            windowLabelOf(win),
		"agent":           launch.harness,
		"command":         strings.Join(argv, " "),
		"cwd":             cwd,
		"state":           w.AgentState.Name(),
		"ready":           outcome == agentStartReady,
		"outcome":         string(outcome),
	}
	if cloned {
		out["cloned"] = true
	}
	if p.Protocol != "" {
		out["protocol"] = p.Protocol
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
			out["prompt_note"] = "The agent was not ready, so the prompt was not typed. Send it with send-text once it is."
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

// startAgentSessionName names the session start-agent makes when the caller
// names none and there is none at all: the harness id, else the program's
// name, else agents.
func startAgentSessionName(l agentLaunch) string {
	for _, name := range []string{l.harness, filepath.Base(l.argv[0])} {
		if name != "" && ValidateSessionName(name) == nil {
			return name
		}
	}
	return "agents"
}
