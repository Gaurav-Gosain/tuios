package app

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// In-band alerts: bytes written into the same stream the frame is rendered
// through, so they arrive wherever the frame arrives.
//
// This is the only notification mechanism that is correct in every mode tuios
// supports. `tuios ssh` runs the whole TUI on the remote host and renders
// through the ssh.Session, so a desktop notification raised by this process
// would pop on a machine nobody is sitting at. An escape sequence travels the
// pipe to whatever terminal is actually in front of the user. The cost is that
// it cannot be clicked; the dock message carries the click.

// notifyTextLimit caps a notification payload. A harness message is free text
// and a terminal has to buffer whatever it is handed; no notification daemon
// shows more than a couple of lines anyway.
const notifyTextLimit = 200

// sanitizeNotifyText makes a string safe to carry inside an OSC payload. The
// three deletions are the whole injection guard: without them a pane title
// containing ESC could terminate the sequence and have the rest of it
// interpreted as commands by the user's terminal.
func sanitizeNotifyText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 0x1b, 0x07, 0x9c: // ESC, BEL, ST: would end or restart the sequence
			continue
		case '\n', '\r', '\t':
			b.WriteRune(' ')
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > notifyTextLimit {
		// Back off to a rune boundary so a cut multi-byte character does not
		// reach the terminal as a lone continuation byte.
		out = out[:notifyTextLimit]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
		out = strings.TrimSpace(out)
	}
	return out
}

// hostNotifySequence builds the in-band notification for text, wrapping it for
// tmux when tuios is running inside one. Empty text yields no bytes.
func hostNotifySequence(text string, insideTmux bool) []byte {
	text = sanitizeNotifyText(text)
	if text == "" {
		return nil
	}
	seq := []byte(ansi.Notify(text))
	if insideTmux {
		seq = wrapTmuxPassthrough(seq)
	}
	return seq
}

// wrapTmuxPassthrough wraps a sequence so tmux forwards it to the terminal
// outside instead of swallowing it. Every inner ESC is doubled, which is what
// tmux's DCS passthrough parser expects. It requires `allow-passthrough on` in
// the user's tmux config; without it tmux drops the sequence, which is the same
// outcome as not wrapping and so costs nothing to attempt.
func wrapTmuxPassthrough(seq []byte) []byte {
	out := make([]byte, 0, len(seq)+16)
	out = append(out, "\x1bPtmux;"...)
	for _, b := range seq {
		if b == 0x1b {
			out = append(out, 0x1b)
		}
		out = append(out, b)
	}
	return append(out, 0x1b, '\\')
}

// insideTmux reports whether this process is running inside tmux.
//
// $TMUX is the direct answer and is what tmux sets for its own children. TERM
// is the answer for the case that matters most here: with `tuios ssh` the TUI
// runs on the remote host, where $TMUX is unset even though the user's local
// terminal is inside tmux, and the forwarded TERM is what carries that fact.
// GNU screen is deliberately not matched: its passthrough uses a different
// wrapping, and emitting tmux's form into screen would paint garbage.
func insideTmux() bool {
	if os.Getenv("TMUX") != "" {
		return true
	}
	return strings.HasPrefix(os.Getenv("TERM"), "tmux")
}

// writeHostSequence writes raw bytes to the terminal the client is attached to.
//
// It funnels through KittyPassthrough because that already owns the host output
// handle for every mode (the ssh.Session under `tuios ssh`, the sip PTY slave in
// web mode, /dev/tty or stdout locally) and serialises writes to it under a
// mutex, which is what keeps this from interleaving with a graphics frame. That
// is the same route the guest OSC 9 forwarding in notify.go already takes.
// PostRenderWriter is the fallback for a client built without the passthrough;
// it queues the bytes behind the next frame rather than writing immediately,
// which is late but not wrong for a notification.
func (m *OS) writeHostSequence(seq []byte) {
	if len(seq) == 0 {
		return
	}
	switch {
	case m.KittyPassthrough != nil:
		m.KittyPassthrough.WriteToHost(seq)
	case m.PostRenderWriter != nil:
		m.PostRenderWriter.QueuePostRender(seq)
	}
}
