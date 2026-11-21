package agentproto

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyBackspace
	keyCtrlC
	keyCtrlD
	keyCtrlU
	keyPaste
)

type key struct {
	kind keyKind
	r    rune
	text string
}

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

const maxPaste = 1 << 20

type keyParser struct {
	state   int
	seq     []byte
	pending []byte
	paste   bytes.Buffer
	tail    []byte
}

const (
	stGround = iota
	stEsc
	stCSI
	stSS3
	stPaste
)

func (p *keyParser) feed(b []byte) []key {
	var out []key
	for _, c := range b {
		switch p.state {
		case stPaste:
			if p.paste.Len() < maxPaste+len(pasteEnd) {
				p.paste.WriteByte(c)
			}
			p.tail = append(p.tail, c)
			if len(p.tail) > len(pasteEnd) {
				p.tail = p.tail[1:]
			}
			if string(p.tail) == pasteEnd {
				text := p.paste.Bytes()
				if bytes.HasSuffix(text, []byte(pasteEnd)) {
					text = text[:len(text)-len(pasteEnd)]
				}
				if len(text) > maxPaste {
					text = text[:maxPaste]
				}
				p.tail = p.tail[:0]
				s := strings.ReplaceAll(strings.ToValidUTF8(string(text), ""), "\r\n", "\n")
				s = strings.ReplaceAll(s, "\r", "\n")
				out = append(out, key{kind: keyPaste, text: s})
				p.paste.Reset()
				p.state = stGround
			}
		case stEsc:
			switch c {
			case '[':
				p.state, p.seq = stCSI, append(p.seq[:0], "\x1b["...)
			case 'O':
				p.state = stSS3
			default:
				p.state = stGround
			}
		case stCSI:
			p.seq = append(p.seq, c)
			if c >= 0x40 && c <= 0x7e {
				if string(p.seq) == pasteStart {
					p.state = stPaste
					p.paste.Reset()
					p.tail = p.tail[:0]
				} else {
					p.state = stGround
				}
				p.seq = p.seq[:0]
			} else if len(p.seq) > 32 {
				p.state, p.seq = stGround, p.seq[:0]
			}
		case stSS3:
			p.state = stGround
		default:
			out = p.ground(c, out)
		}
	}
	return out
}

func (p *keyParser) ground(c byte, out []key) []key {
	if len(p.pending) > 0 || c >= 0x80 {
		p.pending = append(p.pending, c)
		if !utf8.FullRune(p.pending) {
			if len(p.pending) >= utf8.UTFMax {
				p.pending = p.pending[:0]
			}
			return out
		}
		r, _ := utf8.DecodeRune(p.pending)
		p.pending = p.pending[:0]
		if r != utf8.RuneError && typedRune(r) {
			out = append(out, key{kind: keyRune, r: r})
		}
		return out
	}
	switch c {
	case 0x1b:
		p.state = stEsc
	case '\r', '\n':
		out = append(out, key{kind: keyEnter})
	case 0x7f, 0x08:
		out = append(out, key{kind: keyBackspace})
	case 0x03:
		out = append(out, key{kind: keyCtrlC})
	case 0x04:
		out = append(out, key{kind: keyCtrlD})
	case 0x15:
		out = append(out, key{kind: keyCtrlU})
	case '\t':
		out = append(out, key{kind: keyRune, r: ' '})
	default:
		if c >= 0x20 {
			out = append(out, key{kind: keyRune, r: rune(c)})
		}
	}
	return out
}

func typedRune(r rune) bool {
	switch {
	case isControl(r):
		return false
	case r == 0x200d, r >= 0xfe00 && r <= 0xfe0f:
		return true
	}
	return !invisible.Rune(r)
}
