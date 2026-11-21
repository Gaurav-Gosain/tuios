package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

var agentHeldAfter = 30 * time.Second

const (
	callerEnvMaxVars  = 64
	callerEnvMaxValue = 32 << 10
	callerEnvMaxTotal = 256 << 10
)

var envNameRE = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)

type agentLaunch struct {
	spec       string
	argv       []string
	harness    string
	daemonPath bool
}

func (l agentLaunch) pathFrom(callerSent bool) string {
	if callerSent && !l.daemonPath {
		return "caller"
	}
	return "daemon"
}

func (l agentLaunch) withPath(env []string) []string {
	if !l.daemonPath {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+os.Getenv("PATH"))
}

func (l agentLaunch) command() string { return strings.Join(l.argv, " ") }

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
			if reg.Lookup(name) != nil {
				name = cmd
			}
		}
	}
	if strings.ContainsAny(name, `/\`) && !filepath.IsAbs(name) {
		return agentLaunch{}, invalidParam(param, "agent "+echoName(name)+": a program path must be absolute")
	}
	var path string
	if pathList != "" {
		path, err = lookPathIn(name, pathList)
		if err != nil {
			if own, ownErr := exec.LookPath(name); ownErr == nil {
				if abs, absErr := filepath.Abs(own); absErr == nil {
					path, err, launch.daemonPath = abs, nil, true
				}
			}
		}
	} else {
		_, err = exec.LookPath(name)
		path = name
	}
	if err != nil {
		where := "the daemon's PATH"
		if pathList != "" {
			where = "the PATH the caller sent, nor on the daemon's PATH, which new panes get"
		}
		hint := &VerbHint{
			Param:  param,
			Detail: "Install it, name a program that is installed, or pass env with the PATH it is on. The tuios CLI sends its own PATH.",
		}
		if launch.harness == "" && d.agentMatcher.registry != nil {
			ids := d.agentMatcher.registry.IDs()
			hint.Available, hint.DidYouMean = ids, closestMatch(name, ids)
		}
		return agentLaunch{}, hintedVerbError(ErrVerbInvalidParams, display+" is not installed: "+name+" is not on "+where, hint)
	}
	launch.argv = append([]string{path}, words[1:]...)
	return launch, nil
}

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
		case !envNameRE().MatchString(k):
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

type agentStartOutcome string

const (
	agentStartReady        agentStartOutcome = "ready"
	agentStartBlocked      agentStartOutcome = "blocked"
	agentStartTimeout      agentStartOutcome = "timeout"
	agentStartWindowClosed agentStartOutcome = "window_closed"
	agentStartSessionEnded agentStartOutcome = "session_closed"
	agentStartShutdown     agentStartOutcome = "shutdown"
	agentStartStarted      agentStartOutcome = "started"
	agentStartExited       agentStartOutcome = "exited"
)

const startAgentEarlyExit = time.Second

func readyBy(w WindowState) string {
	if w.AgentState == AgentStateUnknown {
		return "quiet"
	}
	return w.AgentState.Name()
}

func (d *Daemon) waitAgentStart(sess *Session, windowID, harness string, timeout time.Duration, stopOnBlocked, reportedOnly bool, held func(WindowState)) (WindowState, agentStartOutcome) {
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
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
			d.attention.closeHeldPrompt(sess.Name(), windowID, heldSummary)
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
			heldSummary = d.attention.openHeldPrompt(sess.Name(), w)
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

func (d *Daemon) typeFirstPrompt(sess *Session, windowID, text string) (string, string, int64) {
	if pty, err := d.resolvePTYForTarget(sess, windowID); err == nil && d.harnessKnown(sess, windowID) {
		if err := waitInputLive(d.ctx, pty, d.inputProfileFor(sess, windowID), inputLiveQuiet, inputLiveMaxWait); err != nil {
			return PromptNotSent, "Could not write to the agent's pane: " + err.Error(), 0
		}
	}
	return d.typePromptPastes(sess, windowID, []string{text})
}

func (d *Daemon) harnessKnown(sess *Session, windowID string) bool {
	reg := d.agentMatcher.registry
	if reg == nil || sess == nil {
		return false
	}
	sess.stateMu.RLock()
	defer sess.stateMu.RUnlock()
	for i := range sess.state.Windows {
		if sess.state.Windows[i].ID == windowID {
			return reg.Lookup(sess.state.Windows[i].AgentHarness) != nil
		}
	}
	return false
}

func (d *Daemon) typePromptPastes(sess *Session, windowID string, pastes []string) (string, string, int64) {
	status, note, at, _ := d.typePromptGated(sess, windowID, pastes)
	return status, note, at
}

func (d *Daemon) typePromptGated(sess *Session, windowID string, pastes []string) (string, string, int64, *promptGate) {
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		return PromptNotSent, "The agent's pane is gone.", 0, nil
	}
	clean := make([]string, len(pastes))
	for i, paste := range pastes {
		clean[i], _ = invisible.StripHidden(paste)
	}
	pastes = clean
	gate := d.newPromptGate(sess, windowID)
	at, err := submitPastes(d.ctx, pty, pastes, d.inputProfileFor(sess, windowID))
	if err != nil {
		return PromptNotSent, "Could not write to the agent's pane: " + err.Error(), 0, nil
	}
	gate.markSubmitted(at)
	stall := d.promptStall()
	taken, gone := d.waitPromptTaken(sess, pty, gate, stall)
	switch {
	case gone:
		return PromptNotSent, "The agent's window closed as the prompt was typed.", at.UnixNano(), nil
	case !taken:
		return PromptStalled, "The prompt was typed and Enter was sent, and the agent showed no sign of taking it within " + stall.String() + ". Look at the pane before sending it again: it may be in the input box, waiting for Enter.", at.UnixNano(), gate
	}
	return PromptSent, "", at.UnixNano(), gate
}

type launchChoice struct {
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
}

func (d *Daemon) withChoice(l agentLaunch, c launchChoice) (agentLaunch, *verbError) {
	var m *harness.Manifest
	if reg := d.agentMatcher.registry; reg != nil && l.harness != "" {
		m = reg.Lookup(l.harness)
	}
	var extra []string
	for _, one := range []struct {
		param string
		c     harness.LaunchChoice
	}{
		{"model", harness.LaunchChoice{Model: c.Model}},
		{"effort", harness.LaunchChoice{Effort: c.Effort}},
		{"permission_mode", harness.LaunchChoice{Permission: c.PermissionMode}},
	} {
		args, err := harness.LaunchArgs(m, one.c)
		if err != nil {
			return l, invalidParam(one.param, err.Error())
		}
		extra = append(extra, args...)
	}
	if len(extra) > 0 {
		l.argv = append(slices.Clip(l.argv), extra...)
	}
	return l, nil
}
