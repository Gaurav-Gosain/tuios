package vt

// ghosttyScanner deliberately ignores 8-bit C1 introducers and terminators
// (0x90, 0x9c, 0x9d, 0x9f): those bytes occur inside UTF-8 continuations, and
// a UTF-8 terminal that honored them would tear multibyte characters apart.
// Only ESC-prefixed forms and BEL are recognized, which matches the sink.
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

const (
	gsGround     ghosttyScanState = iota
	gsEsc                         // after ESC (not yet emitted)
	gsEscInter                    // after ESC + intermediate (not yet emitted)
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
	gsString                      // inside SOS/PM (emitted opaquely)
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
	// escInter is the pending intermediate of a two-byte ESC dispatch.
	escInter byte
	// csiPrefix and csiInter are the marker bytes of the CSI in progress.
	csiPrefix, csiInter byte
	// dcsParams holds DCS parameter and intermediate bytes until the final
	// byte decides whether the body is sixel.
	dcsParams []byte
}

func newGhosttyScanner(hooks ghosttyScanHooks) *ghosttyScanner {
	return &ghosttyScanner{hooks: hooks}
}

func (s *ghosttyScanner) emit(b byte)         { s.out = append(s.out, b) }
func (s *ghosttyScanner) emitBytes(p ...byte) { s.out = append(s.out, p...) }

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
			switch b {
			case 0x1b:
				s.state = gsEsc
			default:
				if b < 0x20 && s.hooks.Ctrl != nil {
					s.hooks.Ctrl(b)
				}
				s.emit(b)
			}
		case gsEsc:
			switch {
			case b == '[':
				s.emitBytes(0x1b, '[')
				s.state = gsCsi
				s.resetSeq()
				s.csiPrefix, s.csiInter = 0, 0
			case b == ']':
				s.state = gsOsc
				s.resetSeq()
			case b == 'P':
				s.state = gsDcs
				s.dcsParams = s.dcsParams[:0]
			case b == '_':
				s.state = gsApc
				s.resetSeq()
			case b == 'X', b == '^':
				s.emitBytes(0x1b, b)
				s.state = gsString
			case b >= 0x20 && b <= 0x2f:
				s.escInter = b
				s.state = gsEscInter
			case b >= 0x30 && b <= 0x7e:
				if s.hooks.ESC != nil {
					s.hooks.ESC(0, b)
				}
				s.emitBytes(0x1b, b)
				s.state = gsGround
			case b == 0x1b:
				// ESC ESC: the first ESC dissolves.
				s.emit(0x1b)
			default:
				// A control aborting the escape: both flow through.
				s.emitBytes(0x1b, b)
				s.state = gsGround
			}
		case gsEscInter:
			if b >= 0x30 && b <= 0x7e {
				if s.hooks.ESC != nil {
					s.hooks.ESC(s.escInter, b)
				}
				s.emitBytes(0x1b, s.escInter, b)
			} else {
				// Aborted or deeper intermediates; emit what was held and
				// let the sink's parser decide.
				s.emitBytes(0x1b, s.escInter, b)
			}
			s.state = gsGround
		case gsCsi:
			switch {
			case b >= '0' && b <= '9' || b == ';' || b == ':':
				s.emit(b)
				s.buffer(b)
			case b >= 0x3c && b <= 0x3f:
				s.emit(b)
				s.csiPrefix = b
			case b >= 0x20 && b <= 0x2f:
				s.emit(b)
				s.csiInter = b
			case b >= 0x40 && b <= 0x7e:
				// The hook runs before the final byte is emitted, so a
				// hook that flushes observes the sink exactly as it stood
				// before this sequence applies: ED 3 reads the pre-clear
				// history length, an alt-screen switch reads the main
				// screen's, and only then does the sequence take effect.
				if s.hooks.CSI != nil && !s.overflow {
					s.hooks.CSI(s.csiPrefix, s.csiInter, b, s.seq)
				}
				s.emit(b)
				s.resetSeq()
				s.state = gsGround
			case b == 0x1b:
				// Aborted CSI; both sides see the same malformed stream.
				s.resetSeq()
				s.state = gsEsc
			case b < 0x20:
				s.emit(b)
				if s.hooks.Ctrl != nil {
					s.hooks.Ctrl(b)
				}
			default:
				s.emit(b)
			}
		case gsOsc:
			switch b {
			case 0x07:
				s.endOsc(b)
			case 0x1b:
				s.state = gsOscEsc
			default:
				s.buffer(b)
			}
		case gsOscEsc:
			if b == '\\' {
				s.endOsc('\\')
			} else {
				// ESC aborts the string and starts a new sequence. The
				// withheld payload is dropped on both sides: the sink never
				// saw the introducer.
				s.resetSeq()
				s.state = gsEsc
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
			default:
				// Ignore controls inside the prefix, as the sink would.
			}
		case gsDcsBody:
			if b == 0x1b {
				s.state = gsDcsBodyEsc
			} else {
				s.emit(b)
			}
		case gsDcsBodyEsc:
			if b == '\\' {
				s.emitBytes(0x1b, '\\')
				s.state = gsGround
			} else {
				s.emit(0x1b)
				s.state = gsEsc
				i--
			}
		case gsSixel:
			if b == 0x1b {
				s.state = gsSixelEsc
			} else {
				s.buffer(b)
			}
		case gsSixelEsc:
			if b == '\\' {
				s.endSixel()
			} else {
				s.resetSeq()
				s.dcsParams = s.dcsParams[:0]
				s.state = gsEsc
				i--
			}
		case gsApc:
			if b == 0x1b {
				s.state = gsApcEsc
			} else {
				s.buffer(b)
			}
		case gsApcEsc:
			if b == '\\' {
				s.endApc('\\')
			} else {
				s.resetSeq()
				s.state = gsEsc
				i--
			}
		case gsString:
			if b == 0x1b {
				s.state = gsStringEsc
			} else {
				s.emit(b)
			}
		case gsStringEsc:
			if b == '\\' {
				s.emitBytes(0x1b, '\\')
				s.state = gsGround
			} else {
				s.emit(0x1b)
				s.state = gsEsc
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
	} else {
		s.emitBytes(0x1b, '_')
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
		if n > 1<<20 {
			return -1
		}
	}
	return n
}
