package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
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
		go func() {
			if _, err := e.stream.Write(b); err != nil {
				a.update(func() { a.toast(locales.ToastTypeaheadLost(err.Error())) })
			}
		}()
	}
	// The selected session's: a session another machine shares shows
	// under each of its hosts, with the same panes.
	if s := a.session(a.selected); len(b) > 0 || s != nil && a.focusOf(s) == id {
		a.wantFocus = id
	}
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

// keepKeyboard gives the keyboard back to the pane the user is in when no
// element of the main window has it, as after a rebuild put the pane's
// terminal under a new element: typed keys went nowhere until a click.
// It tries once each time the keyboard is lost, so a pane that cannot take
// it costs no frames.
func (a *App) keepKeyboard(root ui.Element) {
	findTook := a.find.focused
	a.find.focused = false
	if !a.focused || a.pal.open || a.help.open || a.xp.open || a.sbr.open || a.sb.on || a.prompt != nil || a.picker.open || a.confirm != nil || a.wtAsk != nil || a.ck.open || a.tk.live() {
		return
	}
	if root.FocusWithin() || findTook {
		a.refocused = ""
		return
	}
	s := a.session(a.selected)
	if s == nil {
		return
	}
	id := a.focusOf(s)
	if e := a.terms[id]; id == "" || a.refocused == id || e == nil || e.term == nil {
		return
	}
	a.refocused = id
	a.defer_(a.focusTerminal)
}
