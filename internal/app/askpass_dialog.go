package app

import (
	"image/color"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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

// askpassWidth is the dialog's preferred inner width.
const askpassWidth = 64

// askpassSettle is how long the dialog ignores keys after it opens.
const askpassSettle = 500 * time.Millisecond

// askpassMaxField bounds what the field takes, in bytes: the daemon's limit.
const askpassMaxField = 4096

// askpassDot is what the field shows for each character typed.
const askpassDot = "•"

// askpassState is the prompt the dialog shows.
type askpassState struct {
	// open is the prompt shown, nil when the dialog is closed.
	open *session.AttentionItem
	// buf is what the person typed. It is zeroed whenever it is dropped.
	buf []byte
	// openedAt is when it opened. Keys are ignored for askpassSettle.
	openedAt time.Time
	// sent is true once this client sent an answer or a cancel.
	sent bool
}

// AskpassOpen reports whether the dialog owns the keyboard.
func (m *OS) AskpassOpen() bool { return m.askpass.open != nil }

// drop zeroes and forgets the field.
func (st *askpassState) drop() {
	clear(st.buf)
	st.buf = st.buf[:0]
}

// syncAskpass opens the dialog on a secret prompt about the pane in front of
// the person, and closes it once its prompt is gone. It runs after every
// message, so the dialog opens the moment the person focuses the pane.
func (m *OS) syncAskpass() {
	st := &m.askpass
	if st.open != nil {
		if m.inboxIndex(st.open.ID) >= 0 {
			return
		}
		st.drop()
		*st = askpassState{buf: st.buf}
	}
	if !m.Inbox.anySecret || m.ShowInbox || m.UpgradeConfirmOpen() || m.DaemonClient == nil {
		return
	}
	w := m.GetFocusedWindow()
	if w == nil {
		return
	}
	for _, it := range m.Inbox.Items {
		if it.Secret != nil && it.RequestID != "" && it.Window == w.ID && m.inboxAttached(it) {
			if m.inboxNonce() == "" {
				return
			}
			*st = askpassState{open: &it, openedAt: time.Now(), buf: st.buf}
			return
		}
	}
}

// askpassShownFor reports whether the dialog shows, or opens on, the prompt
// it: then the prompt needs no Inbox alert.
func (m *OS) askpassShownFor(it session.AttentionItem) bool {
	if it.Secret == nil || m.ShowInbox || !m.inboxAttached(it) {
		return false
	}
	w := m.GetFocusedWindow()
	return w != nil && w.ID == it.Window
}

// AskpassKey handles a key while the dialog is open.
func (m *OS) AskpassKey(msg tea.KeyPressMsg) tea.Cmd {
	st := &m.askpass
	if st.open == nil || st.sent || m.ProcessingRemoteKeys || m.pressingTapeKeys || time.Since(st.openedAt) < askpassSettle {
		// Keys a script or a pane sent through the client are not the
		// person's, and type nothing.
		return nil
	}
	switch msg.String() {
	case "enter":
		if len(st.buf) == 0 {
			return nil
		}
		return m.askpassSend(false)
	case "esc":
		return m.askpassSend(true)
	case "backspace", "ctrl+h":
		if len(st.buf) > 0 {
			_, n := utf8.DecodeLastRune(st.buf)
			clear(st.buf[len(st.buf)-n:])
			st.buf = st.buf[:len(st.buf)-n]
		}
		return nil
	case "ctrl+u", "ctrl+w":
		st.drop()
		return nil
	}
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModSuper) == 0 {
		m.askpassType(msg.Text)
	}
	return nil
}

// AskpassPaste puts pasted text in the field: its first line, as a password
// manager pastes it.
func (m *OS) AskpassPaste(text string) {
	st := &m.askpass
	if st.open == nil || st.sent || m.ProcessingRemoteKeys || m.pressingTapeKeys {
		return
	}
	text, _, _ = strings.Cut(text, "\n")
	m.askpassType(strings.TrimRight(text, "\r"))
}

// askpassType appends printable text to the field.
func (m *OS) askpassType(text string) {
	st := &m.askpass
	for _, r := range text {
		if r < 0x20 || r == 0x7f || len(st.buf)+utf8.RuneLen(r) > askpassMaxField {
			continue
		}
		st.buf = utf8.AppendRune(st.buf, r)
	}
}

// askpassSend sends the answer, or the cancel, with this client's attach
// nonce, and zeroes the field.
func (m *OS) askpassSend(cancel bool) tea.Cmd {
	st := &m.askpass
	it := *st.open
	nonce := m.inboxNonce()
	if nonce == "" {
		m.ShowNotification(m.noNonceText(), "error", m.Settings.NotificationDuration*2)
		return nil
	}
	st.sent = true
	params := map[string]any{"request_id": it.RequestID, "human_nonce": nonce}
	var secret []byte
	if cancel {
		params["cancel"] = true
	} else {
		// A copy the call owns: the field is zeroed now, and the copy once
		// the call is encoded.
		secret = append([]byte(nil), st.buf...)
		params["secret"] = secret
	}
	st.drop()
	call := m.inboxActCaller()
	name := inboxWho(it)
	return func() tea.Msg {
		raw, err := call("askpass-answer", params, 5*time.Second)
		clear(secret)
		if err != nil {
			return AskpassAnsweredMsg{Name: name, Err: err}
		}
		var res struct {
			Applied bool   `json:"applied"`
			Reason  string `json:"reason"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			return AskpassAnsweredMsg{Name: name, Err: err}
		}
		return AskpassAnsweredMsg{Name: name, Applied: res.Applied, Reason: res.Reason}
	}
}

// AskpassAnsweredMsg says what became of an answer to a secret prompt.
type AskpassAnsweredMsg struct {
	Name    string
	Applied bool
	Reason  string
	Err     error
}

// applyAskpassAnswered tells the person what became of the answer. The
// dialog itself closes when the prompt leaves the mirror.
func (m *OS) applyAskpassAnswered(msg AskpassAnsweredMsg) {
	switch {
	case msg.Err != nil:
		m.askpass.sent = false
		m.ShowNotification("The answer did not go through: "+inboxActErr(msg.Err, m.AttachedHost)+". Type it again", "error", m.Settings.NotificationDuration*2)
	case !msg.Applied:
		m.ShowNotification(msg.Name+": the prompt ended before the answer ("+strings.ReplaceAll(msg.Reason, "_", " ")+")", "warning", m.Settings.NotificationDuration)
	case msg.Reason == session.AskpassCancelled:
		m.ShowNotification(msg.Name+": prompt cancelled", "info", m.Settings.NotificationDuration)
	default:
		m.ShowNotification(msg.Name+": answer sent", "success", m.Settings.NotificationDuration)
	}
}

// askpassFieldLabel names what the field takes.
func askpassFieldLabel(kind string) string {
	switch kind {
	case "passphrase":
		return "Passphrase:"
	case "password":
		return "Password:"
	case "pin":
		return "PIN:"
	}
	return "Answer:"
}

// renderAskpass draws the dialog. The prompt and the program are never cut:
// they wrap, since they are what the person decides on.
func (m *OS) renderAskpass() (string, overlay.Geometry) {
	st := &m.askpass
	it := st.open
	sec := it.Secret
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(askpassWidth, m.modalRoomWidth())
	inner := max(width-2, 1)

	var body []string
	text := func(s string, ink color.Color) {
		for _, l := range wrapPlain(s, inner) {
			body = append(body, dialogBodyLine(l, width, ink, bg))
		}
	}
	gap := func() { body = append(body, dialogBodyLine("", width, pal.FgDim, bg)) }
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(printableTitle(sec.Title), inner)), width, bg))
	gap()
	who := inboxWho(*it)
	if it.Harness != "" {
		who += " (" + printableTitle(it.Harness) + ")"
	}
	text(who+" runs a program that asks:", pal.FgDim)
	for line := range strings.SplitSeq(sec.Prompt, "\n") {
		text("  "+printableTitle(line), pal.Fg)
	}
	gap()
	switch {
	case sec.Program == "":
		text("tuios cannot tell which program asks. Check before you answer.", pal.Warn)
	case sec.Known:
		text("Program: "+printableTitle(sec.Program), pal.FgDim)
	default:
		text("Program: "+printableTitle(sec.Program), pal.Fg)
		text("tuios does not know this program. Check before you answer.", pal.Warn)
	}
	gap()
	text(askpassFieldLabel(sec.Kind), pal.FgDim)
	// The field is a band of the surface colour across the dialog, so it
	// reads as a field when it is empty.
	fieldW := max(inner-1, 3)
	dots := fieldTail(strings.Repeat(askpassDot, utf8.RuneCount(st.buf)), fieldW-2)
	cursor := overlay.Style(pal.Fg).Render(" ")
	rest := max(fieldW-2-utf8.RuneCountInString(dots), 0)
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(pal.Surface).Foreground(pal.Fg).Render(" "+dots)+cursor+
		overlay.Style(pal.Surface).Render(strings.Repeat(" ", rest)), width, bg))
	note := "The answer goes to the program only. tuios does not keep it."
	if st.sent {
		note = "Sending your answer..."
	}
	gap()
	body = append(body, dialogBodyLine(note, width, pal.FgMute, bg))
	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "cancel"}}
	content, geo := overlay.Dialog{Title: "secret", Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
	return content, geo
}
