package vt

import (
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (e *Emulator) handleMode(params ansi.Params, set, isAnsi bool) {
	for _, p := range params {
		param := p.Param(-1)
		if param == -1 {
			continue
		}

		var mode ansi.Mode = ansi.DECMode(param)
		if isAnsi {
			mode = ansi.ANSIMode(param)
		}

		if !modeRecognised(mode) {
			e.logf("unhandled sequence: mode %d (ansi=%v, set=%v)", param, isAnsi, set)
			continue
		}
		if modePermanent(mode) {
			continue
		}

		setting := ansi.ModeReset
		if set {
			setting = ansi.ModeSet
		}

		e.setMode(mode, setting)
	}
}

const modeAltScreenLegacy = ansi.DECMode(47)

func (e *Emulator) setAltScreenMode(on bool) {
	if (on && e.scr == &e.scrs[1]) || (!on && e.scr == &e.scrs[0]) {
		return
	}
	if on {
		e.scr = e.altScreen()
		e.scrs[1].cur = e.scrs[0].cur
		e.scr.Clear()
	} else {
		e.scr = &e.scrs[0]
	}
	e.cachedSyncOutput.Store(false)
	if e.kittyKbd != nil {
		e.kittyKbd.SetAltScreen(on)
		e.updateKittyKeyboardCache()
	}
	if e.cb.AltScreen != nil {
		e.cb.AltScreen(on)
	}
	if e.cb.CursorVisibility != nil {
		e.cb.CursorVisibility(!e.scr.cur.Hidden)
	}
}

func (e *Emulator) saveCursor() {
	e.scr.SaveCursor()
	e.saveCharsets()
	e.scr.savedExtra.phantom = e.atPhantom
	e.scr.savedExtra.origin = e.isModeSet(ansi.ModeOrigin)
}

func (e *Emulator) restoreCursor() {
	setting := ansi.ModeReset
	if e.scr.savedExtra.origin {
		setting = ansi.ModeSet
	}
	e.modesMu.Lock()
	e.modes[ansi.ModeOrigin] = setting
	e.modesMu.Unlock()
	e.cachedOrigin.Store(e.scr.savedExtra.origin)

	e.scr.RestoreCursor()
	e.restoreCharsets()
	e.atPhantom = e.scr.savedExtra.phantom
}

func (e *Emulator) setMode(mode ansi.Mode, setting ansi.ModeSetting) {
	e.modesMu.Lock()
	e.modes[mode] = setting
	e.modesMu.Unlock()
	if mode == ansi.ModeOrigin {
		e.cachedOrigin.Store(setting.IsSet())
	}
	switch mode {
	case ansi.ModeTextCursorEnable:
		e.scr.setCursorHidden(!setting.IsSet())
	case modeAltScreenLegacy, ansi.ModeAltScreen:
		if setting.IsSet() {
			e.setAltScreenMode(true)
		} else if e.scr == &e.scrs[1] {
			pos := e.scrs[1].cur.Position
			e.setAltScreenMode(false)
			e.scr.setCursor(pos.X, pos.Y, false)
		}
	case ansi.ModeSaveCursor:
		if setting.IsSet() {
			e.saveCursor()
		} else {
			e.restoreCursor()
		}
	case ansi.ModeAltScreenSaveCursor:
		if setting.IsSet() {
			e.saveCursor()
		}
		e.setAltScreenMode(setting.IsSet())
		if setting.IsReset() {
			e.restoreCursor()
		}
	case ansi.ModeOrigin:
		e.atPhantom = false
		e.setCursorPosition(0, 0)
	case ansi.ModeLeftRightMargin:
		if !setting.IsSet() {
			e.scr.setHorizontalMargins(0, e.Width())
		}
	case ansi.ModeInBandResize:
		if setting.IsSet() {
			e.sendInBandResize()
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

	e.updateMouseModeCache()
	if mode == ansi.ModeSynchronizedOutput {
		if setting.IsSet() && !e.cachedSyncOutput.Load() {
			e.syncOpens.Add(1)
			e.syncSetAtNanos.Store(time.Now().UnixNano())
		}
		e.cachedSyncOutput.Store(setting.IsSet())
	}
	if mode == ansi.ModeAutoWrap {
		e.cachedAutoWrap.Store(setting.IsSet())
	}
	if mode == ansi.ModeInsertReplace {
		e.cachedInsertMode.Store(setting.IsSet())
	}
	if mode == ansi.ModeLineFeedNewLine {
		e.cachedLineFeedNewLine.Store(setting.IsSet())
	}
}

func (e *Emulator) autoWrapMode() bool {
	return e.cachedAutoWrap.Load()
}

func (e *Emulator) insertMode() bool {
	return e.cachedInsertMode.Load()
}

func (e *Emulator) originMode() bool {
	return e.cachedOrigin.Load()
}

func (e *Emulator) isModeSet(mode ansi.Mode) bool {
	return e.modeSetting(mode).IsSet()
}

func (e *Emulator) modeSetting(mode ansi.Mode) ansi.ModeSetting {
	e.modesMu.RLock()
	m := e.modes[mode]
	e.modesMu.RUnlock()
	return m
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

// FocusReportingEnabled reports whether the guest has focus event reporting
// (DECSET 1004) on.
func (e *Emulator) FocusReportingEnabled() bool {
	return e.isModeSet(ansi.ModeFocusEvent)
}
