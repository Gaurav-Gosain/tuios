package app

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
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
	out := notifyPlainText(s)
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

// notifyPlainText is s with every control character removed and line breaks
// and tabs turned into spaces, trimmed. It is the part of sanitizeNotifyText
// that applies to any text a pane hands in, before it reaches the dock or the
// host terminal.
//
// ESC, BEL and ST would end or restart the sequence the text is carried in.
// The other C1 controls go too, since a terminal in 8-bit mode reads 0x9b as
// CSI. A byte that is not valid UTF-8 is dropped rather than passed on.
func notifyPlainText(s string) string {
	// Invisible characters would let the text read as something other than
	// what it holds. invisible.Strip keeps a zero-width joiner between two
	// emoji, so a family or a profession emoji stays one picture.
	s = invisible.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			continue
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case r < 0x20, r >= 0x7f && r <= 0x9f:
			continue
		case r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0:
			// Hangul fillers draw as blank space.
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
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

// detectOuterMultiplexer reports what the client's terminal is running inside.
//
// Locally, $TMUX and $STY are the direct answers, set for their own children,
// and TERM backs them up. With `tuios ssh` the TUI runs on the remote host,
// where the server's own environment says nothing about the user's terminal:
// the TERM the client sent in its pty request is what carries the fact, and
// the server's $TMUX/$STY describe only where the server was started.
func (m *OS) detectOuterMultiplexer() outerMultiplexer {
	if m.IsSSHMode && m.SSHSession != nil {
		if term, ok := sshClientTerm(m.SSHSession); ok {
			switch {
			case strings.HasPrefix(term, "tmux"):
				return outerTmux
			case strings.HasPrefix(term, "screen"):
				return outerScreen
			}
		}
		return outerNone
	}
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
