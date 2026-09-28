// Key mapping definitions and ANSI escape sequence builders. This file handles:
// - Converting Bubble Tea KeyPressMsg to raw terminal bytes
// - ANSI/VT escape sequence generation for terminal compatibility
// - Function key support with modifier combinations
// - macOS Option key character mappings

package input

import (
	"runtime"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// runtimeIsDarwin reports whether the process is running on macOS.
// The macOS Option-key character tables (config.MacOSOptionChord) must only be consulted on darwin: their glyphs (¡ ™ £ ¢ ∞ § ¶ • ª, ⇥ ⇤) are
// ordinary typed characters on many non-US layouts (e.g. £ is Shift+3 on UK),
// so treating them as workspace/window shortcuts on other platforms hijacks
// real input before it reaches the shell.
func runtimeIsDarwin() bool {
	return darwinHost
}

// darwinHost is the answer runtimeIsDarwin gives. A variable so a test can put
// the macOS-only paths under test on the machine that runs CI.
var darwinHost = runtime.GOOS == "darwin"

// Ctrl key combinations mapping
// Maps the character code to its control code equivalent
var ctrlKeyMap = map[rune]byte{
	'@':  0x00, // Ctrl+@
	'[':  0x1B, // Ctrl+[ (ESC)
	'\\': 0x1C, // Ctrl+\
	']':  0x1D, // Ctrl+]
	'^':  0x1E, // Ctrl+^
	'_':  0x1F, // Ctrl+_
	'/':  0x1F, // Ctrl+/ (same as Ctrl+_)
	'?':  0x7F, // Ctrl+? (DEL)
}

// Special key codes (non-modifiers)
// Note: the cursor keys (Up, Down, Left, Right, Home, End) are handled
// separately in getRawKeyBytesWithMode to support DECCKM (application cursor
// keys) mode switching between CSI and SS3 sequences. Home and End matter as
// much as the arrows do: zsh's terminfo says khome=\EOH and kend=\EOF while
// zle holds DECCKM on, so the CSI form reaches no binding and the caret does
// not move.
var specialKeyMap = map[rune][]byte{
	tea.KeyEnter:     {'\r'},
	tea.KeyTab:       {'\t'},
	tea.KeyBackspace: {0x7f},
	tea.KeyEscape:    {0x1b},
	tea.KeySpace:     {' '},
	tea.KeyDelete:    {0x1b, '[', '3', '~'},
	tea.KeyInsert:    {0x1b, '[', '2', '~'},
	tea.KeyPgUp:      {0x1b, '[', '5', '~'},
	tea.KeyPgDown:    {0x1b, '[', '6', '~'},
}

// Function keys F1-F12
var functionKeyMap = map[rune][]byte{
	tea.KeyF1:  {0x1b, 'O', 'P'},
	tea.KeyF2:  {0x1b, 'O', 'Q'},
	tea.KeyF3:  {0x1b, 'O', 'R'},
	tea.KeyF4:  {0x1b, 'O', 'S'},
	tea.KeyF5:  {0x1b, '[', '1', '5', '~'},
	tea.KeyF6:  {0x1b, '[', '1', '7', '~'},
	tea.KeyF7:  {0x1b, '[', '1', '8', '~'},
	tea.KeyF8:  {0x1b, '[', '1', '9', '~'},
	tea.KeyF9:  {0x1b, '[', '2', '0', '~'},
	tea.KeyF10: {0x1b, '[', '2', '1', '~'},
	tea.KeyF11: {0x1b, '[', '2', '3', '~'},
	tea.KeyF12: {0x1b, '[', '2', '4', '~'},
}

// getRawKeyBytesWithMode converts a Bubble Tea KeyPressMsg to raw bytes for PTY forwarding.
// The applicationCursorKeys parameter indicates whether DECCKM mode is enabled,
// which determines whether arrow keys send SS3 (ESC O) or CSI (ESC [) sequences.
func getRawKeyBytesWithMode(msg tea.KeyPressMsg, applicationCursorKeys bool) []byte {
	key := msg.Key()

	// Mask off any non-modifier bits (Bubble Tea v2 may set additional flags like 128)
	// Only consider actual modifier keys: Shift=1, Alt=2, Ctrl=4
	modMask := tea.ModShift | tea.ModAlt | tea.ModCtrl
	actualMod := key.Mod & modMask

	// Handle modifier combinations first
	if actualMod != 0 {
		// Handle Shift+Tab (backtab): sends CSI Z
		if actualMod&tea.ModShift != 0 && key.Code == tea.KeyTab {
			return []byte{0x1b, '[', 'Z'}
		}

		// A Ctrl chord on a non-Latin layout (Ctrl+с on a Ukrainian one) has
		// no control code of its own. Terminals use the base-layout key for
		// it, so Ctrl+с is still Ctrl+C to the shell.
		if actualMod&tea.ModCtrl != 0 && key.Code >= 0x80 && key.BaseCode > 0x20 && key.BaseCode < 0x7f {
			key.Code = key.BaseCode
		}

		// Handle Ctrl+letter combinations (standard control codes)
		if actualMod&tea.ModCtrl != 0 {
			// Special Ctrl key combinations
			switch key.Code {
			case tea.KeySpace:
				return []byte{0x00} // Ctrl+Space = NUL
			case tea.KeyBackspace:
				return []byte{0x08} // Ctrl+H
			case tea.KeyTab:
				return []byte{0x09} // Ctrl+I
			case tea.KeyEnter:
				return []byte{0x0A} // Ctrl+J
			case tea.KeyEscape:
				return []byte{0x1B} // Ctrl+[
			}

			// For Ctrl+letter, convert to control codes (1-26)
			if key.Code >= 'a' && key.Code <= 'z' {
				return []byte{byte(key.Code - 'a' + 1)}
			}
			if key.Code >= 'A' && key.Code <= 'Z' {
				return []byte{byte(key.Code - 'A' + 1)}
			}

			// Check the Ctrl symbol map for other combinations
			if ctrlCode, ok := ctrlKeyMap[key.Code]; ok {
				return []byte{ctrlCode}
			}
		}

		// Handle Alt+letter combinations (ESC prefix)
		if actualMod&tea.ModAlt != 0 {
			switch key.Code {
			case tea.KeyBackspace:
				return []byte{0x1b, 0x7f}
			default:
				// Alt+character sends ESC followed by character
				if key.Text != "" && len(key.Text) == 1 {
					return []byte{0x1b, key.Text[0]}
				}
				if key.Code >= 32 && key.Code <= 126 {
					return []byte{0x1b, byte(key.Code)}
				}
			}
		}

		// Handle other modifier combinations (function keys, etc.)
		// Pass the masked modifier to handleModifierKeysWithMod
		if modSeq := handleModifierKeysWithMod(key, actualMod); len(modSeq) > 0 {
			return modSeq
		}
	}

	// Handle cursor keys with DECCKM (application cursor keys) mode support
	// When applicationCursorKeys is true, send SS3 sequences (ESC O x) instead of CSI sequences (ESC [ x).
	// Home and End follow the arrows: xterm sends ESC O H / ESC O F under
	// DECCKM, and terminfo (xterm, xterm-kitty, screen, tmux) records exactly
	// that as khome/kend, so a shell that enabled the mode binds the SS3 form.
	switch key.Code {
	case tea.KeyUp:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'A'}
		}
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'B'}
		}
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'C'}
		}
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'D'}
		}
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'H'}
		}
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		if applicationCursorKeys {
			return []byte{0x1b, 'O', 'F'}
		}
		return []byte{0x1b, '[', 'F'}
	}

	// Handle special keys (no modifiers) using lookup table
	if seq, ok := specialKeyMap[key.Code]; ok {
		return seq
	}

	// Handle function keys using lookup table
	if seq, ok := functionKeyMap[key.Code]; ok {
		return seq
	}

	// For printable characters, use Key.Text if available (handles Unicode, shifted keys)
	if key.Text != "" {
		return []byte(key.Text)
	}

	// Fallback for simple printable characters
	if key.Code >= 32 && key.Code <= 126 {
		return []byte{byte(key.Code)}
	}

	return []byte{}
}

// handleModifierKeysWithMod handles keys with complex modifier combinations
// The mod parameter should already be masked to only include actual modifier bits
func handleModifierKeysWithMod(key tea.Key, mod tea.KeyMod) []byte {
	// Handle function keys with modifiers
	if fnSeq := getFunctionKeySequence(key.Code, getModParam(mod)); fnSeq != nil {
		return fnSeq
	}

	// Handle cursor keys with modifiers
	if cursorSeq := getCursorSequence(key.Code); cursorSeq != nil {
		modParam := getModParam(mod)
		if modParam > 1 {
			// Insert modifier parameter: ESC[1;{mod}{letter}
			result := make([]byte, 0, 8)
			result = append(result, 0x1b, '[', '1', ';', byte('0'+modParam))
			result = append(result, cursorSeq[len(cursorSeq)-1]) // Last character (A,B,C,D,H,F)
			return result
		}
	}

	return []byte{}
}

// getModParam calculates modifier parameter for CSI sequences
func getModParam(mod tea.KeyMod) int {
	// Mask off any non-modifier bits (Bubble Tea v2 may set additional flags like 128)
	// Only consider actual modifier keys: Shift=1, Alt=2, Ctrl=4
	modMask := tea.ModShift | tea.ModAlt | tea.ModCtrl
	mod &= modMask

	modParam := 1
	if mod&tea.ModShift != 0 {
		modParam++
	}
	if mod&tea.ModAlt != 0 {
		modParam += 2
	}
	if mod&tea.ModCtrl != 0 {
		modParam += 4
	}
	return modParam
}

// getCursorSequence returns ANSI escape sequence for cursor movement keys
func getCursorSequence(code rune) []byte {
	switch code {
	case tea.KeyUp:
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	}
	return nil
}

// getFunctionKeySequence returns ANSI sequence for function keys with optional modifiers
func getFunctionKeySequence(code rune, modParam int) []byte {
	var baseSeq []byte

	switch code {
	case tea.KeyF1:
		baseSeq = []byte{0x1b, 'O', 'P'}
	case tea.KeyF2:
		baseSeq = []byte{0x1b, 'O', 'Q'}
	case tea.KeyF3:
		baseSeq = []byte{0x1b, 'O', 'R'}
	case tea.KeyF4:
		baseSeq = []byte{0x1b, 'O', 'S'}
	case tea.KeyF5:
		return buildCSISequence(15, modParam)
	case tea.KeyF6:
		return buildCSISequence(17, modParam)
	case tea.KeyF7:
		return buildCSISequence(18, modParam)
	case tea.KeyF8:
		return buildCSISequence(19, modParam)
	case tea.KeyF9:
		return buildCSISequence(20, modParam)
	case tea.KeyF10:
		return buildCSISequence(21, modParam)
	case tea.KeyF11:
		return buildCSISequence(23, modParam)
	case tea.KeyF12:
		return buildCSISequence(24, modParam)
	default:
		return nil
	}

	// F1-F4 with modifiers need different handling
	if modParam > 1 && baseSeq != nil {
		// Convert to CSI format: ESC[1;{mod}{P,Q,R,S}
		result := []byte{0x1b, '[', '1', ';', byte('0' + modParam)}
		result = append(result, baseSeq[len(baseSeq)-1]) // Last char (P,Q,R,S)
		return result
	}

	return baseSeq
}

// buildCSISequence builds a CSI sequence like ESC[{num};{mod}~ or ESC[{num}~
func buildCSISequence(num, modParam int) []byte {
	seq := []byte{0x1b, '['}

	// Add number
	if num >= 10 {
		seq = append(seq, byte('0'+num/10), byte('0'+num%10))
	} else {
		seq = append(seq, byte('0'+num))
	}

	// Add modifier if present
	if modParam > 1 {
		seq = append(seq, ';', byte('0'+modParam))
	}

	seq = append(seq, '~')
	return seq
}

// vtKeyFromBubbletea converts a bubbletea KeyPressMsg to a VT emulator
// KeyPressEvent for use with the kitty keyboard protocol's SendKey path.
func vtKeyFromBubbletea(msg tea.KeyPressMsg) vt.KeyPressEvent {
	key := msg.Key()
	return vt.KeyPressEvent{
		Code:        key.Code,
		Text:        key.Text,
		Mod:         vt.KeyMod(key.Mod),
		ShiftedCode: shiftedCode(key),
		BaseCode:    key.BaseCode,
		IsRepeat:    key.IsRepeat,
	}
}

// paneMsg is the key press as a pane is sent it: msg with the base-layout key
// the host sent put back. readKey takes it off for tuios's own reading, but
// both encoders need it: for alternate keys, for the control code of a Ctrl
// chord on a letter outside ASCII, and for shiftedCode to tell a real shifted
// key from the decoder's copy of the base key.
func paneMsg(msg tea.KeyPressMsg, o *app.OS) tea.KeyPressMsg {
	if msg.BaseCode == 0 {
		msg.BaseCode = o.HostBaseCode(msg)
	}
	return msg
}

// shiftedCode is the key's shifted code as the host reported it. The decoder
// that parses CSI code:shifted:base u also writes the base key into the shifted
// field, so a key that carries a base-layout key has the base key there. The
// real shifted key is then rebuilt when Shift was held: the capital of a
// letter, whatever else was held (Ctrl+Shift+ш carries no text), or else the
// one character the key typed. A shifted symbol under Ctrl or Alt has no text
// to rebuild it from, so it goes without.
func shiftedCode(key tea.Key) rune {
	if key.BaseCode == 0 || key.ShiftedCode != key.BaseCode {
		return key.ShiftedCode
	}
	if key.Mod&tea.ModShift == 0 {
		return 0
	}
	if up := unicode.ToUpper(key.Code); up != key.Code {
		return up
	}
	if r := []rune(key.Text); len(r) == 1 && r[0] != key.Code {
		return r[0]
	}
	return 0
}
