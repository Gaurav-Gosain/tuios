package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// Typeahead: keys typed into a pane before its terminal is there.
//
// A pane's terminal attaches a moment after the pane shows: a new session,
// a split, a pane of another machine. Until then the pane's card holds a
// stand-in that takes the keyboard in the terminal's place, through the
// same handle, and keeps what is typed. The terminal sends it to the
// program first thing once it attaches, so a person who types right after
// Ctrl+Shift+T loses nothing.

// attachDelay holds every pane's attach, in the e2e build only.
var attachDelay time.Duration

// typeaheadMax bounds what a stand-in keeps: a paste into a pane that
// never attaches stays small.
const typeaheadMax = 64 << 10

// pendingInput is a pane's stand-in for its terminal while it attaches.
// The keyboard goes to it as to the terminal (the pane's handle), and the
// bytes it keeps go to the program once the terminal is there.
func (a *App) pendingInput(c *ui.Context, id string) {
	box := ui.Box(c.Key("typeahead")).Fill().Focusable().FocusRing(false).Bind(a.paneRef(id))
	box.HandleInput(func(ev ui.InputEvent) bool {
		var b []byte
		switch ev.Kind {
		case ui.InputKeyDown:
			if typesText(ev.Mods, ev.Key) {
				return false // its text comes next, as InputText
			}
			b = keyInput(ev.Mods, ev.Key)
		case ui.InputText:
			b = []byte(ev.Text)
		default:
			return false
		}
		if len(b) == 0 {
			return false
		}
		if a.early == nil {
			a.early = map[string][]byte{}
		}
		if len(a.early[id])+len(b) <= typeaheadMax {
			a.early[id] = append(a.early[id], b...)
		}
		return true
	})
	box.TextCaret(ui.Rect{})
}

// flushTypeahead sends a pane's kept keys to its program, once its
// terminal attached, and the keyboard to the terminal when the stand-in
// had it.
func (a *App) flushTypeahead(id string, e *termEntry) {
	b := a.early[id]
	delete(a.early, id)
	if len(b) > 0 && e.stream != nil {
		go e.stream.Write(b)
	}
	if len(b) > 0 || a.focusOf(a.sessionOfPaneOr(id)) == id {
		a.wantFocus = id
	}
}

// sessionOfPaneOr is the session a pane is of, an empty one for none.
func (a *App) sessionOfPaneOr(id string) *proto.Session {
	if s := a.sessionOfPane(id); s != nil {
		return s
	}
	return &proto.Session{}
}

// keyInput is what a terminal sends a program for a key that types no
// text, in its default modes: Enter, Tab, Backspace, Escape, the arrows,
// the editing keys, and Control and Alt chords.
func keyInput(mods ui.Modifiers, key ui.Key) []byte {
	if mods&ui.Super != 0 {
		return nil
	}
	plain := map[ui.Key]string{
		ui.KeyEnter: "\r", ui.KeyTab: "\t", ui.KeyBackspace: "\x7f", ui.KeyEscape: "\x1b",
		ui.KeyUp: "\x1b[A", ui.KeyDown: "\x1b[B", ui.KeyRight: "\x1b[C", ui.KeyLeft: "\x1b[D",
		ui.KeyHome: "\x1b[H", ui.KeyEnd: "\x1b[F", ui.KeyDelete: "\x1b[3~", ui.KeyInsert: "\x1b[2~",
		ui.KeyPageUp: "\x1b[5~", ui.KeyPageDown: "\x1b[6~",
	}
	if mods == 0 {
		return []byte(plain[key])
	}
	if mods == ui.Shift && key == ui.KeyTab {
		return []byte("\x1b[Z")
	}
	if k, ok := keyString(mods, key); ok {
		return keyBytes(k)
	}
	return nil
}
