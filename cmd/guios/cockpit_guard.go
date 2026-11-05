package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The typing guard. The agents view's lists act on single keys: a allows,
// d settles, n starts an agent. A person who types a sentence while a list
// holds the focus, because they think the reply box has it, must not have
// the sentence change anything. A guard on the time between keys cannot
// tell them apart: a slow typist's keys come as far apart as a person's
// deliberate ones (council round 3, M1). So the lists are built so that a
// letter cannot do harm:
//
//   - A letter only moves the cursor, shows something, or asks. A letter
//     that answers an approval, settles, dismisses or passes mail on opens
//     a confirm step that says what will happen (ckAsk). In the confirm,
//     only Enter and Escape act. A letter cancels it and goes to the reply
//     box. A chord (Ctrl+Enter) allows a call that is not risky at once.
//   - A letter that is no list key goes to the reply box, which then has
//     the keys: the rest of the sentence lands there.
//   - The lists draw a 2 DIP ring while they hold the keys, so the person
//     sees where the letters go.
//
// Time still guards one thing: an approval card answers only after it has
// shown the prompt as it is for ckAnswerSettle, so that an Enter or a click
// meant for one prompt does not answer the next one that took its place.

// ckKey is a key a list took.
type ckKey struct {
	key   ui.Key
	shift bool
}

// keyKind is what a list key is to the guard.
type keyKind int

const (
	kindProse keyKind = iota // no list key here: typed text
	kindMove                 // moves the cursor
	kindView                 // shows something, or opens a confirm
)

// typingGuard is the lists' guard state.
type typingGuard struct {
	hinted bool // the hint was shown since the last list key
	// prev is the move or view letter the list ran last, when no other
	// key came after it: the first letter of a sentence, if the next key
	// is typed text.
	prev *prevKey
	// before is prev as it was when the current list key came: a confirm
	// step that key opens keeps it, for "make", where m showed the Mail
	// tab and a asked.
	before *prevKey
}

// prevKey is a move or view letter that ran: its character, how to take
// back what it did, and where its text goes if prose follows.
type prevKey struct {
	r     rune
	undo  func()
	prose func(string) bool
}

// setPrev notes the letter that just ran, so that typed text right after
// it takes it back and keeps it (council round 4: "make" lost its m).
func (g *typingGuard) setPrev(r rune, undo func(), prose func(string) bool) {
	g.prev = &prevKey{r: r, undo: undo, prose: prose}
}

// proseKeys are the keys typing makes, besides Shift.
var proseKeys = []ui.Key{
	ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM,
	ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ,
	ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9,
	ui.KeySpace, ui.KeyComma, ui.KeyPeriod, ui.KeyMinus, ui.KeyQuote, ui.KeySlash, ui.KeySemicolon, ui.KeyEqual, ui.KeyBackquote,
}

// keyRunes are the characters of the keys that are no letter, without and
// with Shift, as a US keyboard types them.
var keyRunes = map[ui.Key][2]rune{
	ui.Key0: {'0', ')'}, ui.Key1: {'1', '!'}, ui.Key2: {'2', '@'}, ui.Key3: {'3', '#'}, ui.Key4: {'4', '$'},
	ui.Key5: {'5', '%'}, ui.Key6: {'6', '^'}, ui.Key7: {'7', '&'}, ui.Key8: {'8', '*'}, ui.Key9: {'9', '('},
	ui.KeySpace: {' ', ' '}, ui.KeyComma: {',', '<'}, ui.KeyPeriod: {'.', '>'}, ui.KeyMinus: {'-', '_'},
	ui.KeyQuote: {'\'', '"'}, ui.KeySlash: {'/', '?'}, ui.KeySemicolon: {';', ':'}, ui.KeyEqual: {'=', '+'},
	ui.KeyBackquote: {'`', '~'},
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

// listKeys tells the guard what each key is to a list. prose takes a
// letter that is no list key; it reports whether a reply box took it.
type listKeys struct {
	kind  func(ckKey) keyKind
	prose func(text string) bool
	hint  func() string
}

// guard reads a list's letter of this build, if any. A list key comes back
// to run; a letter that is no list key goes to prose, or the hint says
// once where the keys go.
func (a *App) guard(el ui.Element, g *typingGuard, lk listKeys) (ckKey, bool) {
	k, ok := takeProse(el)
	if !ok {
		return ckKey{}, false
	}
	if lk.kind(k) != kindProse {
		g.hinted = false
		g.before, g.prev = g.prev, nil
		return k, true
	}
	text := ""
	prose := lk.prose
	if p := g.prev; p != nil {
		// The letter before this one was the sentence's first: it moved
		// the cursor or showed something. Take that back, and the letter
		// goes with the text. The take back may change the view, so the
		// text goes where the letter's list sends it.
		g.prev = nil
		if p.undo != nil {
			p.undo()
		}
		text = string(p.r)
		if p.prose != nil {
			prose = p.prose
		}
	}
	if ch := keyRune(k); ch != 0 {
		text += string(ch)
	}
	if prose != nil && prose(text) {
		return ckKey{}, false
	}
	if !g.hinted {
		g.hinted = true
		if lk.hint != nil {
			a.toast(lk.hint())
		} else {
			a.toast(locales.CockpitKeysToList())
		}
	}
	return ckKey{}, false
}

// ckKind is what a key is to the agents list, on the row the cursor is on:
// a key that does nothing here is typed text.
func (a *App) ckKind(k ckKey) keyKind {
	ck := &a.ck
	r, rowOK := a.ckSelected()
	item := rowOK && r.item != nil
	answerable := item && ckAnswerable(*r.item)
	_, onAgent := a.ckSelectedAgent()
	if k.shift {
		switch {
		case k.key == ui.KeyA && answerable,
			k.key == ui.KeyR && item && ckHeld(*r.item),
			k.key == ui.KeyD && onAgent,
			k.key == ui.KeyM:
			return kindView
		}
		return kindProse
	}
	switch k.key {
	case ui.KeyJ, ui.KeyK:
		return kindMove
	case ui.KeyH, ui.KeyL:
		if ck.stacked {
			return kindMove
		}
	case ui.KeySpace:
		if rowOK && r.kind == ckRowHead || a.ckNewestImage() != nil {
			return kindView
		}
	case ui.KeyA:
		if answerable {
			return kindView
		}
	case ui.KeyR, ui.KeyI, ui.KeyM, ui.KeyB, ui.KeyV, ui.KeyF, ui.KeyN:
		return kindView
	case ui.KeyD:
		if onAgent {
			return kindView
		}
	case ui.KeyU:
		if ck.undo != nil && time.Now().Before(ck.undo.until) {
			return kindView
		}
	case ui.KeyZ:
		// A held approval waits on its hook: it cannot be snoozed, and z
		// does not offer to (QA-074).
		if item && !ckHeld(*r.item) {
			return kindView
		}
	case ui.KeyX:
		if item {
			return kindView
		}
	case ui.KeyP:
		if item && r.item.HeldID != 0 {
			return kindView
		}
	case ui.KeyC:
		if ag, ok := a.ckSelectedAgent(); ok && a.ckHasSiblings(ag) {
			return kindView
		}
	}
	return kindProse
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

// ckListKeys are the agents list's keys for the guard.
func (a *App) ckListKeys() listKeys {
	return listKeys{kind: a.ckKind, prose: a.ckProse}
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
	if el.Shortcut(0, ui.KeyEscape) && a.ck.spawn != nil && a.ck.spawn.open {
		// Escape before the sheet's field had the keys still closes it.
		a.spawnClose()
	}
	return true
}

// keysRing draws a 2 DIP accent ring 1 DIP inside el, the selected row or
// card, while its list has the keys: a person sees where the letters they
// type go (council round 3, M1). It marks the row, not the column (council
// round 4, M3). The ring paints over the children and takes no room.
func keysRing(el ui.Element, on bool, pal *palette, radius float32) {
	if !on {
		return
	}
	col := pal.accent
	el.DrawOver(func(p *ui.Painter, r ui.Rect) {
		p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, col, radius, 2)
	})
}
