package main

import (
	"github.com/Gaurav-Gosain/mygo/ui"
)

// The keys of the agents view's lists: the agents list, the board, the
// Mail view and Compare. One rule holds on all of them: a printable key
// never acts. It goes to the reply box of the selected agent, or to the
// surface's own text field, and the rest of the sentence follows it there.
// The lists move with the arrows, Home, End, Page Up, Page Down and Tab.
// Actions are chords and buttons (ckChords), never letters. There is no
// mode to track: a sentence typed while a list has the keys lands whole
// where it was meant to, however it is typed (council round 4, M1).

// ckKey is a printable key a list took.
type ckKey struct {
	key   ui.Key
	shift bool
}

// proseKeys are the keys typing makes, besides Shift.
var proseKeys = []ui.Key{
	ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM,
	ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ,
	ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9,
	ui.KeySpace, ui.KeyComma, ui.KeyPeriod, ui.KeyMinus, ui.KeyQuote, ui.KeySlash, ui.KeySemicolon, ui.KeyEqual, ui.KeyBackquote,
	ui.KeyBracketLeft, ui.KeyBracketRight, ui.KeyBackslash,
}

// keyRunes are the characters of the keys that are no letter, without and
// with Shift, as a US keyboard types them.
var keyRunes = map[ui.Key][2]rune{
	ui.Key0: {'0', ')'}, ui.Key1: {'1', '!'}, ui.Key2: {'2', '@'}, ui.Key3: {'3', '#'}, ui.Key4: {'4', '$'},
	ui.Key5: {'5', '%'}, ui.Key6: {'6', '^'}, ui.Key7: {'7', '&'}, ui.Key8: {'8', '*'}, ui.Key9: {'9', '('},
	ui.KeySpace: {' ', ' '}, ui.KeyComma: {',', '<'}, ui.KeyPeriod: {'.', '>'}, ui.KeyMinus: {'-', '_'},
	ui.KeyQuote: {'\'', '"'}, ui.KeySlash: {'/', '?'}, ui.KeySemicolon: {';', ':'}, ui.KeyEqual: {'=', '+'},
	ui.KeyBackquote: {'`', '~'}, ui.KeyBracketLeft: {'[', '{'}, ui.KeyBracketRight: {']', '}'},
	ui.KeyBackslash: {'\\', '|'},
}

// keyRune is the character a key of prose types.
func keyRune(k ckKey) rune {
	if k.key >= ui.KeyA && k.key <= ui.KeyZ {
		r := 'a' + rune(k.key-ui.KeyA)
		if k.shift {
			r -= 'a' - 'A'
		}
		return r
	}
	if rs, ok := keyRunes[k.key]; ok {
		if k.shift {
			return rs[1]
		}
		return rs[0]
	}
	return 0
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

// typedText is the printable key the element got in this build, as text.
func typedText(el ui.Element) (string, bool) {
	k, ok := takeProse(el)
	if !ok {
		return "", false
	}
	if r := keyRune(k); r != 0 {
		return string(r), true
	}
	return "", false
}

// listMove reads the keys that move a list's cursor: the arrows (dy, dx),
// Home and End (to the first or last), Page Up and Page Down (by a page),
// and Tab and Shift+Tab (by one). It reports the move, and ok false when
// none came.
func listMove(el ui.Element) (d int, to int, ok bool) {
	key := func(m ui.Modifiers, k ui.Key) bool { return el.Shortcut(m, k) }
	switch {
	case key(0, ui.KeyDown), key(0, ui.KeyTab):
		return 1, 0, true
	case key(0, ui.KeyUp), key(ui.Shift, ui.KeyTab):
		return -1, 0, true
	case key(0, ui.KeyPageDown):
		return 8, 0, true
	case key(0, ui.KeyPageUp):
		return -8, 0, true
	case key(0, ui.KeyHome):
		return 0, -1, true
	case key(0, ui.KeyEnd):
		return 0, 1, true
	}
	return 0, 0, false
}

// ckProse puts typed text in the reply box of the agent the cursor is on,
// and the keys with it. It reports false when the row has no reply box.
func (a *App) ckProse(text string) bool {
	ck := &a.ck
	ag, ok := a.ckSelectedAgent()
	if !ok || ag.Window == "" || ag.Host != "" {
		return false
	}
	if ck.stacked {
		ck.stackFocus = true
	}
	ck.reply.insert = text
	ck.reply.focusNext = true
	return true
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

// ownedKeys takes a list's keys while keysOwned: they run nothing.
func (a *App) ownedKeys(el ui.Element) bool {
	if !a.keysOwned() {
		return false
	}
	if el.Shortcut(0, ui.KeyEscape) && a.ck.spawn != nil && a.ck.spawn.open {
		// Escape before the sheet's field had the keys still closes it.
		a.spawnClose()
	}
	return true
}

// keysRing draws a 2 DIP accent ring 1 DIP inside el, the selected row or
// card, while its list has the keys (council round 4, M3). The ring paints over the children and takes no room.
func keysRing(el ui.Element, on bool, pal *palette, radius float32) {
	if !on {
		return
	}
	col := pal.accent
	el.DrawOver(func(p *ui.Painter, r ui.Rect) {
		p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, col, radius, 2)
	})
}
