package vt

import (
	"fmt"
	"io"

	"github.com/charmbracelet/x/ansi"
)

// kittyKeyboardState tracks the kitty keyboard protocol state for a terminal.
// The protocol uses a stack of flag sets that can be pushed/popped by applications.
type kittyKeyboardState struct {
	stack []int // Stack of keyboard flag bitmasks
}

// newKittyKeyboardState creates a new kitty keyboard state with an empty stack.
func newKittyKeyboardState() *kittyKeyboardState {
	return &kittyKeyboardState{
		stack: []int{0}, // Always have at least one entry (the base)
	}
}

// CurrentFlags returns the currently active keyboard flags.
func (k *kittyKeyboardState) CurrentFlags() int {
	if len(k.stack) == 0 {
		return 0
	}
	return k.stack[len(k.stack)-1]
}

// Push pushes a new set of flags onto the stack.
func (k *kittyKeyboardState) Push(flags int) {
	k.stack = append(k.stack, flags)
}

// Pop removes n entries from the top of the stack.
// It always keeps at least one entry (the base).
func (k *kittyKeyboardState) Pop(n int) {
	if n <= 0 {
		n = 1
	}
	for range n {
		if len(k.stack) <= 1 {
			break
		}
		k.stack = k.stack[:len(k.stack)-1]
	}
}

// Set modifies the current flags based on the mode:
//
//	1 = set given flags, unset all others
//	2 = set given flags, keep existing unchanged
//	3 = unset given flags, keep existing unchanged
func (k *kittyKeyboardState) Set(flags, mode int) {
	current := k.CurrentFlags()
	switch mode {
	case 1:
		current = flags
	case 2:
		current |= flags
	case 3:
		current &^= flags
	default:
		current = flags
	}
	if len(k.stack) == 0 {
		k.stack = append(k.stack, current)
	} else {
		k.stack[len(k.stack)-1] = current
	}
}

// Reset clears the stack back to the base entry.
func (k *kittyKeyboardState) Reset() {
	k.stack = []int{0}
}

// HasDisambiguate returns true if the disambiguate flag is set.
func (k *kittyKeyboardState) HasDisambiguate() bool {
	return k.CurrentFlags()&ansi.KittyDisambiguateEscapeCodes != 0
}

// HasReportEvents returns true if the report events flag is set.
func (k *kittyKeyboardState) HasReportEvents() bool {
	return k.CurrentFlags()&ansi.KittyReportEventTypes != 0
}

// HasReportAlternateKeys returns true if the report alternate keys flag is set.
func (k *kittyKeyboardState) HasReportAlternateKeys() bool {
	return k.CurrentFlags()&ansi.KittyReportAlternateKeys != 0
}

// HasReportAllKeys returns true if the report all keys flag is set.
func (k *kittyKeyboardState) HasReportAllKeys() bool {
	return k.CurrentFlags()&ansi.KittyReportAllKeysAsEscapeCodes != 0
}

// registerKittyKeyboardHandlers registers CSI handlers for kitty keyboard protocol.
func (e *Emulator) registerKittyKeyboardHandlers() {
	// CSI > flags u — Push keyboard mode
	e.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(params ansi.Params) bool {
		flags := 0
		if len(params) > 0 {
			flags = params[0].Param(0)
		}
		e.kittyKbd.Push(flags)
		e.updateKittyKeyboardCache()
		e.logf("kitty keyboard: push flags=%d, stack depth=%d", flags, len(e.kittyKbd.stack))
		return true
	})

	// CSI < count u — Pop keyboard mode
	e.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(params ansi.Params) bool {
		count := 1
		if len(params) > 0 {
			count = params[0].Param(1)
		}
		e.kittyKbd.Pop(count)
		e.updateKittyKeyboardCache()
		e.logf("kitty keyboard: pop count=%d, stack depth=%d, flags=%d", count, len(e.kittyKbd.stack), e.kittyKbd.CurrentFlags())
		return true
	})

	// CSI ? u — Query keyboard mode
	e.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(_ ansi.Params) bool {
		flags := e.kittyKbd.CurrentFlags()
		// Respond with CSI ? flags u
		response := fmt.Sprintf("\x1b[?%du", flags)
		_, _ = io.WriteString(e.pw, response)
		e.logf("kitty keyboard: query, responding with flags=%d", flags)
		return true
	})

	// CSI = flags ; mode u — Set keyboard mode
	e.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(params ansi.Params) bool {
		flags := 0
		mode := 1
		if len(params) > 0 {
			flags = params[0].Param(0)
		}
		if len(params) > 1 {
			mode = params[1].Param(1)
		}
		e.kittyKbd.Set(flags, mode)
		e.updateKittyKeyboardCache()
		e.logf("kitty keyboard: set flags=%d mode=%d, result=%d", flags, mode, e.kittyKbd.CurrentFlags())
		return true
	})
}

// KittyKeyboardFlags returns the current kitty keyboard protocol flags.
// Thread-safe: reads from an atomic cache updated on push/pop/set/reset.
func (e *Emulator) KittyKeyboardFlags() int {
	return int(e.cachedKittyFlags.Load())
}

// updateKittyKeyboardCache updates the thread-safe cached flags.
// Must be called from the VT processing goroutine after any stack change.
func (e *Emulator) updateKittyKeyboardCache() {
	flags := 0
	if e.kittyKbd != nil {
		flags = e.kittyKbd.CurrentFlags()
	}
	e.cachedKittyFlags.Store(int32(flags))
}

// EncodeKeyCSIu encodes a key event in the CSI u format used by the kitty keyboard protocol.
// Returns the encoded sequence, or empty string if the key should use legacy encoding.
func EncodeKeyCSIu(key KeyPressEvent, flags int) string {
	// Only encode if at least disambiguate or report-all-keys flag is set
	if flags&(ansi.KittyDisambiguateEscapeCodes|ansi.KittyReportAllKeysAsEscapeCodes) == 0 {
		return ""
	}

	code := int(key.Code)

	// Don't encode basic printable characters without modifiers
	// (unless report-all-keys flag is set)
	if flags&ansi.KittyReportAllKeysAsEscapeCodes == 0 {
		if key.Mod == 0 && code >= 0x20 && code < 0x7f {
			return ""
		}
	}

	// Map special keys to their CSI u key codes
	switch key.Code {
	case KeyEnter:
		code = 13
	case KeyTab:
		code = 9
	case KeyBackspace:
		code = 127
	case KeyEscape:
		code = 27
	case KeySpace:
		code = 32
	case KeyUp:
		return encodeSpecialKeyCSIu('A', key.Mod, flags)
	case KeyDown:
		return encodeSpecialKeyCSIu('B', key.Mod, flags)
	case KeyRight:
		return encodeSpecialKeyCSIu('C', key.Mod, flags)
	case KeyLeft:
		return encodeSpecialKeyCSIu('D', key.Mod, flags)
	case KeyHome:
		return encodeSpecialKeyCSIu('H', key.Mod, flags)
	case KeyEnd:
		return encodeSpecialKeyCSIu('F', key.Mod, flags)
	case KeyInsert:
		return encodeTildeKeyCSIu(2, key.Mod, flags)
	case KeyDelete:
		return encodeTildeKeyCSIu(3, key.Mod, flags)
	case KeyPgUp:
		return encodeTildeKeyCSIu(5, key.Mod, flags)
	case KeyPgDown:
		return encodeTildeKeyCSIu(6, key.Mod, flags)
	case KeyF1:
		return encodeSpecialKeyCSIu('P', key.Mod, flags)
	case KeyF2:
		return encodeSpecialKeyCSIu('Q', key.Mod, flags)
	case KeyF3:
		return encodeSpecialKeyCSIu('R', key.Mod, flags)
	case KeyF4:
		return encodeSpecialKeyCSIu('S', key.Mod, flags)
	case KeyF5:
		return encodeTildeKeyCSIu(15, key.Mod, flags)
	case KeyF6:
		return encodeTildeKeyCSIu(17, key.Mod, flags)
	case KeyF7:
		return encodeTildeKeyCSIu(18, key.Mod, flags)
	case KeyF8:
		return encodeTildeKeyCSIu(19, key.Mod, flags)
	case KeyF9:
		return encodeTildeKeyCSIu(20, key.Mod, flags)
	case KeyF10:
		return encodeTildeKeyCSIu(21, key.Mod, flags)
	case KeyF11:
		return encodeTildeKeyCSIu(23, key.Mod, flags)
	case KeyF12:
		return encodeTildeKeyCSIu(24, key.Mod, flags)
	}

	// For regular keys, encode as CSI code ; modifiers u
	modParam := kittyModParam(key.Mod)
	if modParam > 1 {
		return fmt.Sprintf("\x1b[%d;%du", code, modParam)
	}
	return fmt.Sprintf("\x1b[%du", code)
}

// encodeSpecialKeyCSIu encodes a special key (arrow, home, end, F1-F4) in CSI u format.
func encodeSpecialKeyCSIu(final byte, mod KeyMod, flags int) string {
	modParam := kittyModParam(mod)
	_ = flags
	if modParam > 1 {
		return fmt.Sprintf("\x1b[1;%d%c", modParam, final)
	}
	return fmt.Sprintf("\x1b[%c", final)
}

// encodeTildeKeyCSIu encodes a tilde-terminated key (Insert, Delete, PgUp, PgDown, F5+) in CSI u format.
func encodeTildeKeyCSIu(num int, mod KeyMod, flags int) string {
	modParam := kittyModParam(mod)
	_ = flags
	if modParam > 1 {
		return fmt.Sprintf("\x1b[%d;%d~", num, modParam)
	}
	return fmt.Sprintf("\x1b[%d~", num)
}

// kittyModParam converts modifier flags to the CSI parameter format.
// The format is 1 + bitwise OR of: 1=shift, 2=alt, 4=ctrl, 8=super
func kittyModParam(mod KeyMod) int {
	param := 1
	if mod&ModShift != 0 {
		param += 1
	}
	if mod&ModAlt != 0 {
		param += 2
	}
	if mod&ModCtrl != 0 {
		param += 4
	}
	if mod&ModMeta != 0 {
		param += 8
	}
	return param
}

