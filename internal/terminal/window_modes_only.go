package terminal

import "sync/atomic"

// KeepModesOnly makes the pane's emulator see only what modesOnly passes, for
// a client that draws nothing of the pane itself. Call it before the pane's
// stream starts.
func (w *Window) KeepModesOnly() { w.modesOnly.Store(&modesOnly{}) }

// modesFilter is set once by KeepModesOnly and read by outputWriter.
type modesFilter = atomic.Pointer[modesOnly]

// modesOnly passes on only the parts of a pane's stream that a client which
// draws nothing still needs from its emulator: OSC sequences (the title, the
// working directory, notifications, the clipboard, the private OSCs), the
// private modes (DECSET and DECRST, for the alternate screen) and the bell.
// Text, SGR, cursor moves, other CSIs and DCS, APC, PM and SOS strings are
// dropped, so the emulator is not asked to lay out a screen nobody reads.
//
// tuios gui-bridge uses it. Its renderer parses every byte in an emulator of
// its own, and the bridge's copy was a second full parse: about as much CPU
// as the renderer's under a flood. The callbacks the emulator fires for what
// passes are the same ones it fires for the full stream.
type modesOnly struct {
	state  modeState
	seq    []byte
	out    []byte
	osc    bool // the held sequence is an OSC
	csiPri bool // the CSI started with '?'
}

type modeState uint8

const (
	modeGround modeState = iota
	modeEsc
	modeCSI
	modeOSC
	modeOSCEsc
	modeString    // DCS, APC, PM, SOS: dropped until ST
	modeStringEsc // ESC seen inside a dropped string
)

// maxHeldSequence caps an OSC or CSI being held. One past it is dropped, as
// the emulator drops an OSC over its own cap. OSC 52 carries a clipboard in
// base64, so the cap is the clipboard's.
const maxHeldSequence = 1 << 20

// filter returns the bytes of in that the emulator still needs. The result
// is valid until the next call. A sequence split across calls is held.
func (f *modesOnly) filter(in []byte) []byte {
	f.out = f.out[:0]
	for i := 0; i < len(in); i++ {
		b := in[i]
		switch f.state {
		case modeGround:
			switch b {
			case 0x1b:
				f.state = modeEsc
			case 0x07:
				f.out = append(f.out, b)
			}
		case modeEsc:
			switch b {
			case ']':
				f.start(modeOSC, true)
			case '[':
				f.start(modeCSI, false)
			case 'P', '_', '^', 'X':
				f.state = modeString
			case 0x1b:
				// ESC ESC: the second one starts again.
			default:
				f.state = modeGround
			}
		case modeCSI:
			f.hold(b)
			switch {
			case b >= 0x40 && b <= 0x7e:
				// A private mode set or reset passes; any other CSI goes.
				if f.csiPri && (b == 'h' || b == 'l') {
					f.out = append(f.out, f.seq...)
				}
				f.state = modeGround
			case b == '?' && len(f.seq) == 3:
				f.csiPri = true
			case b == 0x1b:
				f.state = modeEsc
			case b < 0x20 && b != 0x1b:
				// C0 controls inside a CSI act on their own; nothing here
				// needs them.
			}
		case modeOSC:
			switch b {
			case 0x07:
				f.hold(b)
				f.emit()
			case 0x1b:
				f.state = modeOSCEsc
			default:
				f.hold(b)
			}
		case modeOSCEsc:
			if b == '\\' {
				f.hold(0x1b)
				f.hold('\\')
				f.emit()
			} else {
				// An ESC that is not ST cuts the OSC. The backends differ on
				// a cut OSC: the pure Go emulator runs it and libghostty-vt
				// drops it. So the OSC passes cut by an ESC of its own,
				// finished as a harmless SGR reset, and the emulator decides
				// as it would on the full stream. The ESC starts a new
				// sequence here: b is read again after it.
				f.hold(0x1b)
				f.hold('[')
				f.hold('m')
				f.emit()
				f.state = modeEsc
				i--
			}
		case modeString:
			if b == 0x1b {
				f.state = modeStringEsc
			} else if b == 0x07 {
				// tmux and others end some strings with BEL.
				f.state = modeGround
			}
		case modeStringEsc:
			if b == '\\' {
				f.state = modeGround
			} else {
				// As in the emulator, an ESC that is not ST ends the string
				// and starts a new sequence.
				f.state = modeEsc
				i--
			}
		}
	}
	return f.out
}

// start begins holding a sequence whose introducer has been read.
func (f *modesOnly) start(s modeState, osc bool) {
	f.state = s
	f.osc = osc
	f.csiPri = false
	f.seq = append(f.seq[:0], 0x1b)
	if osc {
		f.seq = append(f.seq, ']')
	} else {
		f.seq = append(f.seq, '[')
	}
}

// hold adds b to the held sequence, or drops the sequence past the cap.
func (f *modesOnly) hold(b byte) {
	if len(f.seq) >= maxHeldSequence {
		f.state = modeString
		f.seq = f.seq[:0]
		return
	}
	f.seq = append(f.seq, b)
}

// emit passes the held OSC on.
func (f *modesOnly) emit() {
	f.out = append(f.out, f.seq...)
	f.seq = f.seq[:0]
	f.state = modeGround
}
