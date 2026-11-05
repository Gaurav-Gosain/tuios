package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The typing guard. The agents view's lists act on single keys: a allows,
// n starts an agent, d settles. A person who types a sentence while a list
// holds the focus, because they think the reply box has it, must not have
// the sentence run as commands: "please also" holds two a, the double
// press that allows a risky command.
//
// Time alone cannot tell a command from prose: a slow typist's keys come
// as far apart as a person's deliberate ones (council round 3, M1). So the
// guard reads the order of the keys:
//
//   - A run is the keys a list took since the last boundary: a key that is
//     no letter (an arrow, Enter, Escape, a bracket), a click on a row, or
//     a pause of runGap.
//   - In a run, only the first key that is not a move may run. Moves
//     (j k, h l while stacked) always run: "j a" moves to an approval and
//     allows it. Every later list key of the run is held as text and runs
//     nothing.
//   - A letter that is no list key here is prose for sure: the run is taken
//     back (moves undone, views closed, a first press disarmed), and the
//     run's letters and this one go to the reply box, which then has the
//     keys. A list without a reply box only says where the keys went.
//   - A two-key action (the second a of a risky allow) counts only when no
//     other key came between the two presses.
//
// A key that is no move also waits guardWindow before it runs, so that the
// next key of a fast typist takes it back before it does anything.

// guardWindow is how long a list's act key waits for the next key.
const guardWindow = 300 * time.Millisecond

// runGap is the pause that ends a run: a typist's keys come closer, even
// at one key a second.
const runGap = 1500 * time.Millisecond

// ckKey is a key a list took.
type ckKey struct {
	key   ui.Key
	shift bool
}

// heldKey is an act key waiting out guardWindow.
type heldKey struct {
	k   ckKey
	at  time.Time
	seq uint64
}

// runKey is a key of the run, with how to take back what it did.
type runKey struct {
	k    ckKey
	undo func()
}

// keyKind is what a list key is to the guard.
type keyKind int

const (
	kindProse keyKind = iota // no list key here: prose for sure
	kindMove                 // moves the cursor, runs at once
	kindView                 // shows something, runs at once, can be taken back
	kindAct                  // changes something: waits guardWindow
)

// typingGuard is the lists' guard state.
type typingGuard struct {
	last   time.Time // the last key of prose
	seq    uint64    // counts every key the lists took
	cur    uint64    // the seq of the key running now
	held   *heldKey
	hinted bool // the hint for this run was shown
	run    []runKey
	acted  bool // a key that is no move ran, or waits, in this run
}

// setUndo notes how to take back the key that just ran.
func (g *typingGuard) setUndo(fn func()) {
	if n := len(g.run); n > 0 {
		g.run[n-1].undo = fn
	}
}

// boundary ends the run: the key that came is no prose.
func (g *typingGuard) boundary() {
	g.seq++
	g.held = nil
	g.run, g.acted, g.hinted = nil, false, false
}

// takeBack takes back what the run's keys did, newest first, and returns
// their text.
func (g *typingGuard) takeBack() string {
	var text []rune
	for i := len(g.run) - 1; i >= 0; i-- {
		if fn := g.run[i].undo; fn != nil {
			fn()
		}
	}
	for _, r := range g.run {
		if ch := keyRune(r.k); ch != 0 {
			text = append(text, ch)
		}
	}
	g.run, g.acted, g.held = nil, false, nil
	return string(text)
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

// typing reports whether a key now would come within guardWindow of the
// last key of prose.
func (g *typingGuard) typing(now time.Time) bool {
	return !g.last.IsZero() && now.Sub(g.last) < guardWindow
}

// listKeys tells the guard what each key is to a list. second reports
// whether a key completes a two-key action its first press armed: it may
// run though the run acted. prose takes the text of a run that turned out
// to be prose; it reports whether a reply box took it.
type listKeys struct {
	kind   func(ckKey) keyKind
	second func(ckKey) bool
	prose  func(text string) bool
	hint   func() string
}

// guard runs a list's keys of prose through the guard. It returns a key to
// run now: a move or a view at once, an act that waited out guardWindow
// with no key after it.
func (a *App) guard(c *ui.Context, el ui.Element, g *typingGuard, lk listKeys) (ckKey, bool) {
	now := time.Now()
	if k, ok := takeProse(el); ok {
		if !g.last.IsZero() && now.Sub(g.last) >= runGap {
			g.run, g.acted, g.hinted, g.held = nil, false, false, nil
		}
		g.last = now
		g.seq++
		// A key came: the act that waited is prose, and runs nothing.
		g.held = nil
		kind := lk.kind(k)
		if kind == kindProse {
			g.run = append(g.run, runKey{k: k})
			text := g.takeBack()
			if lk.prose != nil && lk.prose(text) {
				g.hinted = true
				return ckKey{}, false
			}
			a.guardHint(g, lk)
			return ckKey{}, false
		}
		if kind != kindMove && g.acted && (lk.second == nil || !lk.second(k)) {
			// A later key of the run: text, not a command.
			g.run = append(g.run, runKey{k: k})
			if !g.hinted {
				g.hinted = true
				a.toast(locales.CockpitTypedNoCommand())
			}
			return ckKey{}, false
		}
		g.run = append(g.run, runKey{k: k})
		if kind != kindMove {
			g.acted = true
		}
		if kind == kindMove || kind == kindView {
			g.cur = g.seq
			return k, true
		}
		g.held = &heldKey{k: k, at: now, seq: g.seq}
		c.After(guardWindow)
		return ckKey{}, false
	}
	if h := g.held; h != nil {
		switch wait := guardWindow - now.Sub(h.at); {
		case h.seq != g.seq || wait < -time.Second:
			// Another key came after it, or it waited while the list
			// had no focus: it is not a command now.
			g.held = nil
		case wait > 0:
			c.After(wait)
		default:
			g.held = nil
			g.cur = h.seq
			return h.k, true
		}
	}
	return ckKey{}, false
}

// guardHint says once in a run where the keys went.
func (a *App) guardHint(g *typingGuard, lk listKeys) {
	if g.hinted {
		return
	}
	g.hinted = true
	if lk.hint != nil {
		a.toast(lk.hint())
		return
	}
	a.toast(locales.CockpitKeysToList())
}

// ckKind is what a key is to the agents list, on the row the cursor is on:
// a key that does nothing here is prose.
func (a *App) ckKind(k ckKey) keyKind {
	ck := &a.ck
	r, rowOK := a.ckSelected()
	item := rowOK && r.item != nil
	answerable := item && ckAnswerable(*r.item)
	_, onAgent := a.ckSelectedAgent()
	if k.shift {
		switch k.key {
		case ui.KeyA:
			if answerable {
				return kindAct
			}
		case ui.KeyR:
			if item && ckHeld(*r.item) {
				return kindView
			}
		case ui.KeyD:
			if onAgent {
				return kindAct
			}
		case ui.KeyM:
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
			return kindAct
		}
	case ui.KeyR:
		if a.ckCanReject() {
			return kindAct
		}
		return kindView
	case ui.KeyI, ui.KeyM, ui.KeyB, ui.KeyV, ui.KeyF, ui.KeyN:
		return kindView
	case ui.KeyD:
		if onAgent {
			return kindAct
		}
	case ui.KeyU:
		if ck.undo != nil && time.Now().Before(ck.undo.until) {
			return kindAct
		}
	case ui.KeyZ, ui.KeyX:
		if item {
			return kindAct
		}
	case ui.KeyP:
		if item && r.item.HeldID != 0 {
			return kindAct
		}
	case ui.KeyC:
		if ag, ok := a.ckSelectedAgent(); ok && a.ckHasSiblings(ag) {
			return kindView
		}
	}
	return kindProse
}

// ckSecond reports whether k is the second press of the risky allow its
// first press armed, with no key between the two.
func (a *App) ckSecond(k ckKey) bool {
	ck := &a.ck
	if k.key != ui.KeyA || ck.armed.decision == "" || ck.armed.seq+1 != ck.guard.seq {
		return false
	}
	return ck.armed.decision == map[bool]string{false: "once", true: "always"}[k.shift]
}

// ckProse puts a run of prose in the reply box of the agent the cursor is
// on, and the keys with it. It reports false when the row has no reply box.
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
	return listKeys{kind: a.ckKind, second: a.ckSecond, prose: a.ckProse}
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
		a.ck.guard.seq++
	}
	if el.Shortcut(0, ui.KeyEscape) && a.ck.spawn != nil && a.ck.spawn.open {
		// Escape before the sheet's field had the keys still closes it.
		a.spawnClose()
	}
	return true
}

// keysRing draws a 2 DIP ring inside el while on: the list has the keys,
// so that a person sees where the letters they type go (council round 3,
// M1). The ring paints over the children and takes no room.
func keysRing(el ui.Element, on bool, pal *palette) {
	if !on {
		return
	}
	col := pal.accent
	el.DrawOver(func(p *ui.Painter, r ui.Rect) { p.Stroke(r, col, 0, 2) })
}
