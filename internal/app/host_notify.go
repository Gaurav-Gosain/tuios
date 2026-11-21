package app

import (
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"github.com/charmbracelet/x/ansi"
)

const notifyTextLimit = 200

func sanitizeNotifyText(s string) string {
	out := notifyPlainText(s)
	if len(out) > notifyTextLimit {
		out = out[:notifyTextLimit]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
		out = strings.TrimSpace(out)
	}
	return guardNumericOSCPrefix(out)
}

func notifyPlainText(s string) string {
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
			continue
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

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

const screenStringLimit = 768

func hostNotifySequence(text string, outer outerMultiplexer) []byte {
	text = sanitizeNotifyText(text)
	if text == "" {
		return nil
	}
	seq := ansi.Notify(text)
	switch outer {
	case outerTmux:
		seq = ansi.TmuxPassthrough(seq)
	case outerScreen:
		seq = ansi.ScreenPassthrough(seq, screenStringLimit)
	}
	return []byte(seq)
}

type outerMultiplexer int

const (
	outerNone outerMultiplexer = iota
	outerTmux
	outerScreen
)

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
