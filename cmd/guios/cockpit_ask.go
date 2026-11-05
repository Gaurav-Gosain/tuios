package main

import (
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The confirm step. A letter in the agents list that would change
// something (allow, allow always, reject, settle, dismiss, pass mail on)
// asks first: the card and the key line say what will happen, Enter does
// it and Escape drops it. A letter typed meanwhile drops it too and goes to
// the reply box, so a sentence typed into the list never answers anything
// (council round 3, M1).

// askWhat is what a confirm step does on Enter.
type askWhat int

const (
	askOnce askWhat = iota
	askAlways
	askDeny
	askReason
	askSettle
	askDismiss
	askPass
)

// ckQuestion is the open confirm step.
type ckQuestion struct {
	what askWhat
	row  string // the row the cursor was on
	item string // ckShownKey of the prompt it answers, if any
	text string // what the step says
}

// ckAskFor opens the confirm step for what on the row the cursor is on.
func (a *App) ckAskFor(what askWhat) {
	ck := &a.ck
	r, ok := a.ckSelected()
	if !ok {
		return
	}
	q := &ckQuestion{what: what, row: r.key}
	target := ""
	if r.item != nil {
		q.item = ckShownKey(*r.item)
		target = r.item.Summary
		if _, t, ok := ckAsk(target); ok && t != "" {
			target = t
		}
		target = ckClip(flat(target), 60)
	}
	name := ""
	if r.agent >= 0 {
		name = ck.agents[r.agent].Name
	} else if r.item != nil {
		name = r.item.Name
	}
	switch what {
	case askOnce, askAlways, askDeny:
		if r.item == nil || !ckAnswerable(*r.item) {
			return
		}
		if what == askDeny && !a.ckCanReject() {
			return
		}
		q.text = map[askWhat]func(string) string{
			askOnce: locales.CockpitAskAllow, askAlways: locales.CockpitAskAllowAlways, askDeny: locales.CockpitAskReject,
		}[what](target)
	case askReason:
		q.text = locales.CockpitAskRejectWhy(target)
	case askSettle:
		ag, ok := a.ckSelectedAgent()
		if !ok {
			a.toast(locales.CockpitSettleOnlyAgent())
			return
		}
		q.text = locales.CockpitAskSettle(ag.Name)
		if ag.Settled {
			q.text = locales.CockpitAskUnsettle(ag.Name)
		}
	case askDismiss:
		if r.item == nil {
			return
		}
		q.text = locales.CockpitAskDismiss(name)
	case askPass:
		if r.item == nil || r.item.HeldID == 0 {
			return
		}
		q.text = locales.CockpitAskPass(name)
	}
	ck.question = q
	ck.armed = ckArmed{}
}

// ckClip cuts s to n runes, with an ellipsis.
func ckClip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
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
		if k, ok := takeProse(col); ok {
			// Typed text: never an answer. It goes to the reply box.
			ck.question = nil
			if ch := keyRune(k); ch == 0 || !a.ckProse(string(ch)) {
				a.toast(locales.CockpitKeysToList())
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
	case askReason:
		if it := r.item; it != nil && ck.reasonFor == it.ID {
			a.ckSend(*it, ck.rowAgent(r), "deny", ck.reason)
			ck.reasonFor, ck.reason = "", ""
			ck.fleetFocus.Focus()
		}
	case askSettle:
		a.ckSettle()
	case askDismiss:
		a.ckDismiss()
	case askPass:
		if r.item != nil {
			a.ckPassHeld(*r.item)
		}
	}
}

// ckAllowChord is Ctrl+Enter (Cmd+Enter on the Mac) in the list: it allows
// a call that is not risky at once, as a chord no typed text makes. A risky
// call still asks.
func (a *App) ckAllowChord() {
	r, ok := a.ckSelected()
	if !ok || r.item == nil || !ckAnswerable(*r.item) {
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

// ckQuestionLine draws the open confirm step: what Enter does, and Escape.
func (a *App) ckQuestionLine(c *ui.Context, pal *palette) {
	q := a.ck.question
	if q == nil {
		return
	}
	ui.Text(c, q.text).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.attnText).MaxLines(3)
}
