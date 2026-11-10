package main

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The confirm step. The chord that allows a risky call asks first, and so
// do always allow and the menu's allow: the key line says what will run,
// Enter does it and Escape drops it. Typed text drops it too and goes to the
// reply box (cockpit_guard.go).

// askWhat is what a confirm step does on Enter.
type askWhat int

const (
	askOnce askWhat = iota
	askAlways
	askDeny
)

// ckQuestion is the open confirm step.
type ckQuestion struct {
	what askWhat
	row  string // the row the cursor was on
	item string // ckShownKey of the prompt it answers, if any
	// ask is the question as the locale writes it, with \x00 where the
	// target goes; target is the command (mono) or the agent's name.
	ask, target string
	mono        bool
	// word is the key cap's label for Enter: "Allow once", "Snooze".
	word string
	// decision is the card's button the step asks about, if any.
	decision string
}

// ckTarget is the \x00 a locale string gets for its target, so that the
// key line draws the target in its own font where the language puts it.
const ckTarget = "\x00"

// ckAskFor opens the confirm step for what on the row the cursor is on.
func (a *App) ckAskFor(what askWhat) {
	ck := &a.ck
	r, ok := a.ckSelected()
	if !ok {
		return
	}
	q := &ckQuestion{what: what, row: r.key, ask: "", target: ""}
	command := ""
	if r.item != nil {
		q.item = ckShownKey(*r.item)
		command = r.item.Summary
		if _, t, ok := ckAsk(command); ok && t != "" {
			command = t
		}
		command = ckClipMiddle(flat(command), 48)
	}
	name := ""
	if r.agent >= 0 {
		name = ck.agents[r.agent].Name
	} else if r.item != nil {
		name = r.item.Name
	}
	q.target = name
	switch what {
	case askOnce, askAlways, askDeny:
		if r.item == nil || !ckAnswerable(*r.item) {
			return
		}
		if what == askDeny && !a.ckCanReject() {
			return
		}
		q.target, q.mono = command, true
		q.decision = map[askWhat]string{askOnce: "once", askAlways: "always", askDeny: "deny"}[what]
		q.word = ckDecisionWords(q.decision)
		q.ask = map[askWhat]func(string) string{
			askOnce: locales.CockpitAskAllow, askAlways: locales.CockpitAskAllowAlways, askDeny: locales.CockpitAskReject,
		}[what](ckTarget)
	}
	ck.question = q
	ck.armed = ckArmed{}
}

// ckClipMiddle cuts s to n runes in its middle, as a long command keeps
// its start and its end.
func ckClipMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := (n - 1) / 2
	return string(r[:head]) + "…" + string(r[len(r)-(n-1-head):])
}

// ckQuestionKeys are the keys while a confirm step is open: Enter does it,
// Escape drops it, a letter drops it and goes on as typed text, and any
// other key drops it and does what it does. It reports whether it took the
// key.
func (a *App) ckQuestionKeys(c *ui.Context, col ui.Element) bool {
	ck := &a.ck
	switch {
	case col.Shortcut(0, ui.KeyEnter):
		a.ckQuestionRun()
	case col.Shortcut(0, ui.KeyEscape):
		ck.question = nil
	default:
		if text, ok := typedText(col); ok {
			// Typed text: never an answer. The step goes, and the text
			// goes to the reply box.
			ck.question = nil
			if !a.ckProse(text) {
				a.toast(locales.CockpitKeysNeedAgent())
			}
		} else {
			// Another key: the step goes, and the key does what it does.
			if col.Shortcut(0, ui.KeyDown) || col.Shortcut(0, ui.KeyUp) || col.Shortcut(ui.Cmd, ui.KeyEnter) {
				ck.question = nil
			}
			return false
		}
	}
	c.Invalidate()
	return true
}

// ckQuestionRun does what the open confirm step says, if the row and its
// prompt are still the ones it asked about.
func (a *App) ckQuestionRun() {
	ck := &a.ck
	q := ck.question
	ck.question = nil
	if q == nil {
		return
	}
	r, ok := a.ckSelected()
	if !ok || r.key != q.row || q.item != "" && (r.item == nil || ckShownKey(*r.item) != q.item) {
		a.toast(locales.CockpitJustChanged())
		return
	}
	answer := func(decision string) {
		ck.confirmed = true
		a.ckAnswer(decision)
		ck.confirmed = false
	}
	switch q.what {
	case askOnce:
		answer("once")
	case askAlways:
		answer("always")
	case askDeny:
		answer("deny")
	}
}

// ckAllowChord is Ctrl+Enter (Cmd+Enter on the Mac) in the list: it allows
// a call that is not risky at once, as a chord no typed text makes. A risky
// call still asks.
func (a *App) ckAllowChord() {
	if ag, d, ok := a.ckDraftWaiting(); ok {
		// The reply box holds text: Ctrl+Enter sends it now, as it does in
		// the box and in most chat apps, and never allows.
		a.sendReply(ag, d, true)
		return
	}
	r, ok := a.ckSelected()
	if !ok || r.item == nil || !ckAnswerable(*r.item) || ckIsQuestion(*r.item) {
		// A press with nothing to allow says so, in the key line: a
		// question is answered by its buttons or a reply.
		a.ck.nothingAt, a.ck.nothingOff = time.Now(), false
		a.ck.nothingQuestion = ok && r.item != nil && ckIsQuestion(*r.item)
		return
	}
	if !ckHeld(*r.item) && a.ckApprovalsOff(*r.item, a.ck.rowAgent(r)) {
		// The card has no Allow: it says approvals are off, and the key
		// line says the same, never a reason of its own (Mac round 4,
		// workflow S3).
		a.ck.nothingAt, a.ck.nothingQuestion, a.ck.nothingOff = time.Now(), false, true
		return
	}
	rules := r.item.Risk
	if !ckHeld(*r.item) && a.ck.peek != nil {
		rules = a.ck.peek.risk
	}
	if len(rules) > 0 {
		a.ckAskFor(askOnce)
		return
	}
	a.ck.confirmed = true
	a.ckAnswer("once")
	a.ck.confirmed = false
}

// ckDraftWaiting is the selected agent's reply draft when it holds text or
// images: while it does, Ctrl+Enter sends it and allows nothing. A state,
// not a time: the box is empty or it is not.
func (a *App) ckDraftWaiting() (client.Agent, *ckDraft, bool) {
	ag, ok := a.ckSelectedAgent()
	if !ok || ag.Window == "" || ag.Host != "" || a.ck.reply.asMail[ag.Key()] {
		return client.Agent{}, nil, false
	}
	d := a.draftFor(ag)
	if strings.TrimSpace(d.text) == "" && len(d.images) == 0 {
		return client.Agent{}, nil, false
	}
	return ag, d, true
}

// ckQuestionLine draws the open confirm step in the key line: the
// question, its target in the code font when it is a command, and the key
// caps for Enter and Escape (council round 4, S2).
func (a *App) ckQuestionLine(c *ui.Context, pal *palette) {
	q := a.ck.question
	if q == nil {
		return
	}
	before, after, _ := strings.Cut(q.ask, ckTarget)
	spans := []ui.Span{{Text: before, Color: pal.text, Weight: wSemibold}}
	t := ui.Span{Text: q.target, Color: pal.text, Weight: wSemibold}
	if q.mono {
		t.Font = a.termFamily()
		t.Weight = 0
	}
	spans = append(spans, t, ui.Span{Text: after, Color: pal.text, Weight: wSemibold})
	ui.RichText(c, spans...).FontSize(fsSmall).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
	keyHint(c, pal, "↵", q.word)
	keyHint(c, pal, "Esc", locales.CommonCancel())
}

// ckIsQuestion reports whether an item is a question, which the card
// answers with its own answers or a reply, never an allow.
func ckIsQuestion(it session.AttentionItem) bool {
	return it.Kind == session.AttentionQuestion || it.Kind == session.AttentionAsk
}
