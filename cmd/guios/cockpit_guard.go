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
// the keys went. j and k, which only move the cursor, run at once.

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
			if !g.hinted {
				g.hinted = true
				a.toast(locales.CockpitKeysToList())
			}
			return ckKey{}, false
		}
		g.hinted = false
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

// ckMoveKey reports whether a key only moves the cursor.
func ckMoveKey(k ckKey) bool {
	return !k.shift && (k.key == ui.KeyJ || k.key == ui.KeyK)
}
