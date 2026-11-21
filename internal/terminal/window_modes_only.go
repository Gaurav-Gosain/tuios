package terminal

import "sync/atomic"

// KeepModesOnly makes the pane's emulator see only what modesOnly passes, for
// a client that draws nothing of the pane itself. Call it before the pane's
// stream starts.
func (w *Window) KeepModesOnly() { w.modesOnly.Store(&modesOnly{}) }

type modesFilter = atomic.Pointer[modesOnly]

type modesOnly struct {
	state  modeState
	seq    []byte
	out    []byte
	osc    bool
	csiPri bool
}

type modeState uint8

const (
	modeGround modeState = iota
	modeEsc
	modeCSI
	modeOSC
	modeOSCEsc
	modeString
	modeStringEsc
)

const maxHeldSequence = 1 << 20

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
			default:
				f.state = modeGround
			}
		case modeCSI:
			f.hold(b)
			switch {
			case b >= 0x40 && b <= 0x7e:
				if f.csiPri && (b == 'h' || b == 'l') {
					f.out = append(f.out, f.seq...)
				}
				f.state = modeGround
			case b == '?' && len(f.seq) == 3:
				f.csiPri = true
			case b == 0x1b:
				f.state = modeEsc
			case b < 0x20 && b != 0x1b:
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
				f.state = modeGround
			}
		case modeStringEsc:
			if b == '\\' {
				f.state = modeGround
			} else {
				f.state = modeEsc
				i--
			}
		}
	}
	return f.out
}

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

func (f *modesOnly) hold(b byte) {
	if len(f.seq) >= maxHeldSequence {
		f.state = modeString
		f.seq = f.seq[:0]
		return
	}
	f.seq = append(f.seq, b)
}

func (f *modesOnly) emit() {
	f.out = append(f.out, f.seq...)
	f.seq = f.seq[:0]
	f.state = modeGround
}
