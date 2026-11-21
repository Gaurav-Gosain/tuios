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
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

const promptContextLines = 20

const (
	respondDefaultWait = 5 * time.Second
	respondMaxWait     = 30 * time.Second
	respondPoll        = 50 * time.Millisecond
	respondStateGrace  = screenSettleDelay + 200*time.Millisecond
)

const maxPromptLineRunes = 400

const maxRespondText = 4096

type respondSlots struct {
	mu    sync.Mutex
	slots map[string]*respondSlot
}

type respondSlot struct {
	mu       sync.Mutex
	answered string
	users    int
}

const maxRespondSlots = 512

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

func (r *respondSlots) release(s *respondSlot) {
	r.mu.Lock()
	s.users--
	r.mu.Unlock()
}

type promptLook struct {
	window WindowState
	prompt harness.Prompt
	found  bool
	id     string
	reason string
}

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
	if agentBlockedBy(w) == harness.PromptKindAuth {
		look.reason = "the pane waits for a login, which is answered in the pane"
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
	if look.prompt.Answerable() && crushScreenWins(hid) && !d.paneRunsHarness(sess, w.PTYID, hid) {
		look.prompt = look.prompt.WithoutAnswers()
		look.id = promptID(w, hid, look.prompt)
		look.reason = "tuios does not see " + hid + " running in the pane, so the prompt is answered in the pane"
		return look, nil
	}
	look.id = promptID(w, hid, look.prompt)
	if !look.prompt.Answerable() {
		look.reason = "the rule that reads this prompt declares no answers, so it is answered in the pane"
	}
	return look, nil
}

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

func (d *Daemon) resolvePromptWindow(cs *connState, sessionName, window string) (*Session, WindowState, *verbError) {
	if window == "" {
		return nil, WindowState{}, invalidParam("window", "window is required: name the pane whose prompt to read")
	}
	sess, verr := d.resolveVerbSession(cs, sessionName)
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

func (d *Daemon) verbPeekPrompt(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, w, verr := d.resolvePromptWindow(cs, p.Session, p.Window)
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
		"session":    sess.Name(),
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
		"untrusted":  true,
	}
}

func (d *Daemon) mayRespond(cs *connState, nonce, sessionID string) bool {
	if _, ok := d.humanNonceFor(nonce, sessionID, cs); ok {
		return true
	}
	return d.respondFromShell && cs != nil && cs.peerPID > 0 && d.mayActAsHuman(cs)
}

func promptChangedError(w WindowState, why string) *verbError {
	return hintedVerbError(ErrVerbPromptChanged, "nothing was pressed: "+why, &VerbHint{
		Verb:    "peek-prompt",
		Command: "tuios peek-prompt -w " + shortWindowID(w.ID),
		Detail:  "Read the prompt again with peek-prompt, and answer what it shows now, passing the prompt_id it gives.",
	})
}

func (d *Daemon) verbRespond(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string   `json:"session"`
		Window     string   `json:"window"`
		Action     string   `json:"action"`
		Value      string   `json:"value"`
		PromptID   string   `json:"prompt_id"`
		HumanNonce string   `json:"human_nonce"`
		Timeout    int      `json:"timeout"`
		RiskAck    []string `json:"risk_ack"`
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
	sess, w, verr := d.resolvePromptWindow(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	byPane := ""
	asPerson := d.mayRespond(cs, p.HumanNonce, sess.ID)
	asParent := false
	if !asPerson {
		if pa := d.paneAuthority(cs); pa != nil && d.attention.parentFirst.Load() && parentMayRespond(pa.window, w) {
			asParent, byPane = true, pa.window
		}
	}
	if !asPerson && !asParent {
		if !d.paneMayRespond(cs, sess.Name()) {
			return nil, hintedVerbError(ErrVerbNotHuman, "respond is for the person at an attached client", &VerbHint{
				Param:  "human_nonce",
				Detail: "Answering an agent's prompt is acting as the person, so it takes the nonce of a client attached right now, from a process outside every pane: the Inbox's peek sends its own. A shell outside tuios may respond when the daemon runs with [daemon] respond_from_shell = true, and a pane may when the person gave it the respond grant with tuios set-pane-grants. An agent that wants a prompt answered should ask the person with send-agent-message -w human.",
			})
		}
		if pa := d.paneAuthority(cs); pa != nil {
			byPane = pa.window
		}
	}
	if !asPerson {
		if why := d.childApprovalRefusal(byPane, w); why != "" {
			return nil, childApprovalForbidden(why)
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
	if !asPerson && look.window.AgentLink != nil && look.window.ID != byPane && look.prompt.Kind == harness.PromptKindApproval {
		return nil, childApprovalForbidden("window " + shortWindowID(look.window.ID) + " is a child agent, and its screen shows an approval. Only the person answers it")
	}
	if asParent && look.prompt.Kind != "" && look.prompt.Kind != harness.PromptKindQuestion {
		return nil, childApprovalForbidden("window " + shortWindowID(look.window.ID) + " is your child agent, but its screen shows a " + look.prompt.Kind + " prompt, which only the person answers")
	}
	reply, err := look.prompt.Resolve(p.Action, p.Value)
	if err != nil {
		return nil, hintedVerbError(ErrVerbInvalidParams, "action: "+err.Error(), &VerbHint{
			Param:     "action",
			Available: look.prompt.Actions(),
			Detail:    "The actions this prompt offers now are listed. Anything else is answered in the pane.",
		})
	}
	var seen []string
	if byPane != "" {
		seen = d.promptRisk(sess, look.window, look.prompt)
	}
	if verr := d.respondRiskRefusal(sess, w.ID, look.prompt, p.Action, reply, p.RiskAck, byPane != "", seen); verr != nil {
		return nil, verr
	}
	pty := sess.GetPTY(look.window.PTYID)
	if pty == nil {
		return nil, hintedVerbError(ErrVerbPTYNotFound, "the pane's shell has exited", nil)
	}
	hidden := 0
	if reply.Text != "" {
		reply.Text, hidden = invisible.StripHidden(reply.Text)
	}
	if reply.Text != "" {
		if _, werr := submitPrompt(d.ctx, pty, reply.Text, d.inputProfileFor(sess, w.ID)); werr != nil {
			return nil, promptWriteError(werr)
		}
	} else if _, werr := pty.Write(reply.Keys); werr != nil {
		return nil, promptWriteError(werr)
	}
	slot.answered = look.id
	if byPane != "" {
		LogBasic("respond: %s %s on %s/%s (prompt %s), by pane %s under its respond grant", p.Action, reply.Sent, sess.Name(), shortWindowID(w.ID), look.id, shortWindowID(byPane))
	} else {
		LogBasic("respond: %s %s on %s/%s (prompt %s)", p.Action, reply.Sent, sess.Name(), shortWindowID(w.ID), look.id)
	}

	settledBy, now := d.waitPromptAnswered(sess, w.ID, look.id, wait)
	out := map[string]any{
		"type":              "prompt_response",
		"session":           sess.Name(),
		"window":            w.ID,
		"action":            p.Action,
		"sent":              reply.Sent,
		"prompt_id":         look.id,
		"settled_by":        settledBy,
		"state":             now.AgentState.Name(),
		"message":           printableLine(now.AgentMessage),
		"invisible_removed": hidden,
	}
	if byPane != "" {
		out["by_pane"] = byPane
	}
	return out, nil
}

const (
	respondSettledState   = "state"
	respondSettledPrompt  = "prompt"
	respondSettledTimeout = "timeout"
	respondSettledGone    = "gone"
)

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

func isAnswerAction(a string) bool {
	return slices.Contains(harness.AnswerActions, a)
}

func childApprovalForbidden(why string) *verbError {
	return hintedVerbError(ErrVerbForbidden, "respond is refused: "+why, &VerbHint{
		Detail: "Nothing was pressed. The approval is in the person's Inbox. Ask the person with send-agent-message -w human.",
	})
}
