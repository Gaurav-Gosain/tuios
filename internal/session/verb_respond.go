package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// Peek and respond: read the prompt an agent is blocked on, and answer it,
// without attaching to its pane.
//
// peek-prompt reads what the harness's needs_input rule reads on the pane
// right now: the prompt's lines, its numbered options, how long the pane has
// waited, and which answers the rule declares (see harness/answers.go). It is
// a read, open to any caller, the way capture-pane is.
//
// respond answers. It is the person acting, so it is held to the rule every
// other act as the person is held to: the call carries the nonce of a client
// attached right now and comes from a process that may act as the person (see
// human_sender.go and human_origin.go), or the daemon runs with the
// respond_from_shell grant and the caller is a process the kernel names that
// runs outside every pane. An agent in a pane gets neither, so it cannot
// approve its own tool call or anyone else's.
//
// Pressing a key into a prompt is only safe while the prompt is the one the
// caller read, so respond reads the screen again right before it writes:
//
//  1. The pane must be on needs_input, and a needs_input rule with answers
//     must read a prompt on it now. Otherwise prompt_changed.
//  2. When the caller passes the prompt_id a peek gave it, the prompt now must
//     have the same id. The id covers the rule, the lines it read, the options
//     and when the pane entered its state, so a prompt answered and asked
//     again is a different prompt. Otherwise prompt_changed.
//  3. The prompt must not be one this daemon already answered. Two clients
//     answering the same prompt both pass 2 when they race, and the second one
//     is refused here with prompt_changed: the first answer wins.
//  4. The action must be one the rule offers for what is on the screen: an
//     answer bound to an option label is offered only while that option is
//     shown. Otherwise invalid_params, naming the actions that are.
//
// Steps 1 to 4 and the write run under one lock per window, so nothing else
// answering through tuios can move the prompt between the check and the key.
// Then respond waits, up to timeout, for the pane to leave needs_input or for
// the prompt to change, and returns what the pane says then.

// promptContextLines is how much of the pane bottom a peek reads for the
// options and the lines around them. It is wider than a rule reads, since a
// rule can key on the footer of a menu whose options sit above it.
const promptContextLines = 20

// respondDefaultWait and respondMaxWait bound respond's wait for the answer to
// take.
const (
	respondDefaultWait = 5 * time.Second
	respondMaxWait     = 30 * time.Second
	respondPoll        = 50 * time.Millisecond
	// respondStateGrace is how long the state has to follow once the prompt
	// is gone: past the screen tier's settle delay.
	respondStateGrace = screenSettleDelay + 200*time.Millisecond
)

// maxPromptLineRunes bounds one line a peek returns.
const maxPromptLineRunes = 400

// maxRespondText bounds a typed answer.
const maxRespondText = 4096

// respondSlots holds one slot per window that has been answered, keyed by
// session id and window id. The zero value is ready to use.
type respondSlots struct {
	mu    sync.Mutex
	slots map[string]*respondSlot
}

// respondSlot serialises respond on one window and remembers the prompt id it
// last answered.
type respondSlot struct {
	mu       sync.Mutex
	answered string
	// users counts the calls that took the slot and have not released it.
	// It is guarded by respondSlots.mu, not by mu, so a slot a call has taken
	// but not yet locked is never dropped from under it.
	users int
}

// maxRespondSlots bounds the map. Past it, slots no call holds are dropped: a
// slot only remembers the last answer, and a window answered long ago that
// shows the very same prompt again is a new prompt anyway, since its state
// time moved.
const maxRespondSlots = 512

// slot takes the slot for a window, making it on first use. The caller
// releases it with release when it is done.
func (r *respondSlots) slot(key string) *respondSlot {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slots == nil {
		r.slots = make(map[string]*respondSlot)
	}
	if s, ok := r.slots[key]; ok {
		s.users++
		return s
	}
	if len(r.slots) >= maxRespondSlots {
		for k, s := range r.slots {
			if s.users == 0 {
				delete(r.slots, k)
			}
		}
	}
	s := &respondSlot{users: 1}
	r.slots[key] = s
	return s
}

// release gives back a slot slot took.
func (r *respondSlots) release(s *respondSlot) {
	r.mu.Lock()
	s.users--
	r.mu.Unlock()
}

// promptLook is what a look at a blocked pane found.
type promptLook struct {
	window WindowState
	// prompt is valid when found is true.
	prompt harness.Prompt
	found  bool
	// id names the prompt; empty when none was found.
	id string
	// reason says in words why nothing can be answered, when that is so.
	reason string
}

// lookAtPrompt reads the prompt on a window now. The window must exist; the
// caller resolved it.
func (d *Daemon) lookAtPrompt(sess *Session, windowID string) (promptLook, *verbError) {
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, windowID)
	if err != nil {
		return promptLook{}, mapResolveErr(err, sess)
	}
	w := st.Windows[i]
	look := promptLook{window: w}
	if w.AgentState != AgentStateNeedsInput {
		look.reason = "the pane is not waiting on a prompt: its state is " + w.AgentState.Name()
		return look, nil
	}
	reg := d.agentMatcher.registry
	hid := w.AgentHarness
	if reg == nil || hid == "" {
		look.reason = "tuios does not know which agent runs in the pane, so no rule can read its prompt"
		return look, nil
	}
	pty := sess.GetPTY(w.PTYID)
	if pty == nil {
		return promptLook{}, hintedVerbError(ErrVerbPTYNotFound, "the pane's shell has exited", nil)
	}
	var screen harness.Prompt
	screenOK := false
	if lines := reg.ScreenLines(hid); lines > 0 {
		screen, screenOK = reg.ScreenPrompt(hid, pty.tailText(lines), pty.tailText(max(lines, promptContextLines)))
	}
	title, titleOK := reg.TitlePrompt(hid, pty.Title())
	switch {
	case screenOK && screen.Answerable():
		look.prompt, look.found = screen, true
	case titleOK && title.Answerable():
		look.prompt, look.found = title, true
	case screenOK:
		look.prompt, look.found = screen, true
	case titleOK:
		look.prompt, look.found = title, true
	default:
		look.reason = "no rule of " + hid + " reads a prompt on the pane now"
		return look, nil
	}
	look.id = promptID(w, hid, look.prompt)
	if !look.prompt.Answerable() {
		look.reason = "the rule that reads this prompt declares no answers, so it is answered in the pane"
	}
	return look, nil
}

// promptID names one prompt on one window: the rule that read it, what it
// read, the options, and when the window entered its state. It is a hash so
// it can travel without carrying the screen.
func promptID(w WindowState, hid string, p harness.Prompt) string {
	h := sha256.New()
	put := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	put(w.ID)
	put(strconv.FormatInt(w.AgentStateAt, 10))
	put(hid)
	put(p.Source)
	put(strconv.Itoa(p.Rule))
	for _, l := range p.Lines {
		put(l)
	}
	for _, o := range p.Options {
		put(strconv.Itoa(o.N) + "." + o.Label)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// printableLine keeps a screen line's indentation and drops what a terminal
// would act on, cut to maxPromptLineRunes.
func printableLine(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			continue
		}
		b.WriteRune(r)
		if n++; n >= maxPromptLineRunes {
			break
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// resolvePromptWindow resolves the session and window a peek or respond names.
func (d *Daemon) resolvePromptWindow(sessionName, window string) (*Session, WindowState, *verbError) {
	if window == "" {
		return nil, WindowState{}, invalidParam("window", "window is required: name the pane whose prompt to read")
	}
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, WindowState{}, verr
	}
	st := sess.GetState()
	i, err := findWindowStateIndex(st.Windows, window)
	if err != nil {
		return nil, WindowState{}, mapResolveErr(err, sess)
	}
	return sess, st.Windows[i], nil
}

// verbPeekPrompt answers what the prompt on a pane is and how it may be
// answered.
func (d *Daemon) verbPeekPrompt(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, w, verr := d.resolvePromptWindow(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	look, verr := d.lookAtPrompt(sess, w.ID)
	if verr != nil {
		return nil, verr
	}
	return peekResult(sess, look, time.Now()), nil
}

// PromptPeek is the peek-prompt result as a client decodes it. The daemon
// writes the same fields in peekResult.
type PromptPeek struct {
	Session    string           `json:"session"`
	Window     string           `json:"window"`
	Name       string           `json:"name"`
	Harness    string           `json:"harness"`
	State      string           `json:"state"`
	StateAt    int64            `json:"state_at"`
	WaitingMS  int64            `json:"waiting_ms"`
	Blocked    bool             `json:"blocked"`
	Found      bool             `json:"found"`
	Answerable bool             `json:"answerable"`
	Reason     string           `json:"reason"`
	Source     string           `json:"source"`
	Kind       string           `json:"kind"`
	Message    string           `json:"message"`
	PromptID   string           `json:"prompt_id"`
	Lines      []string         `json:"lines"`
	Options    []harness.Option `json:"options"`
	Actions    []string         `json:"actions"`
}

// Offers reports whether the peek lists action among the answers.
func (p *PromptPeek) Offers(action string) bool {
	return slices.Contains(p.Actions, action)
}

// PromptResponse is the respond result as a client decodes it.
type PromptResponse struct {
	Window    string `json:"window"`
	Action    string `json:"action"`
	Sent      string `json:"sent"`
	PromptID  string `json:"prompt_id"`
	SettledBy string `json:"settled_by"`
	State     string `json:"state"`
	Message   string `json:"message"`
}

// peekResult is the peek-prompt answer for a look.
func peekResult(sess *Session, look promptLook, now time.Time) map[string]any {
	w := look.window
	waiting := int64(0)
	if w.AgentState == AgentStateNeedsInput && w.AgentStateAt > 0 {
		waiting = max(now.Sub(time.Unix(0, w.AgentStateAt)).Milliseconds(), 0)
	}
	lines := []string{}
	options := []harness.Option{}
	actions := []string{}
	source, kind, message := "", "", ""
	rule := -1
	if look.found {
		pr := look.prompt
		for _, l := range pr.Lines {
			lines = append(lines, printableLine(l))
		}
		for _, o := range pr.Options {
			options = append(options, harness.Option{N: o.N, Label: printableLine(o.Label)})
		}
		if a := pr.Actions(); a != nil {
			actions = a
		}
		source, kind, message, rule = pr.Source, pr.Kind, printableLine(pr.Message), pr.Rule
	}
	if kind == "" {
		kind = w.AgentKind
	}
	return map[string]any{
		"type":       "prompt_peek",
		"session":    sess.Name,
		"window":     w.ID,
		"name":       windowLabelOf(w),
		"harness":    w.AgentHarness,
		"state":      w.AgentState.Name(),
		"state_at":   w.AgentStateAt,
		"waiting_ms": waiting,
		"blocked":    w.AgentState == AgentStateNeedsInput,
		"found":      look.found,
		"answerable": len(actions) > 0,
		"reason":     look.reason,
		"source":     source,
		"rule":       rule,
		"kind":       kind,
		"message":    message,
		"prompt_id":  look.id,
		"lines":      lines,
		"options":    options,
		"actions":    actions,
		// The lines are another program's screen: data, not instructions.
		"untrusted": true,
	}
}

// mayRespond reports whether the caller may answer a prompt as the person:
// a verified attach nonce, or the respond_from_shell grant for a caller the
// kernel names outside every pane.
func (d *Daemon) mayRespond(cs *connState, nonce string) bool {
	if d.verifyAnyHumanNonce(nonce, cs) {
		return true
	}
	return d.respondFromShell && cs != nil && cs.peerPID > 0 && d.mayActAsHuman(cs)
}

// promptChangedError is the refusal when the prompt is not the one to answer.
func promptChangedError(w WindowState, why string) *verbError {
	return hintedVerbError(ErrVerbPromptChanged, "nothing was pressed: "+why, &VerbHint{
		Verb:    "peek-prompt",
		Command: "tuios peek-prompt -w " + shortWindowID(w.ID),
		Detail:  "Read the prompt again with peek-prompt, and answer what it shows now, passing the prompt_id it gives.",
	})
}

// verbRespond answers the prompt on a pane. See the file comment for the
// order of checks and who may call it.
func (d *Daemon) verbRespond(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		Action     string `json:"action"`
		Value      string `json:"value"`
		PromptID   string `json:"prompt_id"`
		HumanNonce string `json:"human_nonce"`
		Timeout    int    `json:"timeout"`
		// RiskAck acknowledges a risky prompt: the rules the pane's Inbox
		// item says its call matched. See respondRiskRefusal.
		RiskAck []string `json:"risk_ack"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !isAnswerAction(p.Action) {
		return nil, hintedVerbError(ErrVerbInvalidParams, "action: "+echoName(p.Action)+" is not an answer", &VerbHint{
			Param:    "action",
			Accepted: harness.AnswerActions,
		})
	}
	if len(p.Value) > maxRespondText {
		return nil, invalidParam("value", "value is longer than "+strconv.Itoa(maxRespondText)+" bytes")
	}
	sess, w, verr := d.resolvePromptWindow(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	// A pane the person gave the respond grant may answer for them in the
	// sessions it may write to. See pane_grants.go.
	byPane := ""
	if !d.mayRespond(cs, p.HumanNonce) {
		if !d.paneMayRespond(cs, sess.Name) {
			return nil, hintedVerbError(ErrVerbNotHuman, "respond is for the person at an attached client", &VerbHint{
				Param:  "human_nonce",
				Detail: "Answering an agent's prompt is acting as the person, so it takes the nonce of a client attached right now, from a process outside every pane: the Inbox's peek sends its own. A shell outside tuios may respond when the daemon runs with [daemon] respond_from_shell = true, and a pane may when the person gave it the respond grant with tuios set-pane-grants. An agent that wants a prompt answered should ask the person with send-agent-message -w human.",
			})
		}
		if pa := d.paneAuthority(cs); pa != nil {
			byPane = pa.window
		}
	}
	wait := respondDefaultWait
	if p.Timeout > 0 {
		wait = min(time.Duration(p.Timeout)*time.Millisecond, respondMaxWait)
	}

	slot := d.responds.slot(sess.ID + "\x00" + w.ID)
	defer d.responds.release(slot)
	slot.mu.Lock()
	defer slot.mu.Unlock()

	look, verr := d.lookAtPrompt(sess, w.ID)
	if verr != nil {
		return nil, verr
	}
	switch {
	case !look.found:
		return nil, promptChangedError(look.window, look.reason)
	case p.PromptID != "" && p.PromptID != look.id:
		return nil, promptChangedError(look.window, "the prompt on the pane is not the one prompt_id names")
	case slot.answered != "" && slot.answered == look.id:
		return nil, promptChangedError(look.window, "this prompt was already answered, by this client or another")
	}
	reply, err := look.prompt.Resolve(p.Action, p.Value)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "action: "+err.Error(), &VerbHint{
			Param:     "action",
			Available: look.prompt.Actions(),
			Detail:    "The actions this prompt offers now are listed. Anything else is answered in the pane.",
		})
	}
	if verr := d.respondRiskRefusal(sess, w.ID, look.prompt, p.Action, reply, p.RiskAck, byPane != ""); verr != nil {
		return nil, verr
	}
	pty := sess.GetPTY(look.window.PTYID)
	if pty == nil {
		return nil, hintedVerbError(ErrVerbPTYNotFound, "the pane's shell has exited", nil)
	}
	if reply.Text != "" {
		if _, werr := submitPrompt(d.ctx, pty, reply.Text, d.inputProfileFor(sess, w.ID)); werr != nil {
			return nil, newVerbError(ErrVerbInternal, werr.Error())
		}
	} else if _, werr := pty.Write(reply.Keys); werr != nil {
		return nil, newVerbError(ErrVerbInternal, werr.Error())
	}
	slot.answered = look.id
	if byPane != "" {
		LogBasic("respond: %s %s on %s/%s (prompt %s), by pane %s under its respond grant", p.Action, reply.Sent, sess.Name, shortWindowID(w.ID), look.id, shortWindowID(byPane))
	} else {
		LogBasic("respond: %s %s on %s/%s (prompt %s)", p.Action, reply.Sent, sess.Name, shortWindowID(w.ID), look.id)
	}

	settledBy, now := d.waitPromptAnswered(sess, w.ID, look.id, wait)
	out := map[string]any{
		"type":       "prompt_response",
		"session":    sess.Name,
		"window":     w.ID,
		"action":     p.Action,
		"sent":       reply.Sent,
		"prompt_id":  look.id,
		"settled_by": settledBy,
		"state":      now.AgentState.Name(),
		"message":    printableLine(now.AgentMessage),
	}
	// Absent when the person answered, so their result keeps its shape.
	if byPane != "" {
		out["by_pane"] = byPane
	}
	return out, nil
}

// Ways respond's wait ends.
const (
	respondSettledState   = "state"
	respondSettledPrompt  = "prompt"
	respondSettledTimeout = "timeout"
	respondSettledGone    = "gone"
)

// waitPromptAnswered waits for the window to leave needs_input, or for the
// prompt on it to be another than the one answered, and says which, with the
// window as it was then.
//
// The prompt usually leaves the screen before the state does, since the
// screen tier publishes on its own clock. So once the prompt is gone, the
// pane gets the look it would get on settling, run now, and a short grace to
// say where it went, before the wait ends on the prompt alone.
func (d *Daemon) waitPromptAnswered(sess *Session, windowID, answered string, wait time.Duration) (string, WindowState) {
	deadline := time.Now().Add(wait)
	var promptGoneAt time.Time
	for {
		look, verr := d.lookAtPrompt(sess, windowID)
		switch {
		case verr != nil:
			return respondSettledGone, WindowState{ID: windowID}
		case look.window.AgentState != AgentStateNeedsInput:
			return respondSettledState, look.window
		case look.id != answered && promptGoneAt.IsZero():
			promptGoneAt = time.Now()
			if pty := sess.GetPTY(look.window.PTYID); pty != nil {
				pty.runScreenLook()
			}
			continue
		case look.id != answered && time.Since(promptGoneAt) >= respondStateGrace:
			return respondSettledPrompt, look.window
		case !time.Now().Before(deadline):
			if look.id != answered {
				return respondSettledPrompt, look.window
			}
			return respondSettledTimeout, look.window
		}
		select {
		case <-d.ctx.Done():
			return respondSettledTimeout, look.window
		case <-time.After(respondPoll):
		}
	}
}

// isAnswerAction reports whether a is one of the answer actions.
func isAnswerAction(a string) bool {
	return slices.Contains(harness.AnswerActions, a)
}
