package app

import (
	"image/color"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The secret dialog: a program in a pane, such as an agent's ssh, asks for a
// passphrase, a password or a PIN, and the daemon asks the person
// (session/askpass.go). The prompt is an ask in the Inbox mirror marked with
// Secret, and this client opens it as a dialog with a hidden field when the
// person looks at the pane that asks. Elsewhere it waits in the Inbox.
//
// What the person types is held in a byte buffer of the dialog's own, never
// in a string and never in the shared text field, and it is zeroed when the
// dialog sends, cancels or closes. The field shows one dot per character.
// Keys that send-keys or a tape put through the client type nothing, so no
// script and no agent can fill the field. A key in the first moment after the
// dialog opens is dropped, since it was typed for the pane.

// overlayKindAskpass is the dialog's overlay kind.
const overlayKindAskpass = "askpass"

// askpassSettle is how long the dialog ignores keys after it opens.
const askpassSettle = 500 * time.Millisecond

// askpassState is the prompt the dialog shows.
type askpassState struct {
	// open is the prompt shown, nil when the dialog is closed.
	open *session.AttentionItem
	// buf is what the person typed. It is zeroed whenever it is dropped.
	buf []byte
	// openedAt is when it opened. Keys are ignored for askpassSettle.
	openedAt time.Time
	// sent is true while this client's answer or cancel is on its way. A
	// call that fails clears it, so the person can type again.
	sent bool
	// hidden is a prompt the person closed here with esc while an answer
	// to it could not get through. It stays in the Inbox, and the dialog
	// opens on it again when the person comes back to its pane.
	hidden string
	// holdPane and holdUntil keep the dialog shut for a moment on a pane
	// whose prompt the person just cancelled, so a program that asks again
	// at once, such as a retry loop around ssh, does not take the keys
	// back before the person can move.
	holdPane  string
	holdUntil time.Time
}

// askpassHold is how long the dialog stays shut on a pane after esc.
const askpassHold = 3 * time.Second

// AskpassOpen reports whether the dialog owns the keyboard.
func (m *OS) AskpassOpen() bool { return m.askpass.open != nil }

// askpassFor is the secret prompt about the pane in front of the person, or
// nil. The dialog opens on it, and it needs no Inbox alert.
func (m *OS) askpassFor() *session.AttentionItem {
	w := m.GetFocusedWindow()
	if !m.Inbox.anySecret || m.ShowInbox || w == nil {
		return nil
	}
	for i := range m.Inbox.Items {
		if it := &m.Inbox.Items[i]; it.Secret != nil && it.RequestID != "" && it.Window == w.ID && m.inboxAttached(*it) {
			return it
		}
	}
	return nil
}

// syncAskpass opens the dialog on a secret prompt about the pane in front of
// the person, and closes it once its prompt is gone. It runs after every
// message, so the dialog opens the moment the person focuses the pane.
//
// The dialog is about one pane. When the person moves to another pane, or
// opens the Inbox, it closes and gives the keys back. The prompt waits in
// the Inbox and the dialog opens again when the person comes back.
func (m *OS) syncAskpass() {
	st := &m.askpass
	front := m.askpassFor()
	if st.open != nil && front != nil && front.ID == st.open.ID {
		return
	}
	if st.hidden != "" && (front == nil || front.RequestID != st.hidden) {
		// The person left the pane of the prompt they closed.
		st.hidden = ""
	}
	clear(st.buf)
	*st = askpassState{buf: st.buf[:0], hidden: st.hidden, holdPane: st.holdPane, holdUntil: st.holdUntil}
	if front == nil || front.RequestID == st.hidden || m.UpgradeConfirmOpen() || m.inboxNonce() == "" {
		return
	}
	if front.Window == st.holdPane && time.Now().Before(st.holdUntil) {
		return
	}
	cp := *front
	st.open, st.openedAt = &cp, time.Now()
}

// AskpassKey handles a key while the dialog is open.
func (m *OS) AskpassKey(msg tea.KeyPressMsg) tea.Cmd {
	st := &m.askpass
	if m.notThePersonsKey() || time.Since(st.openedAt) < askpassSettle {
		// Keys a script or a pane sent through the client are not the
		// person's, and type nothing.
		return nil
	}
	if st.sent {
		// An answer is on its way. Esc still gets the person out: it
		// closes the dialog here, and the prompt waits in the Inbox.
		if msg.String() == "esc" {
			clear(st.buf)
			*st = askpassState{buf: st.buf[:0], hidden: st.open.RequestID}
		}
		return nil
	}
	switch msg.String() {
	case "enter":
		if len(st.buf) > 0 {
			return m.askpassSend(false)
		}
	case "esc":
		st.holdPane, st.holdUntil = st.open.Window, time.Now().Add(askpassHold)
		return m.askpassSend(true)
	case "backspace":
		_, n := utf8.DecodeLastRune(st.buf)
		clear(st.buf[len(st.buf)-n:])
		st.buf = st.buf[:len(st.buf)-n]
	case "ctrl+u":
		clear(st.buf)
		st.buf = st.buf[:0]
	default:
		if msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
			m.AskpassPaste(msg.Text)
		}
	}
	return nil
}

// AskpassPaste puts text in the field: printable characters of its first
// line, as a password manager pastes it.
func (m *OS) AskpassPaste(text string) {
	st := &m.askpass
	if st.sent || m.notThePersonsKey() {
		return
	}
	for _, r := range text {
		if r == '\n' || len(st.buf)+utf8.RuneLen(r) > 4096 {
			return
		}
		if r >= 0x20 && r != 0x7f {
			st.buf = utf8.AppendRune(st.buf, r)
		}
	}
}

// askpassSend sends the answer, or the cancel, through answer-ask with this
// client's attach nonce, and zeroes the field. The call's copy is zeroed once
// the call is sent.
func (m *OS) askpassSend(cancel bool) tea.Cmd {
	st := &m.askpass
	extra := map[string]any{"cancel": true}
	label := "cancelled"
	if !cancel {
		extra = map[string]any{"secret": append([]byte(nil), st.buf...)}
		label = "answer sent"
	}
	clear(st.buf)
	st.buf, st.sent = st.buf[:0], true
	return m.inboxAnswerAskCmd(*st.open, label, extra)
}

// askpassAnswered takes the result of the dialog's answer-ask call. A call
// that failed, or that the daemon did not apply, clears sent: the dialog
// takes keys again, so the person can send the answer again or cancel.
func (m *OS) askpassAnswered(msg InboxAskAnsweredMsg) {
	if st := &m.askpass; st.open != nil && st.sent && (msg.Err != nil || !msg.Applied) {
		st.sent = false
	}
}

// renderAskpass draws the dialog. The prompt and the program are never cut:
// they wrap, since they are what the person decides on.
func (m *OS) renderAskpass() (string, overlay.Geometry) {
	st := &m.askpass
	sec := st.open.Secret
	pal := m.overlayUI()
	width := overlay.DialogFitWidth(64, m.modalRoomWidth())
	title, field := "Enter a secret", "Answer:"
	// A yes or no question and a user name are not secrets: the person
	// sees what they type.
	shown := false
	switch sec.Kind {
	case "confirm":
		title, field, shown = "Answer a question", "Answer (yes or no):", true
	case "username":
		title, field, shown = "Enter a user name", "User name:", true
	case "passphrase":
		title, field = "Unlock a key", "Passphrase:"
	case "password":
		title, field = "Enter a password", "Password:"
	case "pin":
		title, field = "Enter a PIN", "PIN:"
	}
	program, programInk := "Program: "+printableTitle(sec.Program), pal.FgDim
	warn := ""
	switch {
	case sec.Program == "":
		program, warn = "", "tuios cannot tell which program asks. Check before you answer."
	case !sec.Known:
		programInk, warn = pal.Fg, "tuios does not know this program. Check before you answer."
	}
	note := "The answer goes to the program only. tuios does not keep it."
	if st.sent {
		note = "Sending your answer..."
	}
	type line struct {
		text string
		ink  color.Color
	}
	// A line with no ink is a gap. A line with ink and no text is left out.
	lines := []line{{"", nil}, {inboxWho(*st.open) + " runs a program that asks:", pal.FgDim}}
	for _, l := range strings.Split(sec.Prompt, "\n") {
		lines = append(lines, line{"  " + printableTitle(l), pal.Fg})
	}
	lines = append(lines, line{"", nil}, line{program, programInk}, line{warn, pal.Warn}, line{"", nil}, line{field, pal.FgDim})

	body := []string{dialogTitleLine(title, width-2, width, pal.Fg, pal.Canvas)}
	for _, l := range lines {
		if l.text == "" && l.ink != nil {
			continue
		}
		for _, w := range wrapPlain(l.text, max(width-2, 1)) {
			body = append(body, dialogBodyLine(w, width, l.ink, pal.Canvas))
		}
	}
	// The field is a band of the surface colour across the dialog, so it
	// reads as a field when it is empty. A bar marks where text goes.
	text := strings.Repeat("•", utf8.RuneCount(st.buf))
	if shown {
		text = printableTitle(string(st.buf))
	}
	dots := fieldTail(text, max(width-5, 1))
	body = append(body, dialogBodyLine(" "+dots+"▏", width, pal.Fg, pal.Surface),
		dialogBodyLine("", width, pal.FgDim, pal.Canvas), dialogBodyLine(note, width, pal.FgMute, pal.Canvas))
	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	return overlay.Dialog{Title: "secret", Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
}
