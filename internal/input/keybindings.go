// Package input implements key mapping definitions and ANSI escape sequence builders for TUIOS.
//
// This module handles:
// - Converting Bubble Tea KeyPressMsg to raw terminal bytes
// - ANSI/VT escape sequence generation for terminal compatibility
// - Function key support with modifier combinations
// - macOS Option key character mappings
package input

import (
	tea "charm.land/bubbletea/v2"
)

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
// Note: Arrow keys (Up, Down, Left, Right) are handled separately in getRawKeyBytesWithMode
// to support DECCKM (application cursor keys) mode switching between CSI and SS3 sequences
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
	tea.KeyHome:      {0x1b, '[', 'H'},
	tea.KeyEnd:       {0x1b, '[', 'F'},
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

// getRawKeyBytes converts a Bubble Tea KeyPressMsg to raw bytes for PTY forwarding.
//
// Key improvements in this version:
// - Leverages Bubble Tea v2 beta Key.Text field for better Unicode/international keyboard support
// - Uses Key.Code and Key.Mod for more reliable modifier key handling
// - Implements proper ANSI/VT escape sequence generation for terminal compatibility
// - Better handling of complex key combinations (Ctrl+Shift+Alt combinations)
// - Improved function key support with modifier combinations
//
// The function ensures applications like vim, emacs, etc. work correctly.
func getRawKeyBytes(msg tea.KeyPressMsg) []byte {
	return getRawKeyBytesWithMode(msg, false)
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
		// Handle Shift+Tab (backtab) - sends CSI Z
		if actualMod&tea.ModShift != 0 && key.Code == tea.KeyTab {
			return []byte{0x1b, '[', 'Z'}
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
		// Pass the masked modifier to handleModifierKeys
		if modSeq := handleModifierKeysWithMod(key, actualMod); len(modSeq) > 0 {
			return modSeq
		}
	}

	// Handle cursor keys with DECCKM (application cursor keys) mode support
	// When applicationCursorKeys is true, send SS3 sequences (ESC O x) instead of CSI sequences (ESC [ x)
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

// ForwardKeyToTerminal is a convenience function that can be called to forward
// a key directly to a terminal window. This is useful for implementing key
// forwarding in other parts of the application.
func ForwardKeyToTerminal(msg tea.KeyPressMsg, w interface{ SendInput([]byte) error }) tea.Cmd {
	rawInput := getRawKeyBytes(msg)
	if len(rawInput) > 0 {
		if err := w.SendInput(rawInput); err != nil {
			// Terminal unavailable
			return nil
		}
	}
	return nil
}

// BuildANSISequence is a public helper that builds an ANSI escape sequence for a key.
// This can be useful for testing or debugging key sequences.
func BuildANSISequence(msg tea.KeyPressMsg) string {
	return string(getRawKeyBytes(msg))
}

// IsMacOSOptionKey checks if a rune represents a macOS Option+digit key press
// and returns the digit (1-9) and true if it matches, or 0 and false otherwise.
//
// macOS Option key mappings:
// Option+1 → ¡, Option+2 → ™, Option+3 → £, Option+4 → ¢, Option+5 → ∞
// Option+6 → §, Option+7 → ¶, Option+8 → •, Option+9 → ª
func IsMacOSOptionKey(r rune) (digit int, ok bool) {
	switch r {
	case '¡':
		return 1, true
	case '™':
		return 2, true
	case '£':
		return 3, true
	case '¢':
		return 4, true
	case '∞':
		return 5, true
	case '§':
		return 6, true
	case '¶':
		return 7, true
	case '•':
		return 8, true
	case 'ª':
		return 9, true
	default:
		return 0, false
	}
}

// IsMacOSOptionShiftKey checks if a rune represents a macOS Option+Shift+digit key press
// and returns the digit (1-9) and true if it matches, or 0 and false otherwise.
//
// macOS Option+Shift key mappings:
// Option+Shift+1 → ⁄, Option+Shift+2 → €, Option+Shift+3 → ‹, Option+Shift+4 → ›
// Option+Shift+5 → ﬁ, Option+Shift+6 → ﬂ, Option+Shift+7 → ‡, Option+Shift+8 → °
// Option+Shift+9 → ·
func IsMacOSOptionShiftKey(r rune) (digit int, ok bool) {
	switch r {
	case '⁄':
		return 1, true
	case '€':
		return 2, true
	case '‹':
		return 3, true
	case '›':
		return 4, true
	case 'ﬁ':
		return 5, true
	case 'ﬂ':
		return 6, true
	case '‡':
		return 7, true
	case '°':
		return 8, true
	case '·':
		return 9, true
	default:
		return 0, false
	}
}

// IsMacOSOptionTab checks if a rune represents a macOS Option+Tab or Option+Shift+Tab key press.
// Returns "next" for opt+tab (⇥), "prev" for opt+shift+tab (⇤), or "" if no match.
//
// macOS Option+Tab mappings:
// Option+Tab → ⇥ (U+21E5, Rightwards Arrow to Bar)
// Option+Shift+Tab → ⇤ (U+21E4, Leftwards Arrow to Bar)
func IsMacOSOptionTab(r rune) string {
	switch r {
	case '⇥': // U+21E5 opt+tab
		return "next"
	case '⇤': // U+21E4 opt+shift+tab
		return "prev"
	default:
		return ""
	}
}
