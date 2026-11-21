package vt

import "github.com/charmbracelet/x/ansi"

var defaultModes = []struct {
	mode    ansi.Mode
	setting ansi.ModeSetting
}{
	{ansi.ModeCursorKeys, ansi.ModeReset},
	{ansi.ModeInsertReplace, ansi.ModeReset},
	{ansi.ModeAutoWrap, ansi.ModeSet},
	{ansi.ModeMouseX10, ansi.ModeReset},
	{ansi.ModeLineFeedNewLine, ansi.ModeReset},
	{ansi.ModeTextCursorEnable, ansi.ModeSet},
	{ansi.ModeNumericKeypad, ansi.ModeReset},
	{ansi.ModeMouseNormal, ansi.ModeReset},
	{ansi.ModeMouseHighlight, ansi.ModeReset},
	{ansi.ModeMouseButtonEvent, ansi.ModeReset},
	{ansi.ModeMouseAnyEvent, ansi.ModeReset},
	{ansi.ModeFocusEvent, ansi.ModeReset},
	{ansi.ModeMouseExtUtf8, ansi.ModeReset},
	{ansi.ModeMouseExtSgr, ansi.ModeReset},
	{ansi.ModeMouseExtUrxvt, ansi.ModeReset},
	{ansi.ModeMouseExtSgrPixel, ansi.ModeReset},
	{modeAltScreenLegacy, ansi.ModeReset},
	{ansi.ModeAltScreen, ansi.ModeReset},
	{ansi.ModeSaveCursor, ansi.ModeReset},
	{ansi.ModeAltScreenSaveCursor, ansi.ModeReset},
	{ansi.ModeInBandResize, ansi.ModeReset},
	{ansi.ModeBracketedPaste, ansi.ModeReset},
	{ansi.ModeSynchronizedOutput, ansi.ModeReset},
	{ansi.ModeUnicodeCore, ansi.ModePermanentlySet},
	{ansi.ModeLightDark, ansi.ModeReset},
	{ansi.ModeLeftRightMargin, ansi.ModeReset},
	{ansi.ModeOrigin, ansi.ModeReset},
}

var recognisedModes = func() map[ansi.Mode]ansi.ModeSetting {
	set := make(map[ansi.Mode]ansi.ModeSetting, len(defaultModes))
	for _, m := range defaultModes {
		set[m.mode] = m.setting
	}
	return set
}()

func modeRecognised(mode ansi.Mode) bool {
	_, ok := recognisedModes[mode]
	return ok
}

func modePermanent(mode ansi.Mode) bool {
	s := recognisedModes[mode]
	return s == ansi.ModePermanentlySet || s == ansi.ModePermanentlyReset
}

func (e *Emulator) resetModes() {
	e.modesMu.Lock()
	e.modes = make(ansi.Modes, len(defaultModes))
	for _, m := range defaultModes {
		e.modes[m.mode] = m.setting
	}
	e.modesMu.Unlock()

	for _, m := range defaultModes {
		e.setMode(m.mode, m.setting)
	}
}
