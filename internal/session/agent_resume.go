package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// Resuming an agent's conversation after the daemon restarts.
//
// A restart ends every program in every pane. A restore brings back the layout
// and starts a new shell in each pane, and nothing else, so the agent a pane
// was running is gone. What is not gone is the conversation: every harness
// with a resume command keeps it on disk, and a hook already told the daemon
// its id (the window's agent_session_id). So the restore offers to start the
// harness again on that conversation, which is the part a person actually
// lost.
//
// Three modes, from daemon.resume_agents:
//
//   - ask (the default): each restored pane with a resumable conversation gets
//     a resume item in the Inbox. The person answers it with y in the Inbox or
//     with tuios resume-agent, or dismisses it. Nothing is typed until then.
//   - auto: the daemon types the resume command into each restored shell once
//     the shell has drawn its prompt, 100 ms apart. A pane whose shell is not
//     at its prompt by then gets the ask item instead.
//   - off: neither. The id stays on the window, so resume-agent still works.
//
// The command is built from the harness manifest's [resume] template and the
// stored id, and from nothing else: no saved command line, prompt or
// environment is replayed. The id is held to a character set every shell reads
// as one plain argument (harness.ValidResumeSessionID), so an id a pane
// reported cannot turn into a second command. And the command is typed only
// into a pane whose own shell holds the terminal's foreground, so it never
// lands in an editor or in another agent's input box.

// Resume modes, the resolved daemon.resume_agents values.
const (
	resumeModeAsk  = "ask"
	resumeModeAuto = "auto"
	resumeModeOff  = "off"
)

// resolveResumeMode maps the configured value onto a mode. Anything that is
// not auto or off is ask, which types nothing without the person.
func resolveResumeMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case resumeModeAuto:
		return resumeModeAuto
	case resumeModeOff:
		return resumeModeOff
	}
	return resumeModeAsk
}

// resumeAutoStagger spaces the automatic resumes, so a restore of twenty
// agents does not start twenty harnesses in the same instant.
const resumeAutoStagger = 100 * time.Millisecond

// resumeShellWait bounds how long an automatic resume waits for a restored
// shell to draw its prompt, and resumeShellQuiet is how long the shell must
// then stay quiet before the command is typed. A shell whose startup prints in
// bursts is caught between them by the quiet window, not in the middle.
const (
	resumeShellWait  = 10 * time.Second
	resumeShellQuiet = 300 * time.Millisecond
)

// resumeOffer is one pane a restore brought back with a conversation that
// can be resumed.
type resumeOffer struct {
	session   string
	window    string
	workspace int
	name      string
	harness   string
	sessionID string
	argv      []string
}

// resumeHarnessOf is the harness a window's conversation id belongs to: the
// one recorded with the id, else, for state written before that was recorded,
// the pane's harness attribution.
func resumeHarnessOf(w WindowState) string {
	if w.AgentSessionHarness != "" {
		return w.AgentSessionHarness
	}
	return w.AgentHarness
}

// resumeOfferFor builds the offer for a restored window, or reports that it
// has nothing to resume: no conversation id, a harness with no resume command,
// or an id that cannot be typed safely.
func (d *Daemon) resumeOfferFor(sessionName string, w WindowState) (resumeOffer, bool) {
	if w.AgentSessionID == "" {
		return resumeOffer{}, false
	}
	hid := resumeHarnessOf(w)
	argv, err := d.agentMatcher.registry.ResumeArgv(hid, w.AgentSessionID)
	if err != nil {
		return resumeOffer{}, false
	}
	return resumeOffer{
		session:   sessionName,
		window:    w.ID,
		workspace: w.Workspace,
		name:      windowLabelOf(w),
		harness:   hid,
		sessionID: w.AgentSessionID,
		argv:      argv,
	}, true
}

// applyResumeOffers does what the configured mode says with a restore's
// offers.
func (d *Daemon) applyResumeOffers(offers []resumeOffer) {
	switch d.resumeAgents {
	case resumeModeOff:
		return
	case resumeModeAuto:
		for i, o := range offers {
			go d.autoResume(o, time.Duration(i)*resumeAutoStagger)
		}
		return
	}
	for _, o := range offers {
		d.attention.openResume(d.resumeItem(o))
	}
}

// resumeItem is the Inbox item that asks about an offer. The summary is the
// command itself, so the person sees exactly what y would type.
func (d *Daemon) resumeItem(o resumeOffer) AttentionItem {
	return AttentionItem{
		Kind:      AttentionResume,
		Session:   o.session,
		Window:    o.window,
		Workspace: o.workspace,
		Harness:   o.harness,
		Name:      attentionText(o.name, attentionMaxSummary),
		Summary:   attentionText(harness.ResumeCommandLine(o.argv), attentionMaxSummary),
	}
}

// autoResume types one offer's command once its restored shell is ready, and
// falls back to asking when the shell never gets there.
func (d *Daemon) autoResume(o resumeOffer, delay time.Duration) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-d.ctx.Done():
			return
		}
	}
	sess := d.manager.GetSession(o.session)
	if sess == nil {
		return
	}
	pty, err := d.resolvePTYForTarget(sess, o.window)
	if err != nil {
		return
	}
	if !waitShellPrompt(d.ctx, pty, resumeShellWait, resumeShellQuiet) {
		if d.ctx.Err() == nil {
			d.attention.openResume(d.resumeItem(o))
		}
		return
	}
	if _, err := d.typeResume(sess, o.window); err != nil {
		LogError("Automatic resume of %s in %s failed, asking instead: %v", o.harness, shortID(o.window), err)
		d.attention.openResume(d.resumeItem(o))
	}
}

// waitShellPrompt waits for a new shell to print something and then go quiet
// for quiet, which is a shell that has drawn its prompt. It reports false when
// maxWait runs out first, the shell exits or ctx ends.
func waitShellPrompt(ctx context.Context, pty *PTY, maxWait, quiet time.Duration) bool {
	deadline := time.Now().Add(maxWait)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if pty.IsExited() {
			return false
		}
		if last := pty.LastOutput(); last > 0 && time.Since(time.Unix(0, last)) >= quiet {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return false
		}
	}
}

// errResumeNothing says the window has no conversation a resume can name.
var errResumeNothing = errors.New("no resumable conversation")

// errResumeBusy says the pane's shell does not hold the foreground.
var errResumeBusy = errors.New("the pane is running a program")

// errResumeRemote says the window's process is on another machine.
var errResumeRemote = errors.New("the pane is on another machine")

// resumePlan is what a resume of one window would type, and why not when it
// cannot.
type resumePlan struct {
	window    WindowState
	harness   string
	sessionID string
	argv      []string
}

// planResume works out the command for a window without typing anything.
func (d *Daemon) planResume(sess *Session, target string) (resumePlan, error) {
	state := sess.GetState()
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return resumePlan{}, err
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return resumePlan{}, err
	}
	w := state.Windows[idx]
	if w.Host != "" {
		return resumePlan{window: w}, errResumeRemote
	}
	if w.AgentSessionID == "" {
		return resumePlan{window: w}, errResumeNothing
	}
	hid := resumeHarnessOf(w)
	argv, err := d.agentMatcher.registry.ResumeArgv(hid, w.AgentSessionID)
	if err != nil {
		return resumePlan{window: w, harness: hid, sessionID: w.AgentSessionID}, err
	}
	return resumePlan{window: w, harness: hid, sessionID: w.AgentSessionID, argv: argv}, nil
}

// shellHoldsForeground reports whether a pane's own shell is in the
// terminal's foreground, which is a pane at its prompt: nothing it runs would
// read what is typed. Where the kernel will not say (Windows, the BSDs), it
// falls back to what the detector last saw: no foreground program and no
// agent state.
func shellHoldsForeground(pty *PTY, w WindowState) bool {
	pid := pty.ShellPID()
	if pid <= 0 || pty.IsExited() {
		return false
	}
	if pgid, ok := readForegroundPGID(pid); ok {
		return pgid == pid
	}
	return w.ForegroundCmd == "" && w.AgentState == AgentStateNone
}

// typeResume types a window's resume command into its shell and closes the
// window's resume item. It refuses a pane whose shell is not at its prompt.
func (d *Daemon) typeResume(sess *Session, target string) (resumePlan, error) {
	plan, err := d.planResume(sess, target)
	if err != nil {
		return plan, err
	}
	pty, err := d.resolvePTYForTarget(sess, plan.window.ID)
	if err != nil {
		return plan, err
	}
	if !shellHoldsForeground(pty, plan.window) {
		return plan, errResumeBusy
	}
	line := harness.ResumeCommandLine(plan.argv) + "\r"
	if _, err := pty.Write([]byte(line)); err != nil {
		return plan, err
	}
	d.attention.closeResume(sess.Name, plan.window.ID, AttentionClosedResolved)
	return plan, nil
}

// verbResumeAgent types the resume command for a window's recorded
// conversation into the window's shell, or with dry_run only says what it
// would type.
//
// What a caller can do with it is strictly less than send-text, which every
// caller of this socket already has: the text is fixed by the harness
// manifest and the id stored on the window, the id is one plain shell token,
// and the pane must be sitting at its shell prompt. So it is not gated on the
// person the way dismiss-attention is: an agent that coordinates others may
// bring one back.
func (d *Daemon) verbResumeAgent(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		DryRun  bool   `json:"dry_run"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(p.Session)
	if verr != nil {
		return nil, verr
	}
	var plan resumePlan
	var err error
	if p.DryRun {
		plan, err = d.planResume(sess, p.Window)
	} else {
		plan, err = d.typeResume(sess, p.Window)
	}
	if err != nil {
		return nil, d.resumeError(sess, plan, err)
	}
	return map[string]any{
		"type":             "agent_resumed",
		"window_id":        plan.window.ID,
		"harness":          plan.harness,
		"agent_session_id": plan.sessionID,
		"argv":             plan.argv,
		"command":          harness.ResumeCommandLine(plan.argv),
		"typed":            !p.DryRun,
	}, nil
}

// resumeError maps a failed resume onto the error a caller can act on.
func (d *Daemon) resumeError(sess *Session, plan resumePlan, err error) *verbError {
	switch {
	case errors.Is(err, errResumeBusy):
		return hintedVerbError(ErrVerbNotReady, "the pane is running a program, so nothing was typed", &VerbHint{
			Verb:    "capture-pane",
			Command: "tuios capture-pane -w " + shortWindowID(plan.window.ID),
			Detail:  "resume-agent types only into a pane whose shell is at its prompt, so the command cannot land in an editor or another agent. Quit what runs there, or open a new pane, and try again.",
		})
	case errors.Is(err, errResumeRemote):
		return hintedVerbError(ErrVerbNotResumable, "the pane runs on "+echoName(plan.window.Host)+", resume it there", &VerbHint{
			Detail: "The conversation belongs to the machine the pane runs on.",
		})
	case errors.Is(err, errResumeNothing):
		return hintedVerbError(ErrVerbNotResumable, "no conversation is recorded for this pane", &VerbHint{
			Verb:    "set-agent-session",
			Command: "tuios integration install claude-code",
			Detail:  "A pane has a conversation to resume once its harness's hook reported one (agent_session_id). tuios integration install sets that up for the harnesses that support it.",
		})
	case errors.Is(err, harness.ErrNoResume):
		return hintedVerbError(ErrVerbNotResumable, "harness "+echoName(plan.harness)+" has no resume command", &VerbHint{
			Command: "tuios explain-agent-detect",
			Detail:  "A harness manifest names its resume command in a [resume] block. Add one in a manifest under the user harness directory to resume this harness.",
		})
	case errors.Is(err, harness.ErrBadResumeID):
		return hintedVerbError(ErrVerbNotResumable, "the recorded conversation id cannot be typed safely", &VerbHint{
			Detail: "A resume command takes an id of letters, digits and _ . / : - only, which every shell reads as one argument. This id is not one, so it is never typed.",
		})
	}
	return mapResolveErr(err, sess)
}
