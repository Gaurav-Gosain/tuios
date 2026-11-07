package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// toasts shows the window's toasts (DESIGN.md section 6): raised cards in
// the text's color with an edge and the overlay shadow, at the bottom
// center, 48 DIPs up so that they clear the hint bars, and moves them: a
// toast rises and fades in, those above make room, and one that goes
// sinks and fades out. At most toastsShown show, the newest lowest. Call
// it once in each window's view.
func toasts(c *ui.Context) { toastsAt(c, nil) }

// toastsShown is how many toasts show at once. An older one closes when a
// new one would push it out, so it does not come back later.
const toastsShown = 2

// A toast closes toastLife after it showed, toastActionLife for one with a
// button, by the clock, checked on every frame. mygo stops a toast's time
// while its window is in the background or the pointer is on the toasts,
// and on a desktop where other apps take the front (or a pointer left over
// the Agents list, where the toasts show) that kept old toasts up for
// minutes, stacked, one saying "cannot read" beside a card that could
// (Mac round 3, W3-M1). The pointer on a toast still holds it, up to
// toastHoldMax.
const (
	toastLife       = 4 * time.Second
	toastActionLife = 8 * time.Second
	toastHoldMax    = 3 * toastLife
)

// toastShown is a toast's Data: when it showed.
type toastShown struct{ at time.Time }

// showToast shows a toast with the time it showed, for toastsAt.
func showToast(c *ui.Context, msg string) {
	c.AddToast(ui.Toast{ID: msg, Title: msg, Data: &toastShown{at: c.Now()}})
}

// toastDue reports whether a toast shown at at is over at now, and how long
// it has left when not.
func toastDue(at, now time.Time, action, hovered bool) (bool, time.Duration) {
	life := toastLife
	if action {
		life = toastActionLife
	}
	if hovered {
		life = max(life, toastHoldMax)
	}
	left := life - now.Sub(at)
	return left <= 0, left
}

// toastsAt is toasts, at the foot of the box in when it is set: the agents
// view puts them over the empty foot of its list, where they cover no
// control and no heading. A box too narrow for a toast is not used.
func toastsAt(c *ui.Context, in *ui.Rect) {
	pal := paletteFor(c)
	fill := pal.popover
	if pal.dark {
		fill = pal.popover.Mix(pal.text, 0.06)
	}
	w, h := c.Size()
	if in != nil && in.W < 220 {
		in = nil
	}
	maxW := min(dialogW, w-2*sp4)
	if in != nil {
		maxW = min(maxW, in.W-2*sp3)
	}
	move := ui.ElementTransition{
		Animation: &motionBase,
		Position:  true,
		Enter:     &ui.Motion{Y: 14, Opacity: 0, Scale: 0.98},
		Exit:      &ui.Motion{Y: 8, Opacity: 0, Scale: 0.98},
	}
	ui.ToastViewportBase(c, func(viewport ui.Element, list []ui.Toast) {
		if in != nil {
			viewport.Left(in.X).Right(w - in.X - in.W).Bottom(h - in.Y - in.H + sp3).AlignItems(ui.Center).Gap(sp2)
		} else {
			viewport.Bottom(48).AlignItems(ui.Center).Gap(sp2)
		}
		now := c.Now()
		var next time.Duration
		var over []string
		for i, t := range list {
			if i < len(list)-toastsShown {
				over = append(over, t.ID)
			}
		}
		for _, t := range list[max(0, len(list)-toastsShown):] {
			p := ui.ToastBase(c, t)
			if ts, ok := t.Data.(*toastShown); ok {
				due, left := toastDue(ts.at, now, t.Action != "", p.Root.Hovered())
				if due {
					over = append(over, t.ID)
				} else if next == 0 || left < next {
					next = left
				}
			}
			box := p.Root.Row().AlignItems(ui.Center).Gap(sp3).Padding(10, sp4).Radius(rCard).
				MaxWidth(maxW).Background(fill).TextColor(pal.text).Border(1, pal.kbdBorder).
				Shadow(0, 2, 4, 0, ui.RGBA(0, 0, 0, 0.12)).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.22))
			box.Transition(move)
			box.Children(func() {
				ui.Column(c).Shrink(1).MinWidth(0).Gap(2).Children(func() {
					title := ui.Text(c, t.Title).FontSize(fsBody).LineHeight(1.4).MaxLines(3)
					if t.Description != "" {
						title.FontWeight(wMedium)
						ui.Text(c, t.Description).FontSize(fsSmall).TextColor(pal.textMuted).LineHeight(1.4).MaxLines(3)
					}
				})
				if t.Action == "" {
					return
				}
				b := p.ActionButton().Height(hControlSm).Padding(0, 10).Radius(rControl).Shrink(0).TextColor(pal.accent).FontSize(fsBody).FontWeight(wMedium)
				if b.Hovered() {
					b.Background(pal.rowHover)
				}
				b.Children(func() { ui.Text(c, t.Action).SingleLine() })
			})
		}
		for _, id := range over {
			c.CloseToast(id)
		}
		if next > 0 {
			c.After(next)
		}
	})
}

// ckPromptToast shows a toast about the approval card on screen: the
// prompt cannot be read, or approvals are now on. It closes when the card
// changes (dropStalePromptToasts): the prompt was answered, became a held
// one, or ended, and the toast would then say what the screen beside it
// does not.
func (a *App) ckPromptToast(msg string) {
	a.toast(msg)
	if a.ck.promptToasts == nil {
		a.ck.promptToasts = map[string]string{}
	}
	a.ck.promptToasts[msg] = a.ck.shown.key
}

// dropStalePromptToasts closes the toasts of a card that this frame did not
// draw. Call it after the cards and before the toasts.
func (a *App) dropStalePromptToasts(c *ui.Context) {
	for msg, key := range a.ck.promptToasts {
		if key != a.ck.cardKey {
			c.CloseToast(msg)
			delete(a.ck.promptToasts, msg)
		}
	}
}
