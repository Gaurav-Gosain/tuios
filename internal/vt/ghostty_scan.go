package vt

import (
	"unicode"
	"unicode/utf8"
)

// In the ground state ghosttyScanner deliberately ignores 8-bit C1
// introducers and terminators (0x90, 0x9c, 0x9d, 0x9f): those bytes occur
// inside UTF-8 continuations, and a UTF-8 terminal that honored them would
// tear multibyte characters apart. There only ESC-prefixed forms and BEL are
// recognized, which matches the sink. Inside an escape sequence, a CSI or a
// DCS prefix the sink's parser does read them as 8-bit controls, and the
// scanner follows it there (highInSequence).
//
// ghosttyScanner splits a raw PTY byte stream for the libghostty-backed
// terminal. libghostty parses everything itself, but tuios owns a handful of
// sequence families the library either does not surface (kitty graphics APC,
// sixel DCS, OSC 66 text sizing, OSC 52 clipboard reads) or does not expose
// state for (DECSTBM values, charset designations, the kitty keyboard stack).
// The scanner walks the stream once, emits everything else to the sink in
// order, and reports the sequences tuios must observe. Sixel and kitty APC
// are withheld from the sink entirely: tuios's passthrough pipeline is their
// only consumer, and forwarding sixel would render it twice.
//
// It is a tokenizer, not a parser: it tracks only enough state to find
// sequence boundaries, so its per-byte cost stays far below a full emulator.
// State carries across calls, so chunk boundaries inside a sequence are safe.
//
// Every input byte is explicitly either emitted or withheld; the emit buffer
// is flushed to the Forward hook before any other hook runs, which keeps the
// sink's parser exactly in step with the intercept handlers.

// ghosttyScanHooks receives what the scanner finds. A nil hook forwards the
// family untouched. Hooks run in stream order relative to Forward.
type ghosttyScanHooks struct {
	// Forward receives every byte span destined for libghostty.
	Forward func(p []byte)
	// KittyAPC receives the payload of an APC starting with 'G' (between
	// ESC _ and ST). The sequence is not forwarded.
	KittyAPC func(payload []byte)
	// SixelDCS receives the parameter bytes and body of a sixel DCS (final
	// byte 'q', no intermediates). The sequence is not forwarded.
	SixelDCS func(params, payload []byte)
	// OSC receives every complete OSC payload (between the introducer and
	// terminator). Returning false withholds the sequence from the sink.
	OSC func(number int, payload []byte) (forward bool)
	// CSI observes every complete CSI: prefix is the private marker (0 if
	// none), inter the last intermediate (0 if none), final the final byte,
	// params the raw parameter bytes. Always forwarded.
	CSI func(prefix, inter, final byte, params []byte)
	// ESC observes two-byte and three-byte ESC dispatches: inter is the
	// intermediate byte (0 if none). Always forwarded.
	ESC func(inter, final byte)
	// Ctrl observes C0 controls outside withheld strings. Always forwarded.
	Ctrl func(b byte)
}

type ghosttyScanState int

// csiState follows the CSI states of the sink's parser: right after the
// introducer, among the parameters, among the intermediates, and in a CSI
// it will not dispatch.
type csiState uint8

const (
	csiEntry csiState = iota
	csiParam
	csiInter
	csiIgnore
)

const (
	gsGround     ghosttyScanState = iota
	gsEsc                         // after ESC (held while the sink is at ground; see escSent)
	gsEscInter                    // after ESC + intermediate (emitted)
	gsCsi                         // inside CSI (emitted as it goes)
	gsOsc                         // inside OSC payload (withheld)
	gsOscEsc                      // inside OSC, after ESC
	gsDcs                         // inside DCS before the final byte (withheld)
	gsDcsBody                     // inside non-sixel DCS body (emitted)
	gsDcsBodyEsc                  // non-sixel DCS body, after ESC
	gsSixel                       // inside sixel DCS body (withheld)
	gsSixelEsc                    // sixel body, after ESC
	gsApc                         // inside APC (withheld)
	gsApcEsc                      // inside APC, after ESC
	gsString                      // inside SOS/PM (withheld)
	gsStringEsc                   // SOS/PM body, after ESC
)

// ghosttyScanCap bounds every withheld sequence, matching the pure emulator's
// parser data budget. A sequence that exceeds it is dropped, not grown.
const ghosttyScanCap = 4 * 1024 * 1024

type ghosttyScanner struct {
	hooks ghosttyScanHooks

	state ghosttyScanState
	// out accumulates bytes for the sink within one Scan call.
	out []byte
	// seq accumulates the current withheld or observed sequence body.
	seq      []byte
	overflow bool
	// escInter is the last intermediate of an ESC sequence, and escInters
	// how many it has.
	escInter  byte
	escInters int
	// escSent says the sink already has the ESC that started the escape
	// sequence in progress. The scanner holds an ESC until the next byte
	// says whether the sequence is one it withholds, but only while the
	// sink is in its ground state. It hands the ESC over at once when the
	// sink is part way through a sequence the ESC ends, and early when the
	// sink has to see it first: a control inside the sequence is carried
	// out where it is, and an ESC that ends a UTF-8 character moves the
	// cursor past the U+FFFD the sink prints for it. Before a string it
	// withholds, the scanner takes the sink back to its ground state with
	// CAN (cancelSentEsc).
	escSent bool
	// csiPrefix and csiInter are the marker bytes of the CSI in progress,
	// and csiState where in the CSI the sink's parser is.
	csiPrefix, csiInter byte
	csiState            csiState
	// stringKind is the introducer of the SOS or PM in progress, X or ^.
	stringKind byte
	// dcsParams holds DCS parameter and intermediate bytes until the final
	// byte decides whether the body is sixel.
	dcsParams []byte
	// oscBEL says the OSC handed to the hook ended with BEL rather than ST,
	// so a reply can end the same way.
	oscBEL bool

	// lastPrint is the last character printed in the ground state, which is
	// what REP repeats: the library keeps it where no query reaches. A
	// character that takes no column of its own (a combining mark, a joiner,
	// a selector) joins the one before it and is not recorded, as the
	// library records it. Zero means nothing printed since a reset.
	lastPrint rune
	// u8 holds the bytes of a UTF-8 character still arriving, and u8want
	// how many it has in all.
	u8     [4]byte
	u8n    int
	u8want int

	// afterCSI, when a CSI hook sets it, runs once the CSI's final byte has
	// reached the sink, for a hook whose effect the sink has to see after
	// the sequence rather than before it.
	afterCSI func()
}

// notePrint records a ground-state byte of a UTF-8 character toward
// lastPrint.
func (s *ghosttyScanner) notePrint(b byte) {
	switch {
	case b&0xc0 == 0x80:
		if s.u8n == 0 || s.u8n >= s.u8want {
			// A continuation byte with no character to continue: the
			// sink prints U+FFFD for it.
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
				// Not a character: the sink prints U+FFFD.
				s.lastPrint = utf8.RuneError
			case r >= 0x80 && r <= 0x9f, joinsPrevious(r):
				// A C1 control sent as UTF-8 (U+0080 to U+009F) is a
				// control, not a character REP can repeat.
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
			// A byte that starts no character: the sink prints U+FFFD.
			s.u8n = 0
			s.lastPrint = utf8.RuneError
		}
	}
}

// joinsPrevious reports whether r takes no column of its own and joins the
// character before it.
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

// sendEsc hands the sink the ESC of the escape sequence in progress, unless
// it has it already.
func (s *ghosttyScanner) sendEsc() {
	if !s.escSent {
		s.emit(0x1b)
		s.escSent = true
	}
}

// cancelSentEsc takes the sink out of the escape state, where an ESC handed
// over early left it, before a string the scanner withholds. The sink reads
// CAN there as the end of the sequence and prints nothing. A string that is
// forwarded later comes with its own ESC.
func (s *ghosttyScanner) cancelSentEsc() {
	if s.escSent {
		s.emit(0x18)
		s.escSent = false
	}
}

// enterCsi starts a CSI whose introducer the sink has.
func (s *ghosttyScanner) enterCsi() {
	s.state = gsCsi
	s.resetSeq()
	s.csiPrefix, s.csiInter = 0, 0
	s.csiState = csiEntry
}

// highInSequence follows a byte with the top bit set inside an escape
// sequence, a CSI or a DCS prefix, after the sink has had the sequence so
// far. In the ground state the sink reads such a byte as part of a UTF-8
// character, and the scanner ignores the 8-bit controls there. Inside a
// sequence its parser reads them as 8-bit controls, so a byte of a UTF-8
// character can end the sequence or start another. A byte from 0xA0 is
// ignored, and the sequence goes on. ST ends the sequence. The other
// controls from 0x80 to 0x9A are carried out as ESC and the byte less 0x40,
// so 0x88 is HTS. CSI starts a CSI. DCS, OSC, APC, SOS and PM start a
// string the scanner withholds; CAN takes the sink to its ground state
// first.
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

// flushOut hands the pending emitted bytes to the sink. Called before any
// intercept hook so the sink has consumed everything preceding the sequence.
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

// Scan consumes one chunk. Forward spans and hook calls happen in stream
// order; the chunk may end mid-sequence.
func (s *ghosttyScanner) Scan(p []byte) {
	for i := 0; i < len(p); i++ {
		b := p[i]
		switch s.state {
		case gsGround:
			if b >= 0x20 && b < 0x7f && s.u8n == 0 {
				// A run of printable ASCII, which is nearly every byte a
				// pane prints, goes to the sink whole. Its last byte is
				// REP's character.
				j := i + 1
				for j < len(p) && p[j] >= 0x20 && p[j] < 0x7f {
					j++
				}
				if j == i+1 {
					// One character between two sequences, as in a
					// stream that colours each cell: no copy call.
					s.out = append(s.out, b)
				} else {
					s.out = append(s.out, p[i:j]...)
				}
				s.lastPrint = rune(p[j-1])
				i = j - 1
				continue
			}
			// A byte that cannot continue the character in progress ends
			// it, and the sink prints U+FFFD for it, which REP repeats.
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
					// A hook for this sequence that reads the cursor has
					// to read it after the U+FFFD.
					s.sendEsc()
				}
			default:
				switch {
				case b < 0x20:
					if s.hooks.Ctrl != nil {
						s.hooks.Ctrl(b)
					}
				case b <= 0x7f:
					// REP's character. The sink prints DEL as well.
					s.lastPrint = rune(b)
				case b >= 0x80:
					s.notePrint(b)
				}
				s.emit(b)
			}
		case gsEsc:
			// The escape state of the DEC parser the sink follows: a C0
			// control is carried out and the sequence goes on, an
			// intermediate goes on to the intermediate state, and a final
			// byte dispatches.
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
				// ESC ESC: the first ESC dissolves, and the second one
				// starts the sequence. The sink is out of its ground
				// state, so it has the second one at once (see escSent).
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
				// Ignored, and the sequence goes on.
				s.sendEsc()
				s.emit(b)
			default:
				s.sendEsc()
				s.highInSequence(b)
			}
		case gsEscInter:
			// The sink has the ESC and the intermediates already.
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
			// The CSI states of the sink's parser. A prefix counts only
			// right after the introducer, and a parameter byte after an
			// intermediate, a prefix after a parameter, or a colon first
			// make a CSI the parser reads to its end and does not dispatch.
			if s.csiState == csiParam && b >= '0' && b <= ';' {
				// A parameter, which is most of an SGR-heavy stream.
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
				// The hook runs before the final byte is emitted, so a
				// hook that flushes observes the sink exactly as it stood
				// before this sequence applies: ED 3 reads the pre-clear
				// history length, an alt-screen switch reads the main
				// screen's, and only then does the sequence take effect.
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
				// The ESC ends the CSI, and the sink has it at once (see
				// escSent).
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
				// CAN and SUB cancel the string, as in the DEC parser the
				// pure emulator and libghostty both follow: nothing is
				// dispatched, and what follows is ordinary text.
				s.resetSeq()
				s.emit(b)
				s.state = gsGround
			default:
				if b < 0x20 {
					// Any other C0 control inside an OSC string is ignored,
					// as in the DEC parser, so a hook reads the payload the
					// pure emulator reads.
					continue
				}
				s.buffer(b)
			}
		case gsOscEsc:
			if b == '\\' {
				s.endOsc('\\')
			} else if oscNumber(s.seq) == 7501 { // progstatus.Command
				// The pure emulator's parser ends the string at the ESC and
				// dispatches it, then reads the ESC as a new sequence. An
				// OSC 7501 report is read the same way here, so the two
				// backends agree on which reports apply. The OSC is never
				// forwarded, so nothing changes for libghostty.
				s.endOsc('\\')
				s.state = gsEsc
				s.escSent = false
				i--
			} else {
				// ESC aborts the string and starts a new sequence. The
				// withheld payload is dropped on both sides: the sink never
				// saw the introducer.
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
					// No real DCS prefix is this long; stop inspecting.
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
				// Aborted before the final byte: drop the held prefix on
				// both sides.
				s.dcsParams = s.dcsParams[:0]
				s.state = gsEsc
				s.escSent = false
			case b == 0x18, b == 0x1a:
				s.dcsParams = s.dcsParams[:0]
				s.emit(b)
				s.state = gsGround
			case b >= 0xa0:
				// The sink's parser ignores it here.
			case b >= 0x80:
				// An 8-bit control, which the sink's parser reads inside
				// the prefix. It needs the prefix first.
				s.emitHeldDcs()
				s.highInSequence(b)
			default:
				// Ignore controls inside the prefix, as the sink would.
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
				// The sink has the ESC: it ended the string there.
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
				// CAN and SUB cancel the string, and nothing is drawn.
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
			// The sink does nothing with an SOS or a PM, and its parser
			// would read an 8-bit control inside one, which a UTF-8
			// character can hold. The string is withheld whole.
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

// endOsc completes a withheld OSC. When the hook keeps it, the sequence is
// re-emitted verbatim with its original terminator.
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
	// Any other APC is withheld: the sink does nothing with it, and its
	// parser would read an 8-bit control inside one, which a UTF-8
	// character can hold.
	s.resetSeq()
	s.state = gsGround
}

// emitHeldDcs re-emits the DCS introducer and held prefix once the final byte
// shows the body is not sixel.
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

// oscNumber parses the leading decimal command number of an OSC payload,
// returning -1 when there is none.
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
