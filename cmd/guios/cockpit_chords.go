package main

import (
	"runtime"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The agents view's chords: every action of its lists, since a printable
// key never acts (cockpit_guard.go). One table, so that the key line, the
// buttons, the help and the keys sheet name the same chords as the code
// runs. Cmd is Command on the Mac and Control elsewhere (ui.Cmd).
//
// None of them is a text field's chord (Ctrl+A C V X Z Y, Shift and Ctrl
// with the arrows) or one guios binds for the whole window (actions.go).
// On the Mac, Cmd+H hides the app and Cmd+M minimizes it, so snooze, the
// Mail view and the board take Shift as well.

// chordID names a chord.
type chordID int

const (
	chAllow      chordID = iota // allow once; a risky call asks first
	chAlways                    // always allow, asks first
	chReject                    // reject
	chSettle                    // settle, or take off the shelf
	chUndoSettle                // take back the last settle
	chSnooze                    // snooze, or show again
	chBoard                     // the board, and back
	chMail                      // the Mail view, and back
	chFilter                    // needs you only
	chTabPrev                   // the focus tab before
	chTabNext                   // the focus tab after
	chNewAgent                  // a new agent; on the board, one on the card
	chClaim                     // the board: give the card to the selected agent, asks first
	chCardLeft                  // the board: move the card a column left
	chCardRight                 // the board: move the card a column right
	chAddCard                   // the board's card field: add the card
	chJudge                     // Compare: start a judge, asks first
	chKeep                      // Compare: keep the attempt, asks first
)

// chord is one chord: its keys, and whether the agents list runs it
// (ckChordKeys). The board and Compare run their own.
type chord struct {
	id   chordID
	mods ui.Modifiers
	key  ui.Key
	list bool
}

var ckChords = []chord{
	{chAllow, ui.Cmd, ui.KeyEnter, true},
	{chAlways, ui.Cmd | ui.Alt, ui.KeyEnter, true},
	{chReject, ui.Cmd | ui.Shift, ui.KeyX, true},
	{chSettle, ui.Cmd, ui.KeyS, true},
	{chUndoSettle, ui.Cmd, ui.KeyU, true},
	{chSnooze, ui.Cmd | ui.Shift, ui.KeyH, true},
	{chBoard, ui.Cmd | ui.Shift, ui.KeyK, true},
	{chMail, ui.Cmd | ui.Shift, ui.KeyM, true},
	{chFilter, ui.Cmd, ui.KeyI, true},
	{chTabPrev, ui.Alt, ui.KeyBracketLeft, true},
	{chTabNext, ui.Alt, ui.KeyBracketRight, true},
	// Ctrl+Shift+N is the window's New agent (actions.go): listed here
	// for its name only.
	{chNewAgent, ui.Cmd | ui.Shift, ui.KeyN, false},
	{chClaim, ui.Cmd, ui.KeyE, false},
	{chCardLeft, ui.Alt | ui.Shift, ui.KeyLeft, false},
	{chCardRight, ui.Alt | ui.Shift, ui.KeyRight, false},
	{chAddCard, ui.Cmd, ui.KeyEnter, false},
	{chJudge, ui.Cmd | ui.Alt, ui.KeyJ, false},
	{chKeep, ui.Cmd | ui.Alt, ui.KeyK, false},
}

// chordOf is the chord of id.
func chordOf(id chordID) chord {
	for _, c := range ckChords {
		if c.id == id {
			return c
		}
	}
	return chord{}
}

// chordHit reports whether el got the chord id in this build.
func chordHit(el ui.Element, id chordID) bool {
	c := chordOf(id)
	return el.Shortcut(c.mods, c.key)
}

// chordName is a chord as this platform writes it: "Ctrl+↵", "⇧⌘X".
func chordName(id chordID) string {
	c := chordOf(id)
	mac := runtime.GOOS == "darwin"
	cmd := c.mods&ui.Ctrl != 0 || c.mods&ui.Super != 0
	if mac {
		// Apple's order, as the menus and the empty state write it:
		// ⌥ ⇧ ⌘, then the key ("⇧⌘X", not "⌘⇧X").
		var b strings.Builder
		if c.mods&ui.Alt != 0 {
			b.WriteString("⌥")
		}
		if c.mods&ui.Shift != 0 {
			b.WriteString("⇧")
		}
		if cmd {
			b.WriteString("⌘")
		}
		return b.String() + chordKeyName(c.key)
	}
	var parts []string
	if cmd {
		parts = append(parts, "Ctrl")
	}
	if c.mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if c.mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, chordKeyName(c.key)), "+")
}

// chordKeyName is the key of a chord as a key cap shows it.
func chordKeyName(k ui.Key) string {
	switch k {
	case ui.KeyEnter:
		return "↵"
	case ui.KeyLeft:
		return "←"
	case ui.KeyRight:
		return "→"
	case ui.KeyBracketLeft:
		return "["
	case ui.KeyBracketRight:
		return "]"
	}
	if k >= ui.KeyA && k <= ui.KeyZ {
		return string(rune('A' + (k - ui.KeyA)))
	}
	return "?"
}

// chordWords is what a chord does, for the help sheet.
func chordWords(id chordID) string {
	switch id {
	case chAllow:
		return locales.CockpitAllowOnce()
	case chAlways:
		return locales.CockpitAllowAlways()
	case chReject:
		return locales.CockpitReject()
	case chSettle:
		return locales.CockpitSettle()
	case chUndoSettle:
		return locales.KeysUndoSettle()
	case chSnooze:
		return locales.CockpitSnooze()
	case chBoard:
		return locales.CockpitTabBoard()
	case chMail:
		return locales.CockpitMailForYou()
	case chFilter:
		return locales.KeysNeedsOnly()
	case chTabPrev:
		return locales.KeysTabPrev()
	case chTabNext:
		return locales.KeysTabNext()
	case chNewAgent:
		return locales.ActionNewAgent()
	case chClaim:
		return locales.KeysGiveCard()
	case chCardLeft:
		return locales.KeysMoveCardLeft()
	case chCardRight:
		return locales.KeysMoveCardRight()
	case chAddCard:
		return locales.CockpitAddCard()
	case chJudge:
		return locales.CompareJudge()
	case chKeep:
		return locales.KeysKeepAttempt()
	}
	return ""
}
