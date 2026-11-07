package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// Keys typed while a workspace switch is on its way.
//
// A switch asks the daemon, and the new workspace's panes show only when its
// answer lands and their terminals attach (holdSwitches, shownSession), up to
// holdMax later. Until then the keyboard was on the pane of the workspace
// left, or on nothing at all: a Ctrl+B w N that came through the prefix card
// left the focus on nothing once the card went. A line typed in that time
// reached no pane, a Ctrl+B was dropped (keysShortcut gives the leader to a
// pane's terminal when the workspace has panes), and the next key of the
// prefix went to the shell as text (Mac round 2, F2-M1).
//
// Now the switch takes the keyboard into a sink from the key that asks for
// it. The sink keeps every key and text in order, and when the new
// workspace's pane gets the focus (focusTerminal) they go to it as if typed
// there: the leader and the prefix's keys to the prefix, the rest to the
// pane. A switch that never lands lets the keys go after switchWait.

// switchWait bounds how long the sink keeps the keys of a switch the daemon
// does not answer.
const switchWait = 2 * time.Second

// switchKeys is the sink of a switch on its way.
type switchKeys struct {
	sid    string    // the session whose switch is on its way, "" none
	since  time.Time // when it was asked
	landed bool      // the daemon's answer came (holdSwitches)
	ref    ui.Handle
	queue  []switchKey
}

// switchKey is one input the sink kept: a key press, or text.
type switchKey struct {
	mods ui.Modifiers
	key  ui.Key
	text string
}

// switchStarted notes a switch of the session asked, and takes the
// keyboard into the sink.
func (a *App) switchStarted(sid string) {
	sw := &a.sw
	if sw.sid != sid {
		sw.queue = nil
	}
	sw.sid, sw.since, sw.landed = sid, time.Now(), false
	if a.win != nil {
		a.win.Invalidate()
	}
}

// switchLanded notes the daemon's answer for the session.
func (a *App) switchLanded(sid string) {
	if a.sw.sid == sid {
		a.sw.landed = true
	}
}

// switching reports whether the shown session's switch is on its way: asked
// and not answered, or answered and held until its panes attach.
func (a *App) switching() bool {
	sw := &a.sw
	if sw.sid == "" || sw.sid != a.selected || a.quick.drop {
		// The scratch shows over every workspace and keeps the keys.
		return false
	}
	if a.holds[sw.sid] != nil {
		return true
	}
	return !sw.landed && time.Since(sw.since) < switchWait
}

// switchSink draws the sink while a switch is on its way: an element of no
// size that holds the keyboard and keeps what is typed.
func (a *App) switchSink(c *ui.Context) {
	sw := &a.sw
	if sw.sid == "" {
		return
	}
	if !a.switching() {
		// Answered and attached, or given up on: the pane takes the keys.
		sw.sid = ""
		if sw.ref.Focused(c) || len(sw.queue) > 0 {
			a.defer_(a.focusTerminal)
		}
		return
	}
	c.After(switchWait - time.Since(sw.since))
	sink := ui.Column(c.Key("switch-sink")).Width(0).Height(0).Focusable().FocusRing(false).Bind(&sw.ref)
	if !sw.ref.Focused(c) && !a.tk.live() {
		sw.ref.Focus()
	}
	sink.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputKeyDown:
			if !typesText(ev.Mods, ev.Key) {
				sw.queue = append(sw.queue, switchKey{mods: ev.Mods, key: ev.Key})
			}
			return true
		case ui.InputText:
			sw.queue = append(sw.queue, switchKey{text: ev.Text})
			return true
		}
		return false
	})
	sink.TextCaret(ui.Rect{})
}

// replaySwitchKeys sends what the sink kept to the pane that now has the
// keyboard, in the order it was typed. A key the prefix takes goes to the
// prefix, as in the pane's terminal (keyDown, and takeTyped in gated.Write).
func (a *App) replaySwitchKeys(pane string) {
	q := a.sw.queue
	a.sw.queue = nil
	t := a.termFor(pane)
	for _, k := range q {
		if k.text != "" {
			if t != nil {
				t.Send([]byte(k.text))
			}
			continue
		}
		if a.keyDown(pane, k.mods, k.key) || t == nil {
			continue
		}
		if b := switchKeyBytes(k.mods, k.key); b != nil {
			t.Send(b)
		}
	}
}

// switchKeyBytes are the bytes a key that types no text sends, in the
// plain encoding every shell reads.
func switchKeyBytes(mods ui.Modifiers, key ui.Key) []byte {
	name, ok := keyString(mods, key)
	if !ok {
		return nil
	}
	switch name {
	case "enter":
		return []byte{'\r'}
	case "tab":
		return []byte{'\t'}
	case "esc":
		return []byte{0x1b}
	case "backspace":
		return []byte{0x7f}
	case "up":
		return []byte("\x1b[A")
	case "down":
		return []byte("\x1b[B")
	case "right":
		return []byte("\x1b[C")
	case "left":
		return []byte("\x1b[D")
	case "home":
		return []byte("\x1b[H")
	case "end":
		return []byte("\x1b[F")
	case "delete":
		return []byte("\x1b[3~")
	}
	return keyBytes(name)
}
