package vt

import (
	"io"

	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleMode(params ansi.Params, set, isAnsi bool) {
	for _, p := range params {
		param := p.Param(-1)
		if param == -1 {
			// Missing parameter, ignore
			continue
		}

		var mode ansi.Mode = ansi.DECMode(param)
		if isAnsi {
			mode = ansi.ANSIMode(param)
		}

		setting := e.modes[mode]
		if setting == ansi.ModePermanentlyReset || setting == ansi.ModePermanentlySet {
			// Permanently set modes are ignored.
			continue
		}

		setting = ansi.ModeReset
		if set {
			setting = ansi.ModeSet
		}

		e.setMode(mode, setting)
	}
}

// setAltScreenMode sets the alternate screen mode.
func (e *Emulator) setAltScreenMode(on bool) {
	if (on && e.scr == &e.scrs[1]) || (!on && e.scr == &e.scrs[0]) {
		// Already in alternate screen mode, or normal screen, do nothing.
		return
	}
	if on {
		e.scr = &e.scrs[1]
		e.scrs[1].cur = e.scrs[0].cur
		e.scr.Clear()
		e.scr.buf.Touched = nil
		e.setCursor(0, 0)
	} else {
		e.scr = &e.scrs[0]
	}
	if e.cb.AltScreen != nil {
		e.cb.AltScreen(on)
	}
	if e.cb.CursorVisibility != nil {
		e.cb.CursorVisibility(!e.scr.cur.Hidden)
	}
}

// saveCursor saves the cursor position.
func (e *Emulator) saveCursor() {
	e.scr.SaveCursor()
}

// restoreCursor restores the cursor position.
func (e *Emulator) restoreCursor() {
	e.scr.RestoreCursor()
}

// setMode sets the mode to the given value.
func (e *Emulator) setMode(mode ansi.Mode, setting ansi.ModeSetting) {
	e.logf("setting mode %T(%v) to %v", mode, mode, setting)
	e.modes[mode] = setting
	switch mode {
	case ansi.ModeTextCursorEnable:
		e.scr.setCursorHidden(!setting.IsSet())
	case ansi.ModeAltScreen:
		e.setAltScreenMode(setting.IsSet())
	case ansi.ModeSaveCursor:
		if setting.IsSet() {
			e.saveCursor()
		} else {
			e.restoreCursor()
		}
	case ansi.ModeAltScreenSaveCursor: // Alternate Screen Save Cursor (1047 & 1048)
		// Save primary screen cursor position
		// Switch to alternate screen
		// Doesn't support scrollback
		if setting.IsSet() {
			e.saveCursor()
		}
		e.setAltScreenMode(setting.IsSet())
	case ansi.ModeInBandResize:
		if setting.IsSet() {
			_, _ = io.WriteString(e.pw, ansi.InBandResize(e.Height(), e.Width(), 0, 0))
		}
	}
	if setting.IsSet() {
		if e.cb.EnableMode != nil {
			e.cb.EnableMode(mode)
		}
	} else if setting.IsReset() {
		if e.cb.DisableMode != nil {
			e.cb.DisableMode(mode)
		}
	}
}

// isModeSet returns true if the mode is set.
func (e *Emulator) isModeSet(mode ansi.Mode) bool {
	m, ok := e.modes[mode]
	return ok && m.IsSet()
}

// ApplicationCursorKeys returns true if DECCKM (application cursor keys mode) is enabled.
// When this mode is set, cursor keys send SS3 sequences (ESC O A) instead of CSI sequences (ESC [ A).
func (e *Emulator) ApplicationCursorKeys() bool {
	return e.isModeSet(ansi.ModeCursorKeys)
}

// BracketedPasteEnabled returns true if bracketed paste mode (?2004) is enabled.
// When enabled, pasted text should be wrapped with escape sequences.
func (e *Emulator) BracketedPasteEnabled() bool {
	return e.isModeSet(ansi.ModeBracketedPaste)
}
