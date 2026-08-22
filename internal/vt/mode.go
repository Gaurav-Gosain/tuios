package vt

import "github.com/charmbracelet/x/ansi"

// defaultModes lists the recognized modes and their default values, in the
// order their effects apply on a reset. The order is fixed because several
// setMode calls have side effects - resetting 1048 restores the cursor,
// resetting DECOM homes it, resetting DECLRMM clears the margins, resetting
// the alternate screen switches back to the main one - and ranging over a
// map applied them in a different order on every run, so RIS left the
// terminal in a state that varied between runs. DECOM goes last so a reset
// deterministically ends with the cursor homed.
var defaultModes = []struct {
	mode    ansi.Mode
	setting ansi.ModeSetting
}{
	{ansi.ModeCursorKeys, ansi.ModeReset},          // ?1
	{ansi.ModeInsertReplace, ansi.ModeReset},       // 4, an ANSI mode
	{ansi.ModeAutoWrap, ansi.ModeSet},              // ?7
	{ansi.ModeMouseX10, ansi.ModeReset},            // ?9
	{ansi.ModeLineFeedNewLine, ansi.ModeReset},     // ?20
	{ansi.ModeTextCursorEnable, ansi.ModeSet},      // ?25
	{ansi.ModeNumericKeypad, ansi.ModeReset},       // ?66
	{ansi.ModeMouseNormal, ansi.ModeReset},         // ?1000
	{ansi.ModeMouseHighlight, ansi.ModeReset},      // ?1001
	{ansi.ModeMouseButtonEvent, ansi.ModeReset},    // ?1002
	{ansi.ModeMouseAnyEvent, ansi.ModeReset},       // ?1003
	{ansi.ModeFocusEvent, ansi.ModeReset},          // ?1004
	{ansi.ModeMouseExtSgr, ansi.ModeReset},         // ?1006
	{ansi.ModeAltScreen, ansi.ModeReset},           // ?1047
	{ansi.ModeSaveCursor, ansi.ModeReset},          // ?1048
	{ansi.ModeAltScreenSaveCursor, ansi.ModeReset}, // ?1049
	{ansi.ModeBracketedPaste, ansi.ModeReset},      // ?2004
	{ansi.ModeSynchronizedOutput, ansi.ModeReset},  // ?2026
	{ansi.ModeUnicodeCore, ansi.ModeReset},         // ?2027
	{ansi.ModeLightDark, ansi.ModeReset},           // ?2031
	{ansi.ModeLeftRightMargin, ansi.ModeReset},     // ?69
	{ansi.ModeOrigin, ansi.ModeReset},              // ?6
}

// resetModes resets all modes to their default values.
func (e *Emulator) resetModes() {
	e.modesMu.Lock()
	e.modes = make(ansi.Modes, len(defaultModes))
	for _, m := range defaultModes {
		e.modes[m.mode] = m.setting
	}
	e.modesMu.Unlock()

	// Set mode effects. setMode locks modesMu itself, so this must run after the
	// reassignment above is unlocked to avoid a re-entrant lock.
	for _, m := range defaultModes {
		e.setMode(m.mode, m.setting)
	}
}
