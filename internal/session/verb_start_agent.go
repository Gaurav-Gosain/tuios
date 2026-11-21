package session

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
		TaskHost     string            `json:"task_host"`
		TaskSession  string            `json:"task_session"`
		TaskMail     bool              `json:"task_mail"`
		Parent       string            `json:"parent"`
		AddDirs      []string          `json:"add_dirs"`
		AfterReady   []string          `json:"after_ready"`
		launchChoice
	}
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
	grants, verr := d.launchGrants(cs, p.Grants)
	if verr != nil {
		return nil, verr
	}
	parent, verr := d.resolveAgentParent(cs, "start-agent", p.Parent)
	if verr != nil {
		return nil, verr
	}
	if strings.TrimSpace(p.Agent) == "" {
		return nil, invalidParam("agent", "agent is required: the harness or program to start, with its arguments, such as claude or \"codex --model o5\"")
	}
	if p.Workspace < 0 {
		return nil, invalidParam("workspace", "workspace is a workspace number, e.g. 2. Omit it for the calling pane's workspace, else the one the session shows")
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
	extra = append(extra, d.replyMarkerArgs(launch.harness, p.Protocol)...)
	extra = append(extra, p.Args...)
	argv := append(append([]string{}, launch.argv...), extra...)
	if p.Protocol != "" {
		argv = protocolAgentArgv(p.Protocol, launch.argv, extra)
	}
	if argv, verr = d.clampChildMode(cs, "start-agent", launch.harness, argv, env, p.Protocol != ""); verr != nil {
		return nil, verr
	}
	command := argv
	if p.Protocol != "" {
		var verr *verbError
		if command, verr = d.protocolArgv(p.Protocol, launch.harness, argv); verr != nil {
			return nil, verr
		}
	}

	if _, _, verr := d.childWindowBound(cs, "start-agent", cwd); verr != nil {
		return nil, verr
	}
	sess := d.findTargetSession(cs, name)
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

	if p.Workspace == 0 {
		p.Workspace = d.callerWorkspaceIn(cs, sess)
	}

	sessionID := sess.ID
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
		Title:       p.Name,
		Name:        p.Name,
		Cwd:         cwd,
		CallerCwd:   d.callerCwd(cs),
		Workspace:   p.Workspace,
		Focus:       p.Focus,
		Command:     command,
		Env:         env,
		Grants:      grants,
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
	var w WindowState
	var outcome agentStartOutcome
	if p.NoWait {
		w, outcome = d.waitAgentStart(sess, win.ID, launch.harness, startAgentEarlyExit, true, p.Protocol != "", nil)
		if outcome == agentStartTimeout {
			outcome = agentStartStarted
			if pty := sess.GetPTY(win.PTYID); pty != nil {
				if _, exited := pty.ExitStatus(); exited {
					outcome = agentStartExited
				}
			}
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
	case agentStartExited:
		reason := "The agent exited at once."
		if pty := sess.GetPTY(win.PTYID); pty != nil {
			if code, _ := pty.ExitStatus(); code != 0 {
				reason = "The agent exited at once with status " + strconv.Itoa(code) + "."
			}
		}
		out["reason"] = reason + " What it printed is in the pane: look at it with capture-pane."
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

func startAgentSessionName(l agentLaunch) string {
	for _, name := range []string{l.harness, filepath.Base(l.argv[0])} {
		if name != "" && ValidateSessionName(name) == nil {
			return name
		}
	}
	return "agents"
}

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
