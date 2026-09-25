package session

import (
	"errors"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Starting an agent: what fan and start-agent share.
//
// An agent is named the way a person types it, as one string: "claude",
// "codex --model o5", or a program no manifest knows. The first word is a
// harness id (claude-code), a program name a manifest detects (claude), or any
// program; the rest are its arguments. The string is split into words here,
// with quotes and backslashes read the way a POSIX shell reads them, and the
// program is exec'd directly. No shell ever parses it, so nothing in the string
// is expanded, substituted or redirected.
//
// The program is found on the caller's PATH when the caller sent one (env), and
// on the daemon's otherwise. A daemon started from a login shell long ago has a
// PATH of its own, and an agent installed since, or into a directory only the
// caller's shell adds, was reported as not installed although the caller could
// run it.
//
// Being ready for a first prompt is positive evidence: the pane reads idle or
// done, from a report, a hook, the screen or the title. unknown counts only for
// a harness whose manifest can never show idle, as agentReady says. A program no
// manifest knows is ready only when it reports a state itself. While an agent
// is not ready for longer than agentHeldAfter, the Inbox says so, because the
// most common reason is a first-run screen only the person can answer.

// agentHeldAfter is how long an agent the daemon started may sit unready before
// the Inbox asks the person to look. It is a variable so tests can shorten it.
var agentHeldAfter = 30 * time.Second

// callerEnvMax bounds a caller's environment: the number of variables, the
// size of one value, and the size of all of them.
const (
	callerEnvMaxVars  = 64
	callerEnvMaxValue = 32 << 10
	callerEnvMaxTotal = 256 << 10
)

// envNameRE is a portable environment variable name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// agentLaunch is one agent to start: the argv to exec and the harness it is,
// when a manifest recognises it.
type agentLaunch struct {
	// spec is the string as the caller wrote it.
	spec string
	// argv is what is exec'd. argv[0] is an absolute path when the caller
	// sent a PATH, and the program name as written otherwise.
	argv []string
	// harness is the manifest id, empty for a program no manifest knows.
	harness string
}

// command is the argv as one line, for a result a person reads.
func (l agentLaunch) command() string { return strings.Join(l.argv, " ") }

// splitAgentWords splits an agent string into words the way a POSIX shell
// splits a simple command: whitespace separates words, single quotes keep
// everything literal, double quotes keep everything but a backslash before
// " or \, and a backslash outside quotes keeps the next character. Nothing is
// expanded.
func splitAgentWords(s string) ([]string, error) {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped, inWord = true, true
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("a quote is not closed")
	}
	if escaped {
		return nil, errors.New("it ends with a backslash")
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, nil
}

// lookPathIn finds a program on a PATH list the way exec.LookPath finds it on
// the process's own. A directory in the list that is not absolute is skipped:
// "." in a caller's PATH means the caller's directory, which the daemon is not
// in. A name with a separator must be an absolute path.
func lookPathIn(name, pathList string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		if !filepath.IsAbs(name) {
			return "", errors.New("a program path must be absolute")
		}
		return exec.LookPath(name)
	}
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}

// resolveAgentLaunch turns an agent string into the argv to exec. pathList is
// the caller's PATH, empty to use the daemon's.
func (d *Daemon) resolveAgentLaunch(param, spec, pathList string) (agentLaunch, *verbError) {
	spec = strings.TrimSpace(spec)
	words, err := splitAgentWords(spec)
	if err != nil {
		return agentLaunch{}, invalidParam(param, "agent "+echoName(spec)+" cannot be read: "+err.Error())
	}
	if len(words) == 0 || words[0] == "" {
		return agentLaunch{}, invalidParam(param, "name the agent to start: a harness such as claude, codex or gemini, or a program, with its arguments after it")
	}
	launch := agentLaunch{spec: spec}
	name, display := words[0], words[0]
	if reg := d.agentMatcher.registry; reg != nil {
		if m, cmd, ok := reg.Resolve(name); ok {
			launch.harness = m.ID
			display = m.DisplayName
			// A harness id names the program that starts it; a program
			// name the manifest detects is kept as written.
			if reg.Lookup(name) != nil {
				name = cmd
			}
		}
	}
	if strings.ContainsAny(name, `/\`) && !filepath.IsAbs(name) {
		// Relative to what? The caller's directory is not the daemon's.
		return agentLaunch{}, invalidParam(param, "agent "+echoName(name)+": a program path must be absolute")
	}
	var path string
	if pathList != "" {
		path, err = lookPathIn(name, pathList)
	} else {
		// exec resolves the name again at spawn, on the same PATH, so the
		// lookup only checks that it is there and the name as written is
		// what is kept.
		_, err = exec.LookPath(name)
		path = name
	}
	if err != nil {
		where := "the daemon's PATH"
		if pathList != "" {
			where = "the PATH the caller sent"
		}
		hint := &VerbHint{
			Param:  param,
			Detail: "Install it, name a program that is installed, or pass env with the PATH it is on. The tuios CLI sends its own PATH.",
		}
		// A name no manifest knows may be a harness spelled wrong, so the
		// ones tuios recognises are listed, with the closest.
		if launch.harness == "" && d.agentMatcher.registry != nil {
			ids := d.agentMatcher.registry.IDs()
			hint.Available, hint.DidYouMean = ids, closestMatch(name, ids)
		}
		return agentLaunch{}, hintedVerbError(ErrVerbInvalidParams, display+" is not installed: "+name+" is not on "+where, hint)
	}
	launch.argv = append([]string{path}, words[1:]...)
	return launch, nil
}

// callerEnv checks the environment a caller sent and returns it as KEY=VALUE
// pairs in name order, with the PATH among them, which is where programs are
// looked up. A call from another machine may not send one: its variables name
// directories and settings of that machine. A TUIOS_ variable is refused, since
// those are the contract a pane's process reads its identity from, and so are
// TMUX and TMUX_PANE, which the daemon strips on purpose (guestenv).
func callerEnv(cs *connState, env map[string]string) ([]string, string, *verbError) {
	if len(env) == 0 {
		return nil, "", nil
	}
	if cs != nil && (cs.viaLink || cs.paneOnly) {
		return nil, "", hintedVerbError(ErrVerbForbidden, "env from another machine is refused: its variables describe that machine", &VerbHint{
			Param:  "env",
			Detail: "Nothing was started. Call without env, and the program is found on this machine's PATH.",
		})
	}
	if len(env) > callerEnvMaxVars {
		return nil, "", invalidParam("env", "env carries at most 64 variables")
	}
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	total := 0
	out := make([]string, 0, len(names))
	for _, k := range names {
		v := env[k]
		switch {
		case !envNameRE.MatchString(k):
			return nil, "", invalidParam("env", "env: "+echoName(k)+" is not a variable name")
		case strings.HasPrefix(k, "TUIOS_") || k == "TUIOS":
			return nil, "", invalidParam("env", "env: "+echoName(k)+" is set by tuios for every pane and cannot be passed")
		case k == "TMUX" || k == "TMUX_PANE":
			return nil, "", invalidParam("env", "env: "+k+" would make the agent believe it runs in tmux, and cannot be passed")
		case strings.ContainsRune(v, 0):
			return nil, "", invalidParam("env", "env: the value of "+k+" holds a NUL byte")
		case len(v) > callerEnvMaxValue:
			return nil, "", invalidParam("env", "env: the value of "+k+" is longer than 32 KiB")
		}
		total += len(k) + len(v) + 1
		if total > callerEnvMaxTotal {
			return nil, "", invalidParam("env", "env is larger than 256 KiB")
		}
		out = append(out, k+"="+v)
	}
	return out, env["PATH"], nil
}

// agentStartOutcome is how the wait for a just-started agent ended.
type agentStartOutcome string

const (
	agentStartReady        agentStartOutcome = "ready"
	agentStartBlocked      agentStartOutcome = "blocked"
	agentStartTimeout      agentStartOutcome = "timeout"
	agentStartWindowClosed agentStartOutcome = "window_closed"
	agentStartSessionEnded agentStartOutcome = "session_closed"
	agentStartShutdown     agentStartOutcome = "shutdown"
)

// readyBy names the evidence a ready pane showed: its state, or quiet for a
// pane on unknown whose harness can never show more.
func readyBy(w WindowState) string {
	if w.AgentState == AgentStateUnknown {
		return "quiet"
	}
	return w.AgentState.Name()
}

// waitAgentStart waits for an agent the daemon just started to be ready for
// its first prompt (fanReadyStates, through agentReady). With stopOnBlocked a
// pane on needs_input ends the wait at once; without it the wait goes on, since
// what the pane asks is the person's to answer and the prompt comes after.
//
// When the pane has not been ready for agentHeldAfter and is not on
// needs_input, which raises its own Inbox item, held is called once with the
// pane as it is, and the Inbox gets an item saying the agent waits at a screen
// tuios does not recognise. The item is closed when the wait ends, and by the
// pane's next state change.
//
// harness is the harness the daemon started, when a manifest named it. It
// stands in for detection until detection has run: without it, a pane that
// reads unknown before the detector has named its harness would count as a
// harness that can never show idle, and the prompt would be typed into
// whatever the agent shows first.
//
// reportedOnly counts only a state the pane shows, never unknown, whatever the
// harness: a protocol pane's screen is a transcript no manifest rule reads, and
// its own report is the evidence.
func (d *Daemon) waitAgentStart(sess *Session, windowID, harness string, timeout time.Duration, stopOnBlocked, reportedOnly bool, held func(WindowState)) (WindowState, agentStartOutcome) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		types:   map[string]bool{EventAgentState: true, EventWindowClosed: true, EventSessionClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)

	current := func() (WindowState, bool) {
		st := sess.GetState()
		i, err := findWindowStateIndex(st.Windows, windowID)
		if err != nil {
			return WindowState{}, false
		}
		return st.Windows[i], true
	}
	heldSummary := ""
	defer func() {
		if heldSummary != "" {
			d.attention.closeHeldPrompt(sess.Name, windowID, heldSummary)
		}
	}()
	check := func() (WindowState, agentStartOutcome, bool) {
		w, ok := current()
		if !ok {
			return w, agentStartWindowClosed, true
		}
		judged := w
		judged.AgentHarness = firstNonEmpty(w.AgentHarness, harness)
		ready := fanReadyStates[w.AgentState.Name()]
		if !reportedOnly {
			ready = d.agentReady(judged, fanReadyStates)
		}
		if ready {
			return w, agentStartReady, true
		}
		if stopOnBlocked && w.AgentState == AgentStateNeedsInput {
			return w, agentStartBlocked, true
		}
		return w, "", false
	}
	if w, outcome, done := check(); done {
		return w, outcome
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	heldTimer := time.NewTimer(agentHeldAfter)
	defer heldTimer.Stop()
	for {
		select {
		case <-deadline.C:
			w, _ := current()
			return w, agentStartTimeout
		case <-d.ctx.Done():
			w, _ := current()
			return w, agentStartShutdown
		case <-heldTimer.C:
			w, ok := current()
			if !ok || w.AgentState == AgentStateNeedsInput {
				continue
			}
			heldSummary = d.attention.openHeldPrompt(sess.Name, w)
			if held != nil {
				held(w)
			}
		case ev := <-sub.ch:
			if ev.Type == EventSessionClosed {
				return WindowState{}, agentStartSessionEnded
			}
			if w, outcome, done := check(); done {
				return w, outcome
			}
		}
	}
}

// typeFirstPrompt pastes and submits the first prompt into an agent that is
// ready for it, and waits for the pane to show it took it. It returns the
// prompt status (PromptSent, PromptStalled or PromptNotSent), a note for any
// but sent, and when the prompt was typed.
func (d *Daemon) typeFirstPrompt(sess *Session, windowID, text string) (string, string, int64) {
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		return PromptNotSent, "The agent's pane is gone.", 0
	}
	// Pasted and submitted with a carriage return, the way ask-agent types its
	// question. See prompt_submit.go.
	gate := d.newPromptGate(sess, windowID)
	at, err := submitPrompt(d.ctx, pty, text, d.inputProfileFor(sess, windowID))
	if err != nil {
		return PromptNotSent, "Could not write to the agent's pane: " + err.Error(), 0
	}
	// The prompt is sent only once the agent shows it took it. See
	// prompt_gate.go.
	gate.markSubmitted(at)
	stall := d.promptStall()
	taken, gone := d.waitPromptTaken(sess, pty, gate, stall)
	switch {
	case gone:
		return PromptNotSent, "The agent's window closed as the prompt was typed.", at.UnixNano()
	case !taken:
		return PromptStalled, "The prompt was typed and Enter was sent, and the agent showed no sign of taking it within " + stall.String() + ". Look at the pane before sending it again: it may be in the input box, waiting for Enter.", at.UnixNano()
	}
	return PromptSent, "", at.UnixNano()
}
