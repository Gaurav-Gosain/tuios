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
		NoWait       bool              `json:"no_wait"`
		Env          map[string]string `json:"env"`
		Protocol     string            `json:"protocol"`
		Grants       []string          `json:"grants"`
		Task         string            `json:"task"`
		HumanNonce   string            `json:"human_nonce"`
		// TaskHost names the machine that owns the card, as this
		// machine's [hosts] calls it, and TaskSession its session there:
		// the new pane claims the card on its owner over the link
		// (pool_place.go).
		TaskHost    string `json:"task_host"`
		TaskSession string `json:"task_session"`
		// TaskMail mails the pane the card once it holds it, from tuios,
		// so the agent reads it rather than having it typed.
		TaskMail bool   `json:"task_mail"`
		Parent   string `json:"parent"`
		// AddDirs are passed with the harness's add-dir flag, and
		// AfterReady is sent once the agent is ready (agent_presets.go).
		AddDirs    []string `json:"add_dirs"`
		AfterReady []string `json:"after_ready"`
		launchChoice
	}
	// A preset is read once, here, and merged into the params.
	params, verr := d.expandAgentPreset(params)
	if verr != nil {
		return nil, verr
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Protocol != "" {
		if verr := checkProtocol(p.Protocol); verr != nil {
			return nil, verr
		}
	}
	// What the new pane may do through tuios, decided before anything is
	// made. See pane_grants.go.
	grants, verr := d.launchGrants(cs, p.Grants)
	if verr != nil {
		return nil, verr
	}
	// The agent the new one reports to, also decided before anything is
	// made: a parent the caller may not name, or a chain too deep, starts
	// nothing. See agent_parent.go.
	parent, verr := d.resolveAgentParent(cs, "start-agent", p.Parent)
	if verr != nil {
		return nil, verr
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
		if launch, verr = d.resolveAgentLaunch("agent", p.Agent, pathList); verr != nil {
			return verr
		}
		launch, verr = d.withChoice(launch, p.launchChoice)
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
	env = launch.withPath(env)
	extra, verr := d.addDirArgs(launch, p.AddDirs)
	if verr != nil {
		return nil, verr
	}
	extra = append(extra, p.Args...)
	argv := append(append([]string{}, launch.argv...), extra...)
	if p.Protocol != "" {
		argv = protocolAgentArgv(p.Protocol, launch.argv, extra)
	}
	// The agent's permission mode is held to the calling agent's, before
	// anything is made (child_clamp.go).
	if argv, verr = d.clampChildMode(cs, "start-agent", launch.harness, argv, env, p.Protocol != ""); verr != nil {
		return nil, verr
	}
	// A protocol pane runs the pane program with the agent's argv after it.
	// The agent is still exec'd directly, by the pane program, with no shell.
	command := argv
	if p.Protocol != "" {
		var verr *verbError
		if command, verr = d.protocolArgv(p.Protocol, launch.harness, argv); verr != nil {
			return nil, verr
		}
	}

	// The session: the named one, made when it does not exist, or the most
	// recently active one, made when there is none at all.
	// Checked before a session is made for it: a pane without admin starts
	// an agent only inside its own folder.
	if _, _, verr := d.childWindowBound(cs, "start-agent", cwd); verr != nil {
		return nil, verr
	}
	sess := d.findTargetSession(cs, name)
	// The card the new pane is to work on, checked before anything is made.
	// It is a card of the session the pane opens in, so that session has to
	// exist already. See task_auto.go.
	var task *taskParam
	taskHost := strings.TrimSpace(p.TaskHost)
	if taskHost != "" {
		switch {
		case p.Task == "":
			return nil, invalidParam("task_host", "task_host names the machine that owns the card task names. Pass task too")
		case p.TaskSession == "":
			return nil, invalidParam("task_session", "task_host needs task_session: the card's session on "+echoName(taskHost))
		case d.federation == nil:
			return nil, invalidParam("task_host", "task_host "+echoName(taskHost)+" is not a linked machine: this daemon has no [hosts]")
		}
		if _, err := d.federation.Table().Lookup(taskHost); err != nil {
			return nil, invalidParam("task_host", "task_host "+echoName(taskHost)+" is not a linked machine")
		}
	}
	if p.Task != "" && taskHost == "" {
		cardSession := strings.TrimSpace(p.TaskSession)
		if cardSession == "" {
			if sess == nil {
				return nil, invalidParam("task", "task names a card of the session the agent starts in, and session "+echoName(name)+" does not exist yet. Name the card's session with task_session")
			}
			cardSession = sess.Name()
		}
		if task, verr = d.admitTaskParam(cs, "start-agent", cardSession, p.Task, p.HumanNonce, false); verr != nil {
			return nil, verr
		}
	}
	created := false
	if sess == nil {
		if name == "" {
			name = startAgentSessionName(launch)
		}
		var err error
		sess, err = d.manager.CreateSession(name, callerSessionConfig(cs), defaultVerbSessionWidth, defaultVerbSessionHeight)
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
	win, bverr, err := d.addWindowFor(cs, "start-agent", sess, NewWindowOptions{
		Title:     p.Name,
		Name:      p.Name,
		Cwd:       cwd,
		CallerCwd: d.callerCwd(cs),
		Workspace: p.Workspace,
		// Not focused unless asked: an agent starting a helper should not
		// pull the person out of the pane they are in.
		Focus:   p.Focus,
		Command: command,
		Env:     env,
		Grants:  grants,
		// Checked above, on the agent's own argv.
		modeChecked: true,
	}, onExit)
	if bverr != nil {
		return nil, bverr
	}
	if err != nil {
		return nil, newWindowErr(err, sess, p.Workspace)
	}
	if parent != nil {
		sess.setAgentParent(win.ID, parent)
	} else {
		d.noteStartedBy(cs, win.ID)
	}
	// The card is the new pane's from the start, before the wait, so the
	// board shows who works on it while the agent loads.
	d.noteAgentStarted(sess, win.ID)
	var taskRow *TaskRow
	taskNote := ""
	if taskHost != "" {
		res, verr := d.forwardTaskFor(sess.Name(), win.ID, "claim-task", taskHost, p.TaskSession, p.Task)
		if verr != nil {
			taskNote = "Task card " + p.Task + " on " + taskHost + " was not claimed: " + verr.Message
		} else if row, ok := res.(TaskRow); ok {
			taskRow = &row
		}
	}
	if task != nil {
		// The card's session cannot read the pane, so it keeps a name: the
		// pane's own, or the agent's when the pane has none yet.
		holderName := windowLabelOf(win)
		if reg := d.agentMatcher.registry; p.Name == "" && reg != nil && launch.harness != "" {
			if m := reg.Lookup(launch.harness); m != nil && m.DisplayName != "" {
				holderName = m.DisplayName
			}
		}
		if row, verr := d.claimForPane(task, win.ID, holderName, launch.harness, task.sess != sess); verr != nil {
			taskNote = "Task card " + task.id + " was not claimed: " + verr.Message
		} else {
			taskRow = &row
			// A pane that closed before the claim landed lets the card go
			// now: its close found nothing to let go.
			if _, ok := findWindowState(sess.GetState(), win.ID); !ok {
				d.remoteHolds.take(win.ID)
				d.releaseHeld(task.sess, win.ID, taskReleasePaneClosed)
			}
		}
	}
	if taskRow != nil && p.TaskMail {
		d.mailHeldCard(sess, win, *taskRow, taskHost)
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
	var w WindowState
	var outcome agentStartOutcome
	if p.NoWait {
		w, outcome = d.waitAgentStart(sess, win.ID, launch.harness, startAgentEarlyExit, true, p.Protocol != "", nil)
		if outcome == agentStartTimeout {
			outcome = agentStartStarted
		}
	} else {
		w, outcome = d.waitAgentStart(sess, win.ID, launch.harness, timeout, true, p.Protocol != "", nil)
	}
	out := map[string]any{
		"type":            "agent_started",
		"session":         sess.Name(),
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
		"path_from":       launch.pathFrom(pathList != ""),
	}
	if cloned {
		out["cloned"] = true
	}
	if parent != nil {
		out["parent"] = parent.ref
		out["root"] = parent.link.Root
	}
	if p.Protocol != "" {
		out["protocol"] = p.Protocol
	}
	if taskRow != nil {
		out["task"] = *taskRow
	} else if taskNote != "" {
		out["task_note"] = taskNote
	}
	if w.ID != "" {
		out["name"] = windowLabelOf(w)
		if h := w.AgentHarness; h != "" {
			out["agent"] = h
		}
	}
	switch outcome {
	case agentStartStarted:
		out["reason"] = "Started without waiting for the agent to be ready (no_wait). If it stays unready, the Inbox says so."
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
	// With no_wait the after_ready steps and the prompt go in the background,
	// once the agent is ready, so the answer does not wait for them either.
	if p.NoWait && (outcome == agentStartStarted || outcome == agentStartReady) && (len(p.AfterReady) > 0 || p.Prompt != "") {
		if len(p.AfterReady) > 0 {
			out["after_ready_status"] = PromptPending
		}
		if p.Prompt != "" {
			out["prompt_status"] = PromptPending
		}
		go d.deliverStartAfter(sess, win.ID, launch.harness, timeout, p.Protocol != "", p.AfterReady, p.Prompt)
		return out, nil
	}
	if len(p.AfterReady) > 0 {
		if outcome != agentStartReady {
			out["after_ready_status"] = PromptNotSent
			out["after_ready_note"] = "The agent was not ready, so the after_ready steps were not sent."
		} else {
			status, note := d.sendAfterReady(sess, win.ID, p.AfterReady)
			out["after_ready_status"] = status
			if note != "" {
				out["after_ready_note"] = note
			}
			if status != PromptSent && p.Prompt != "" {
				out["prompt_status"] = PromptNotSent
				out["prompt_note"] = "The after_ready steps stopped, so the prompt was not typed."
				return out, nil
			}
		}
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

// deliverStartAfter is the rest of start-agent with no_wait: it waits for the
// agent to be ready, then sends the after_ready steps and types the prompt, as
// start-agent does in the call when it waits. An agent that stays unready
// raises the Inbox item waitAgentStart opens, and nothing is sent.
func (d *Daemon) deliverStartAfter(sess *Session, windowID, harness string, timeout time.Duration, reportedOnly bool, afterReady []string, prompt string) {
	if _, outcome := d.waitAgentStart(sess, windowID, harness, timeout, false, reportedOnly, nil); outcome != agentStartReady {
		return
	}
	if len(afterReady) > 0 {
		if status, _ := d.sendAfterReady(sess, windowID, afterReady); status != PromptSent {
			return
		}
	}
	if prompt != "" {
		d.typeFirstPrompt(sess, windowID, prompt)
	}
}
