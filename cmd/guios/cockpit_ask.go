package main

import (
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The confirm step. A letter in the agents list that would change
// something (allow, allow always, reject, settle, snooze, dismiss, pass
// mail on) asks first: the card and the key line say what will happen, Enter does
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
	askSnooze
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
	// opener is the text of the letters that opened the step, "" for the
	// menu, and openerUndo takes back what the letters before it did.
	opener     string
	openerUndo func()
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
	case askReason:
		q.target, q.mono, q.decision = command, true, "reason"
		q.word = locales.CockpitReject()
		q.ask = locales.CockpitAskRejectWhy(ckTarget)
	case askSettle:
		ag, ok := a.ckSelectedAgent()
		if !ok {
			a.toast(locales.CockpitSettleOnlyAgent())
			return
		}
		q.target = ag.Name
		q.ask, q.word = locales.CockpitAskSettle(ckTarget), locales.CockpitSettle()
		if ag.Settled {
			q.ask, q.word = locales.CockpitAskUnsettle(ckTarget), locales.CockpitUnsettle()
		}
	case askDismiss:
		if r.item == nil {
			return
		}
		q.ask, q.word = locales.CockpitAskDismiss(ckTarget), locales.CockpitDismiss()
	case askPass:
		if r.item == nil || r.item.HeldID == 0 {
			return
		}
		q.ask, q.word = locales.CockpitAskPass(ckTarget), locales.CockpitPassMail()
	case askSnooze:
		if r.item != nil && ckHeld(*r.item) {
			a.toast(locales.CockpitNoSnoozeHeld())
			return
		}
		if r.item == nil {
			a.toast(locales.CockpitSnoozeOnlyItem())
			return
		}
		q.ask, q.word = locales.CockpitAskSnooze(ckTarget), locales.CockpitSnooze()
		if r.item.SnoozedUntil != 0 {
			q.ask, q.word = locales.CockpitAskWake(ckTarget), locales.CockpitWake()
		}
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
		if k, ok := takeProse(col); ok {
			// Typed text: never an answer. It goes to the reply box with
			// the letter that opened the step, which was its first letter:
			// "ask it" lands whole, not as "sk it" (council round 4, feel
			// S1).
			q := ck.question
			ck.question = nil
			text := ""
			if q != nil {
				if q.openerUndo != nil {
					q.openerUndo()
				}
				text = q.opener
			}
			if ch := keyRune(k); ch != 0 {
				text += string(ch)
			}
			if text == "" || !a.ckProse(text) {
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
	case askSnooze:
		a.ckSnooze()
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
	keyHint(c, pal, "esc", locales.CommonCancel())
}
