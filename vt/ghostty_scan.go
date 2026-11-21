package vt

import (
	"unicode"
	"unicode/utf8"
)

type ghosttyScanHooks struct {
	Forward  func(p []byte)
	KittyAPC func(payload []byte)
	SixelDCS func(params, payload []byte)
	OSC      func(number int, payload []byte) (forward bool)
	CSI      func(prefix, inter, final byte, params []byte)
	ESC      func(inter, final byte)
	Ctrl     func(b byte)
}

type ghosttyScanState int

type csiState uint8

const (
	csiEntry csiState = iota
	csiParam
	csiInter
	csiIgnore
)

const (
	gsGround ghosttyScanState = iota
	gsEsc
	gsEscInter
	gsCsi
	gsOsc
	gsOscEsc
	gsDcs
	gsDcsBody
	gsDcsBodyEsc
	gsSixel
	gsSixelEsc
	gsApc
	gsApcEsc
	gsString
	gsStringEsc
)

const ghosttyScanCap = 4 * 1024 * 1024

type ghosttyScanner struct {
	hooks ghosttyScanHooks

	state               ghosttyScanState
	out                 []byte
	seq                 []byte
	overflow            bool
	escInter            byte
	escInters           int
	escSent             bool
	csiPrefix, csiInter byte
	csiState            csiState
	stringKind          byte
	dcsParams           []byte
	oscBEL              bool

	lastPrint rune
	u8        [4]byte
	u8n       int
	u8want    int

	afterCSI func()
}

func (s *ghosttyScanner) notePrint(b byte) {
	switch {
	case b&0xc0 == 0x80:
		if s.u8n == 0 || s.u8n >= s.u8want {
			s.u8n = 0
			s.lastPrint = utf8.RuneError
			return
		}
		s.u8[s.u8n] = b
		s.u8n++
		if s.u8n == s.u8want {
			r, size := utf8.DecodeRune(s.u8[:s.u8n])
			s.u8n = 0
			switch {
			case r == utf8.RuneError && size <= 1:
				s.lastPrint = utf8.RuneError
			case r >= 0x80 && r <= 0x9f, joinsPrevious(r):
			default:
				s.lastPrint = r
			}
		}
	default:
		s.u8[0] = b
		s.u8n = 1
		switch {
		case b&0xe0 == 0xc0:
			s.u8want = 2
		case b&0xf0 == 0xe0:
			s.u8want = 3
		case b&0xf8 == 0xf0:
			s.u8want = 4
		default:
			s.u8n = 0
			s.lastPrint = utf8.RuneError
		}
	}
}

func joinsPrevious(r rune) bool {
	switch {
	case r == 0x200d, r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef:
		return true
	case r < 0x300:
		return false
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf)
}

func newGhosttyScanner(hooks ghosttyScanHooks) *ghosttyScanner {
	return &ghosttyScanner{hooks: hooks}
}

func (s *ghosttyScanner) emit(b byte)         { s.out = append(s.out, b) }
func (s *ghosttyScanner) emitBytes(p ...byte) { s.out = append(s.out, p...) }

func (s *ghosttyScanner) sendEsc() {
	if !s.escSent {
		s.emit(0x1b)
		s.escSent = true
	}
}

func (s *ghosttyScanner) cancelSentEsc() {
	if s.escSent {
		s.emit(0x18)
		s.escSent = false
	}
}

func (s *ghosttyScanner) enterCsi() {
	s.state = gsCsi
	s.resetSeq()
	s.csiPrefix, s.csiInter = 0, 0
	s.csiState = csiEntry
}

func (s *ghosttyScanner) highInSequence(b byte) {
	switch {
	case b >= 0xa0:
		s.emit(b)
	case b == 0x9b:
		s.emit(b)
		s.enterCsi()
	case b == 0x90:
		s.emit(0x18)
		s.dcsParams = s.dcsParams[:0]
		s.state = gsDcs
	case b == 0x9d:
		s.emit(0x18)
		s.resetSeq()
		s.state = gsOsc
	case b == 0x9f:
		s.emit(0x18)
		s.resetSeq()
		s.state = gsApc
	case b == 0x98, b == 0x9e:
		s.emit(0x18)
		s.stringKind = 'X'
		if b == 0x9e {
			s.stringKind = '^'
		}
		s.state = gsString
	case b == 0x9c:
		s.emit(b)
		s.state = gsGround
	default:
		if s.hooks.ESC != nil {
			s.hooks.ESC(0, b-0x40)
		}
		s.emit(b)
		s.state = gsGround
	}
}

func (s *ghosttyScanner) flushOut() {
	if len(s.out) > 0 && s.hooks.Forward != nil {
		s.hooks.Forward(s.out)
	}
	s.out = s.out[:0]
}

func (s *ghosttyScanner) buffer(b byte) {
	if len(s.seq) >= ghosttyScanCap {
		s.overflow = true
		return
	}
	s.seq = append(s.seq, b)
}

func (s *ghosttyScanner) resetSeq() {
	s.seq = s.seq[:0]
	s.overflow = false
}

func (s *ghosttyScanner) Scan(p []byte) {
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch s.state {
		case gsGround:
			if b >= 0x20 && b < 0x7f && s.u8n == 0 {
				j := i + 1
				for j < len(p) && p[j] >= 0x20 && p[j] < 0x7f {
					j++
				}
				if j == i+1 {
					s.out = append(s.out, b)
				} else {
					s.out = append(s.out, p[i:j]...)
				}
				s.lastPrint = rune(p[j-1])
				i = j - 1
				continue
			}
			cut := s.u8n > 0 && s.u8n < s.u8want && b&0xc0 != 0x80
			if cut {
				s.u8n = 0
				s.lastPrint = utf8.RuneError
			}
			switch b {
			case 0x1b:
				s.state = gsEsc
				s.escSent = false
				if cut {
					s.sendEsc()
				}
			default:
				switch {
				case b < 0x20:
					if s.hooks.Ctrl != nil {
						s.hooks.Ctrl(b)
					}
				case b <= 0x7f:
					s.lastPrint = rune(b)
				case b >= 0x80:
					s.notePrint(b)
				}
				s.emit(b)
			}
		case gsEsc:
			switch {
			case b == '[':
				s.sendEsc()
				s.emit('[')
				s.enterCsi()
			case b == ']':
				s.cancelSentEsc()
				s.state = gsOsc
				s.resetSeq()
			case b == 'P':
				s.cancelSentEsc()
				s.state = gsDcs
				s.dcsParams = s.dcsParams[:0]
			case b == '_':
				s.cancelSentEsc()
				s.state = gsApc
				s.resetSeq()
			case b == 'X', b == '^':
				s.cancelSentEsc()
				s.stringKind = b
				s.state = gsString
			case b >= 0x20 && b <= 0x2f:
				s.sendEsc()
				s.emit(b)
				s.escInter, s.escInters = b, 1
				s.state = gsEscInter
			case b >= 0x30 && b <= 0x7e:
				if s.hooks.ESC != nil {
					s.hooks.ESC(0, b)
				}
				s.sendEsc()
				s.emit(b)
				s.state = gsGround
			case b == 0x1b:
				s.sendEsc()
				s.emit(0x1b)
			case b == 0x18, b == 0x1a:
				s.sendEsc()
				s.emit(b)
				s.state = gsGround
			case b < 0x20:
				s.sendEsc()
				s.emit(b)
				if s.hooks.Ctrl != nil {
					s.hooks.Ctrl(b)
				}
			case b == 0x7f:
				s.sendEsc()
				s.emit(b)
			default:
				s.sendEsc()
				s.highInSequence(b)
			}
		case gsEscInter:
			switch {
			case b >= 0x30 && b <= 0x7e:
				if s.hooks.ESC != nil && s.escInters == 1 {
					s.hooks.ESC(s.escInter, b)
				}
				s.emit(b)
				s.state = gsGround
			case b >= 0x20 && b <= 0x2f:
				s.emit(b)
				s.escInter = b
				s.escInters++
			case b == 0x1b:
				s.state = gsEsc
				s.escSent = false
				s.sendEsc()
			case b == 0x18, b == 0x1a:
				s.emit(b)
				s.state = gsGround
			case b < 0x20:
				s.emit(b)
				if s.hooks.Ctrl != nil {
					s.hooks.Ctrl(b)
				}
			case b == 0x7f:
				s.emit(b)
			default:
				s.highInSequence(b)
			}
		case gsCsi:
			if s.csiState == csiParam && b >= '0' && b <= ';' {
				s.emit(b)
				s.buffer(b)
				continue
			}
			switch {
			case b >= 0x30 && b <= 0x3f:
				s.emit(b)
				switch s.csiState {
				case csiEntry:
					switch {
					case b == ':':
						s.csiState = csiIgnore
					case b >= 0x3c:
						s.csiPrefix = b
						s.csiState = csiParam
					default:
						s.buffer(b)
						s.csiState = csiParam
					}
				case csiParam:
					if b <= ';' {
						s.buffer(b)
					} else {
						s.csiState = csiIgnore
					}
				case csiInter:
					s.csiState = csiIgnore
				}
			case b >= 0x20 && b <= 0x2f:
				s.emit(b)
				if s.csiState != csiIgnore {
					s.csiInter = b
					s.csiState = csiInter
				}
			case b >= 0x40 && b <= 0x7e:
				if s.hooks.CSI != nil && !s.overflow && s.csiState != csiIgnore {
					s.hooks.CSI(s.csiPrefix, s.csiInter, b, s.seq)
				}
				s.emit(b)
				s.resetSeq()
				s.state = gsGround
				if f := s.afterCSI; f != nil {
					s.afterCSI = nil
					s.flushOut()
					f()
				}
			case b == 0x1b:
				s.resetSeq()
				s.state = gsEsc
				s.escSent = false
				s.sendEsc()
			case b == 0x18, b == 0x1a:
				s.emit(b)
				s.resetSeq()
				s.state = gsGround
			case b < 0x20:
				s.emit(b)
				if s.hooks.Ctrl != nil {
					s.hooks.Ctrl(b)
				}
			case b == 0x7f:
				s.emit(b)
			default:
				s.resetSeq()
				s.highInSequence(b)
			}
		case gsOsc:
			switch b {
			case 0x07:
				s.endOsc(b)
			case 0x1b:
				s.state = gsOscEsc
			case 0x18, 0x1a:
				s.resetSeq()
				s.emit(b)
				s.state = gsGround
			default:
				if b < 0x20 {
					continue
				}
				s.buffer(b)
			}
		case gsOscEsc:
			if b == '\\' {
				s.endOsc('\\')
			} else if oscNumber(s.seq) == 7501 {
				s.endOsc('\\')
				s.state = gsEsc
				s.escSent = false
				i--
			} else {
				s.resetSeq()
				s.state = gsEsc
				s.escSent = false
				i--
			}
		case gsDcs:
			switch {
			case b >= 0x30 && b <= 0x3f || b >= 0x20 && b <= 0x2f:
				s.dcsParams = append(s.dcsParams, b)
				if len(s.dcsParams) > 256 {
					s.emitHeldDcs()
					s.state = gsDcsBody
				}
			case b >= 0x40 && b <= 0x7e:
				if b == 'q' && !hasIntermediates(s.dcsParams) && s.hooks.SixelDCS != nil {
					s.resetSeq()
					s.state = gsSixel
				} else {
					s.emitHeldDcs()
					s.emit(b)
					s.state = gsDcsBody
				}
			case b == 0x1b:
				s.dcsParams = s.dcsParams[:0]
				s.state = gsEsc
				s.escSent = false
			case b == 0x18, b == 0x1a:
				s.dcsParams = s.dcsParams[:0]
				s.emit(b)
				s.state = gsGround
			case b >= 0xa0:
			case b >= 0x80:
				s.emitHeldDcs()
				s.highInSequence(b)
			default:
			}
		case gsDcsBody:
			switch b {
			case 0x1b:
				s.state = gsDcsBodyEsc
			case 0x18, 0x1a:
				s.emit(b)
				s.state = gsGround
			default:
				s.emit(b)
			}
		case gsDcsBodyEsc:
			if b == '\\' {
				s.emitBytes(0x1b, '\\')
				s.state = gsGround
			} else {
				s.emit(0x1b)
				s.state = gsEsc
				s.escSent = true
				i--
			}
		case gsSixel:
			switch b {
			case 0x1b:
				s.state = gsSixelEsc
			case 0x18, 0x1a:
				s.resetSeq()
				s.dcsParams = s.dcsParams[:0]
				s.emit(b)
				s.state = gsGround
			default:
				s.buffer(b)
			}
		case gsSixelEsc:
			if b == '\\' {
				s.endSixel()
			} else {
				s.resetSeq()
				s.dcsParams = s.dcsParams[:0]
				s.state = gsEsc
				s.escSent = false
				i--
			}
		case gsApc:
			switch b {
			case 0x1b:
				s.state = gsApcEsc
			case 0x18, 0x1a:
				s.resetSeq()
				s.emit(b)
				s.state = gsGround
			default:
				s.buffer(b)
			}
		case gsApcEsc:
			if b == '\\' {
				s.endApc('\\')
			} else {
				s.resetSeq()
				s.state = gsEsc
				s.escSent = false
				i--
			}
		case gsString:
			switch b {
			case 0x1b:
				s.state = gsStringEsc
			case 0x18, 0x1a:
				s.emit(b)
				s.state = gsGround
			}
		case gsStringEsc:
			if b == '\\' {
				s.state = gsGround
			} else {
				s.state = gsEsc
				s.escSent = false
				i--
			}
		}
	}
	s.flushOut()
}

func (s *ghosttyScanner) endOsc(term byte) {
	forward := true
	s.oscBEL = term == 0x07
	if s.hooks.OSC != nil && !s.overflow {
		s.flushOut()
		forward = s.hooks.OSC(oscNumber(s.seq), s.seq)
	}
	if forward {
		s.emitBytes(0x1b, ']')
		s.out = append(s.out, s.seq...)
		if term == '\\' {
			s.emitBytes(0x1b, '\\')
		} else {
			s.emit(term)
		}
	}
	s.resetSeq()
	s.state = gsGround
}

func (s *ghosttyScanner) endSixel() {
	if s.hooks.SixelDCS != nil && !s.overflow {
		s.flushOut()
		s.hooks.SixelDCS(s.dcsParams, s.seq)
	}
	s.resetSeq()
	s.dcsParams = s.dcsParams[:0]
	s.state = gsGround
}

func (s *ghosttyScanner) endApc(term byte) {
	if len(s.seq) > 0 && s.seq[0] == 'G' && s.hooks.KittyAPC != nil {
		if !s.overflow {
			s.flushOut()
			s.hooks.KittyAPC(s.seq)
		}
	}
	s.resetSeq()
	s.state = gsGround
}

func (s *ghosttyScanner) emitHeldDcs() {
	s.emitBytes(0x1b, 'P')
	s.out = append(s.out, s.dcsParams...)
	s.dcsParams = s.dcsParams[:0]
}

func hasIntermediates(params []byte) bool {
	for _, b := range params {
		if b >= 0x20 && b <= 0x2f {
			return true
		}
	}
	return false
}

func oscNumber(payload []byte) int {
	n := -1
	for _, b := range payload {
		if b < '0' || b > '9' {
			break
		}
		if n < 0 {
			n = 0
		}
		n = n*10 + int(b-'0')
		if n > maxOSCNumber {
			return -1
		}
	}
	return n
}
