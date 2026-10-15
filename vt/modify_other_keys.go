package vt

import (
	"fmt"
	"io"
	"strconv"

	"github.com/charmbracelet/x/ansi"
)

// modifyOtherKeysResource is the XTMODKEYS resource number of modifyOtherKeys.
// The other resources (modifyCursorKeys, modifyFunctionKeys and the rest)
// change encodings this emulator does not offer a choice of, so they are left
// unrecognised.
const modifyOtherKeysResource = 4

// registerModifyOtherKeysHandlers registers xterm's key modifier options,
// XTMODKEYS (CSI > Pp ; Pv m), its reset (CSI > Pp n) and its query, XTQMODKEYS
// (CSI ? Pp m), for the modifyOtherKeys resource.
func (e *Emulator) registerModifyOtherKeysHandlers() {
	e.RegisterCsiHandler(ansi.Command('>', 0, 'm'), func(params ansi.Params) bool {
		// "CSI > m" with no resource resets every resource, this one included.
		if len(params) == 0 {
			e.modifyOtherKeys.Store(0)
			return true
		}
		if res, _, _ := params.Param(0, -1); res != modifyOtherKeysResource {
			return false
		}
		// A missing value resets the resource to its initial value, 0.
		level, _, _ := params.Param(1, 0)
		if level < 0 || level > 2 {
			// Level 3 (xterm patch 397) has no meaning to an application here.
			return false
		}
		e.modifyOtherKeys.Store(int32(level))
		return true
	})

	e.RegisterCsiHandler(ansi.Command('>', 0, 'n'), func(params ansi.Params) bool {
		// "CSI > 4 n" disables modifyOtherKeys.
		if res, _, _ := params.Param(0, -1); res != modifyOtherKeysResource {
			return false
		}
		e.modifyOtherKeys.Store(0)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'm'), func(params ansi.Params) bool {
		// XTQMODKEYS: answered CSI > 4 ; level m.
		if res, _, _ := params.Param(0, -1); res != modifyOtherKeysResource {
			return false
		}
		_, _ = io.WriteString(e.pipe, fmt.Sprintf("\x1b[>4;%dm", e.modifyOtherKeys.Load()))
		return true
	})
}

// ModifyOtherKeys returns the modifyOtherKeys level the guest set with
// XTMODKEYS: 0 off, 1 or 2. Thread-safe.
func (e *Emulator) ModifyOtherKeys() int {
	return int(e.modifyOtherKeys.Load())
}

// RestoreModifyOtherKeys puts back a level saved from another emulator, for a
// client that reattaches after the guest set it.
func (e *Emulator) RestoreModifyOtherKeys(level int) {
	if level < 0 || level > 2 {
		return
	}
	e.modifyOtherKeys.Store(int32(level)) //nolint:gosec // bounded above
}

// EncodeModifyOtherKeys encodes a key press the way xterm does under
// modifyOtherKeys, as CSI 27 ; modifier ; code ~, or returns "" when the key
// keeps its ordinary encoding at that level. Only the "other" keys take part:
// characters, Space, Tab, Enter, Backspace and Escape. Cursor, editing,
// keypad and function keys already carry their modifiers in their own
// sequences.
//
// Level 2 encodes every modified key whose ordinary form would lose the
// modifier, which is every one but a shifted character, and Shift+Tab, which
// has CSI Z. Level 1 leaves the keys with well-known behaviour alone: a Ctrl
// or Shift chord that makes a control character (Ctrl+A is 0x01), Alt with a
// character (ESC prefix), and Backspace.
//
// The kitty keyboard protocol is the richer of the two, so a pane with kitty
// flags set does not reach this encoder.
func EncodeModifyOtherKeys(key KeyPressEvent, level int) string {
	if level <= 0 {
		return ""
	}
	shift := key.Mod.Contains(ModShift)
	alt := key.Mod.Contains(ModAlt)
	ctrl := key.Mod.Contains(ModCtrl)
	meta := key.Mod.Contains(ModMeta)
	if !shift && !alt && !ctrl && !meta {
		return ""
	}
	param := 1
	if shift {
		param++
	}
	if alt {
		param += 2
	}
	if ctrl {
		param += 4
	}
	if meta {
		param += 8
	}
	shiftOnly := param == 2
	ctrlOnly := param == 5

	code := key.Code
	encode := false
	switch code {
	case KeyTab:
		// Shift+Tab alone is back-tab, CSI Z, at both levels.
		encode = !shiftOnly
	case KeyEnter:
		encode = true
	case KeyBackspace:
		encode = level >= 2
	default:
		if code < 0x20 && code != KeyEscape || code >= KeyExtended {
			return ""
		}
		if shift {
			switch {
			case key.ShiftedCode != 0:
				code = key.ShiftedCode
			case code >= 'a' && code <= 'z':
				code -= 'a' - 'A'
			}
		}
		controlChar := code == KeyEscape || ctrl && makesControlChar(key.Code)
		switch {
		case level >= 2 && controlChar:
			encode = true
		case level >= 2:
			// A shifted character is just the character, except Space,
			// which would lose the Shift.
			encode = !shiftOnly || code == KeySpace
		case controlChar:
			encode = !ctrlOnly && !shiftOnly
		default:
			// Level 1: Alt with a character keeps its ESC prefix, and a
			// shifted character is the character.
			encode = ctrl
		}
	}
	if !encode {
		return ""
	}
	return "\x1b[27;" + strconv.Itoa(param) + ";" + strconv.Itoa(int(code)) + "~"
}

// makesControlChar reports whether Ctrl with this key makes a C0 control
// character in the ordinary encoding: the letters, Space, and @ [ \ ] ^ _ / ?.
func makesControlChar(code rune) bool {
	switch {
	case code >= 'a' && code <= 'z', code >= 'A' && code <= 'Z':
		return true
	}
	switch code {
	case ' ', '@', '[', '\\', ']', '^', '_', '/', '?':
		return true
	}
	return false
}
