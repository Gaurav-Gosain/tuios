package vt

import "github.com/charmbracelet/x/ansi"

// resetModes resets all modes to their default values.
func (e *Emulator) resetModes() {
	e.modes = ansi.Modes{
		// Recognized modes and their default values.
		ansi.ModeCursorKeys:          ansi.ModeReset, // ?1
		ansi.ModeOrigin:              ansi.ModeReset, // ?6
		ansi.ModeAutoWrap:            ansi.ModeSet,   // ?7
		ansi.ModeMouseX10:            ansi.ModeReset, // ?9
		ansi.ModeLineFeedNewLine:     ansi.ModeReset, // ?20
		ansi.ModeTextCursorEnable:    ansi.ModeSet,   // ?25
		ansi.ModeNumericKeypad:       ansi.ModeReset, // ?66
		ansi.ModeLeftRightMargin:     ansi.ModeReset, // ?69
		ansi.ModeMouseNormal:         ansi.ModeReset, // ?1000
		ansi.ModeMouseHighlight:      ansi.ModeReset, // ?1001
		ansi.ModeMouseButtonEvent:    ansi.ModeReset, // ?1002
		ansi.ModeMouseAnyEvent:       ansi.ModeReset, // ?1003
		ansi.ModeFocusEvent:          ansi.ModeReset, // ?1004
		ansi.ModeMouseExtSgr:         ansi.ModeReset, // ?1006
		ansi.ModeAltScreen:           ansi.ModeReset, // ?1047
		ansi.ModeSaveCursor:          ansi.ModeReset, // ?1048
		ansi.ModeAltScreenSaveCursor: ansi.ModeReset, // ?1049
		ansi.ModeBracketedPaste:      ansi.ModeReset, // ?2004
	}

	// Set mode effects.
	for mode, setting := range e.modes {
		e.setMode(mode, setting)
	}
}
