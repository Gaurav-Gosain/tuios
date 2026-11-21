package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

const (
	resumeModeAsk  = "ask"
	resumeModeAuto = "auto"
	resumeModeOff  = "off"
)

func resolveResumeMode(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case resumeModeAuto:
		return resumeModeAuto
	case resumeModeOff:
		return resumeModeOff
	}
	return resumeModeAsk
}

const resumeAutoStagger = 100 * time.Millisecond

const (
	resumeShellWait  = 10 * time.Second
	resumeShellQuiet = 300 * time.Millisecond
	resumePromptPoll = 25 * time.Millisecond
)

type resumeOffer struct {
	session   string
	window    string
	workspace int
	name      string
	harness   string
	sessionID string
	argv      []string
	risky     []string
}

// ResumeWarning is what a resume command that carries flags turning off
// approvals or sandboxing says about them, wherever the command is shown: a
// Resume row, the agents view's card, list-attention and resume-agent. Empty
// for none.
func ResumeWarning(risky []string) string {
	if len(risky) == 0 {
		return ""
	}
	return "Resumes with approvals off (" + strings.Join(risky, ", ") + ")"
}

func resumeHarnessOf(w WindowState) string {
	if w.AgentSessionHarness != "" {
		return w.AgentSessionHarness
	}
	return w.AgentHarness
}

func resumeFlagsOf(w WindowState, hid string) []string {
	if w.AgentFlags == nil || w.AgentFlags.Harness != hid {
		return nil
	}
	return w.AgentFlags.Flags
}

func agentWasLive(w WindowState) bool {
	return w.AgentHarness != "" || w.AgentState != AgentStateNone
}

func (d *Daemon) resumeOfferFor(sessionName string, w WindowState) (resumeOffer, bool) {
	if !d.agentsEnabled() {
		return resumeOffer{}, false
	}
	if w.AgentSessionID == "" || !agentWasLive(w) {
		return resumeOffer{}, false
	}
	hid := resumeHarnessOf(w)
	argv, err := d.agentMatcher.registry.ResumeArgv(hid, w.AgentSessionID, resumeFlagsOf(w, hid))
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
		risky:     d.agentMatcher.registry.RiskyFlags(hid, argv),
	}, true
}

func (d *Daemon) applyResumeOffers(offers []resumeOffer) {
	switch d.resumeAgents {
	case resumeModeOff:
		return
	case resumeModeAuto:
		for i, o := range offers {
			if len(o.risky) > 0 {
				d.attention.openResume(d.resumeItem(o))
				continue
			}
			go d.autoResume(o, time.Duration(i)*resumeAutoStagger)
		}
		return
	}
	for _, o := range offers {
		d.attention.openResume(d.resumeItem(o))
	}
}

func (d *Daemon) resumeItem(o resumeOffer) AttentionItem {
	return AttentionItem{
		Kind:       AttentionResume,
		Session:    o.session,
		Window:     o.window,
		Workspace:  o.workspace,
		Harness:    o.harness,
		Name:       attentionText(o.name, attentionMaxSummary),
		Summary:    attentionText(harness.ResumeCommandLine(o.argv), attentionMaxSummary),
		RiskyFlags: o.risky,

		AgentSessionID: o.sessionID,
	}
}

func (d *Daemon) autoResume(o resumeOffer, delay time.Duration) {
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-d.ctx.Done():
			return
		}
	}
	sess, _ := d.manager.ResolveSession(o.session)
	if sess == nil {
		return
	}
	pty, err := d.resolvePTYForTarget(sess, o.window)
	if err != nil {
		return
	}
	deadline := time.Now().Add(resumeShellWait)
	for waitShellPrompt(d.ctx, pty, deadline, resumeShellQuiet) {
		if d.ctx.Err() != nil || pty.IsExited() {
			return
		}
		if _, err = d.typeResume(sess, o.window); err == nil {
			return
		}
		if !errors.Is(err, errResumeBusy) || !time.Now().Before(deadline) {
			break
		}
		select {
		case <-time.After(min(resumePromptPoll, time.Until(deadline))):
		case <-d.ctx.Done():
			return
		}
	}
	if d.ctx.Err() == nil && !pty.IsExited() {
		if err != nil {
			LogError("Automatic resume of %s in %s failed, asking instead: %v", o.harness, shortID(o.window), err)
		}
		d.attention.openResume(d.resumeItem(o))
	}
}

func waitShellPrompt(ctx context.Context, pty *PTY, deadline time.Time, quiet time.Duration) bool {
	tick := time.NewTicker(resumePromptPoll)
	defer tick.Stop()
	for {
		if ctx.Err() != nil || pty.IsExited() {
			return false
		}
		if !time.Now().Before(deadline) {
			return false
		}
		facts := pty.ShellFacts()
		if facts.PromptReady {
			return true
		}
		if last := pty.LastOutput(); !facts.Seen && last > 0 && time.Since(time.Unix(0, last)) >= quiet {
			return true
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return false
		}
	}
}

var errResumeNothing = errors.New("no resumable conversation")

var errResumeBusy = errors.New("the pane is running a program")

var errResumeRemote = errors.New("the pane is on another machine")

type resumePlan struct {
	window    WindowState
	harness   string
	sessionID string
	argv      []string
	risky     []string
}

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
	argv, err := d.agentMatcher.registry.ResumeArgv(hid, w.AgentSessionID, resumeFlagsOf(w, hid))
	if err != nil {
		return resumePlan{window: w, harness: hid, sessionID: w.AgentSessionID}, err
	}
	return resumePlan{window: w, harness: hid, sessionID: w.AgentSessionID, argv: argv,
		risky: d.agentMatcher.registry.RiskyFlags(hid, argv)}, nil
}

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
	d.attention.closeResume(sess.Name(), plan.window.ID, AttentionClosedResolved)
	return plan, nil
}

func (d *Daemon) verbResumeAgent(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		DryRun  bool   `json:"dry_run"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
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
		"risky_flags":      plan.risky,
		"warning":          ResumeWarning(plan.risky),
		"typed":            !p.DryRun,
	}, nil
}

func (d *Daemon) resumeError(sess *Session, plan resumePlan, err error) *verbError {
	switch {
	case errors.Is(err, errResumeBusy):
		return hintedVerbError(ErrVerbNotReady, "the pane is running a program, so nothing was typed", &VerbHint{
			Verb:    "capture-pane",
			Command: "tuios capture-pane " + paneArgs(sess, plan.window.ID),
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
