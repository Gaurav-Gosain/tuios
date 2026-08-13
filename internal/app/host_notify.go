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
	return guardNumericOSCPrefix(out)
}

// guardNumericOSCPrefix keeps an OSC 9 payload from being read as a command.
//
// OSC 9 was extended by ConEmu with a dozen numbered subcommands (9;4 progress,
// 9;9 cwd, and so on) and terminals split on how much of that they honour:
// Ghostty intercepts all twelve, kitty and WezTerm only 4, and foot drops the
// payload outright when everything before the first semicolon parses as a
// number. A pane the user named "4" would otherwise turn its notification into a
// progress-bar command. One leading space costs nothing and settles it.
func guardNumericOSCPrefix(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && s[i] == ';' {
		return " " + s
	}
	return s
}

// screenStringLimit is the 768-byte cap GNU screen puts on a string sequence.
// Past it screen dumps the remainder onto the display as literal text, so the
// wrapper chunks rather than truncating.
const screenStringLimit = 768

// hostNotifySequence builds the in-band notification for text, wrapped for
// whatever multiplexer tuios is running inside. Empty text yields no bytes.
//
// OSC 9 rather than OSC 777 or OSC 99: it is the one sequence every terminal
// that does notifications at all accepts, and it is the one already vendored
// here and already used to forward a guest pane's notifications to the host.
func hostNotifySequence(text string, outer outerMultiplexer) []byte {
	text = sanitizeNotifyText(text)
	if text == "" {
		return nil
	}
	seq := ansi.Notify(text)
	switch outer {
	case outerTmux:
		// tmux forwards no OSC 9 of its own (it handles only the 9;4 progress
		// form and drops the rest), so under tmux the wrap is not an
		// optimisation, it is the only thing that gets the sequence out.
		seq = ansi.TmuxPassthrough(seq)
	case outerScreen:
		// screen's wrapping is not tmux's: it must NOT double the inner ESC, and
		// the inner sequence has to be BEL-terminated so an ST does not end the
		// passthrough early. ansi.Notify is BEL-terminated, which is what makes
		// it usable here.
		seq = ansi.ScreenPassthrough(seq, screenStringLimit)
	}
	return []byte(seq)
}

// outerMultiplexer names the multiplexer between tuios and the user's terminal.
type outerMultiplexer int

const (
	outerNone outerMultiplexer = iota
	outerTmux
	outerScreen
)

// detectOuterMultiplexer reports what tuios is running inside.
//
// $TMUX and $STY are the direct answers, set for their own children. TERM is
// the answer for the case that matters most here: with `tuios ssh` the TUI runs
// on the remote host, where neither variable is set even though the user's local
// terminal is inside a multiplexer, and the forwarded TERM is what carries that
// fact.
func detectOuterMultiplexer() outerMultiplexer {
	term := os.Getenv("TERM")
	switch {
	case os.Getenv("TMUX") != "", strings.HasPrefix(term, "tmux"):
		return outerTmux
	case os.Getenv("STY") != "", strings.HasPrefix(term, "screen"):
		return outerScreen
	}
	return outerNone
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
