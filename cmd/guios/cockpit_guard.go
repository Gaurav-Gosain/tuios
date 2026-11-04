package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The typing guard. The agents view's lists act on single keys: a allows,
// n starts an agent, d settles. A person who types a sentence while a list
// holds the focus, because a reply box did not take it, must not have the
// sentence run as commands: "please also" holds two a, the double press
// that allows a risky command. So a list's letter does not run at once:
// it waits guardWindow, and runs only if no other key of prose came in
// that time. A key that comes within guardWindow of the one before is
// prose, and nothing runs; the first such key of a burst says once where
// the keys went.
//
// Three groups of keys, so that a deliberate key still feels instant:
//   - Moves (j k h l) only move the cursor: they run at once.
//   - Views (b M m v f Space n i, and r where it writes) only show
//     something or put the keys somewhere: they run at once, and are
//     taken back (setUndo) when more prose follows within guardWindow.
//   - Acts (a A r R d D u z x p c, and t H L n on the board) change
//     something for good or start something: they wait guardWindow.

// guardWindow is how long a list's letter waits to tell a command from
// typing: keys typed as prose come closer together.
const guardWindow = 300 * time.Millisecond

// ckKey is a key a list took.
type ckKey struct {
	key   ui.Key
	shift bool
}

// heldKey is a letter waiting out guardWindow.
type heldKey struct {
	k  ckKey
	at time.Time
}

// typingGuard is a list's guard state.
type typingGuard struct {
	last   time.Time // the last key of prose
	held   *heldKey
	hinted bool // the hint for this burst was shown
	// undo takes back the view key that ran at once at undoAt, if more
	// prose follows within guardWindow.
	undo   func()
	undoAt time.Time
}

// setUndo notes how to take back a view key that just ran.
func (g *typingGuard) setUndo(fn func()) {
	g.undo, g.undoAt = fn, time.Now()
}

// takeBack takes back the view key that ran, when typing follows it.
func (g *typingGuard) takeBack(now time.Time) {
	if g.undo != nil && now.Sub(g.undoAt) < guardWindow {
		g.undo()
	}
	g.undo = nil
}

// proseKeys are the keys typing makes, besides Shift.
var proseKeys = []ui.Key{
	ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM,
	ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ,
	ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9,
	ui.KeySpace, ui.KeyComma, ui.KeyPeriod, ui.KeyMinus, ui.KeyQuote, ui.KeySlash, ui.KeySemicolon, ui.KeyEqual, ui.KeyBackquote,
}

// takeProse reads the key of prose the element got in this build, if any.
// Shift first: a key with Shift also reads as the key alone.
func takeProse(el ui.Element) (ckKey, bool) {
	for _, k := range proseKeys {
		if el.Shortcut(ui.Shift, k) {
			return ckKey{key: k, shift: true}, true
		}
		if el.Shortcut(0, k) {
			return ckKey{key: k}, true
		}
	}
	return ckKey{}, false
}

// typing reports whether a key now would come within guardWindow of the
// last key of prose.
func (g *typingGuard) typing(now time.Time) bool {
	return !g.last.IsZero() && now.Sub(g.last) < guardWindow
}

// guard runs a list's keys of prose through the guard. It returns a key to
// run now: one immediate says runs at once, or one that waited out
// guardWindow with nothing after it.
func (a *App) guard(c *ui.Context, el ui.Element, g *typingGuard, immediate func(ckKey) bool) (ckKey, bool) {
	now := time.Now()
	if k, ok := takeProse(el); ok {
		typing := g.typing(now)
		g.last = now
		if typing {
			g.held = nil
			g.takeBack(now)
			if !g.hinted {
				g.hinted = true
				a.toast(locales.CockpitKeysToList())
			}
			return ckKey{}, false
		}
		g.hinted, g.undo = false, nil
		if immediate(k) {
			return k, true
		}
		g.held = &heldKey{k: k, at: now}
		c.After(guardWindow)
		return ckKey{}, false
	}
	if h := g.held; h != nil {
		switch wait := guardWindow - now.Sub(h.at); {
		case !g.last.Equal(h.at) || wait < -time.Second:
			// Another key came after it, or it waited while the list
			// had no focus: it is not a command now.
			g.held = nil
		case wait > 0:
			c.After(wait)
		default:
			g.held = nil
			return h.k, true
		}
	}
	return ckKey{}, false
}

// ckInstant reports whether an agents list key runs at once: a move, or a
// view key that ckLetter can take back. The others wait.
func (a *App) ckInstant(k ckKey) bool {
	if k.shift {
		return k.key == ui.KeyM
	}
	switch k.key {
	case ui.KeyJ, ui.KeyK, ui.KeyH, ui.KeyL, ui.KeyB, ui.KeyM, ui.KeyV, ui.KeyF, ui.KeySpace, ui.KeyN, ui.KeyI:
		return true
	case ui.KeyR:
		// r writes in the reply box, unless it rejects an approval.
		return !a.ckCanReject()
	}
	return false
}

// keysOwned reports whether a sheet, a dialog or a field of the agents view
// is open: it owns the keyboard for as long as it is open, and the lists'
// single keys are off, not only until its field takes the focus. A sheet
// focuses its field a frame after it opens; a key typed in between must
// not run as a list's command (QA-064).
func (a *App) keysOwned() bool {
	ck := &a.ck
	switch {
	case ck.spawn != nil && ck.spawn.open,
		ck.cmp != nil,
		a.pal.open,
		a.help.open,
		a.prompt != nil && a.prompt.open,
		a.confirm != nil && a.confirm.open,
		a.wtAsk != nil && a.wtAsk.open,
		a.picker.open,
		a.renaming != "" || a.renamingProject != "",
		ck.reasonFor != "",
		ck.tabs.board != nil && ck.tabs.board.adding >= 0,
		ck.view == viewMail && ck.tabs.mail != nil && ck.tabs.mail.replyTo != 0:
		return true
	}
	return false
}

// ownedKeys takes a list's keys of prose while keysOwned: they run
// nothing, and count as typing for the guard.
func (a *App) ownedKeys(el ui.Element) bool {
	if !a.keysOwned() {
		return false
	}
	if _, ok := takeProse(el); ok {
		a.ck.guard.last = time.Now()
	}
	if el.Shortcut(0, ui.KeyEscape) && a.ck.spawn != nil && a.ck.spawn.open {
		// Escape before the sheet's field had the keys still closes it.
		a.spawnClose()
	}
	return true
}
