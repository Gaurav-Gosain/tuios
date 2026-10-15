package vt

import (
	"time"

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

		// A mode the emulator does not implement is not stored. Storing it
		// made DECRQM report it set, and a guest that probes before it
		// enables a feature believed a mode nothing acts on.
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

// modeAltScreenLegacy is DEC private mode 47, the original alternate screen.
// ansi has no name for it.
const modeAltScreenLegacy = ansi.DECMode(47)

// setAltScreenMode sets the alternate screen mode.
func (e *Emulator) setAltScreenMode(on bool) {
	if (on && e.scr == &e.scrs[1]) || (!on && e.scr == &e.scrs[0]) {
		// Already in alternate screen mode, or normal screen, do nothing.
		return
	}
	if on {
		e.scr = e.altScreen()
		// The switch carries the cursor across rather than homing it. None
		// of 1047 and 1049 is defined to move the cursor, and xterm, tmux and
		// ghostty all leave it where it stood. A program that switches and
		// then writes without addressing the cursor first lands where a real
		// terminal puts it.
		e.scrs[1].cur = e.scrs[0].cur
		e.scr.Clear()
	} else {
		e.scr = &e.scrs[0]
	}
	// A screen switch ends any frame in progress; clear a stuck sync flag so a
	// window is never left holding a stale frame (e.g. when an app exits without
	// closing its synchronized update).
	e.cachedSyncOutput.Store(false)
	// Each screen has its own kitty keyboard stack.
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

// saveCursor saves everything DECSC saves: the position, the pen, the character
// set selection, origin mode and the pending-wrap flag. xterm's DECSC
// documentation lists all of them, and every path that saves a cursor here
// (DECSC, SCOSC, DEC modes 1048 and 1049) means the same thing by it.
func (e *Emulator) saveCursor() {
	e.scr.SaveCursor()
	e.saveCharsets()
	e.scr.savedExtra.phantom = e.atPhantom
	e.scr.savedExtra.origin = e.isModeSet(ansi.ModeOrigin)
}

// restoreCursor is the DECRC half of saveCursor.
func (e *Emulator) restoreCursor() {
	// Origin mode goes back first, and through the map rather than through
	// setMode: setting DECOM homes the cursor, which would undo the position
	// this is about to restore.
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

// setMode sets the mode to the given value.
func (e *Emulator) setMode(mode ansi.Mode, setting ansi.ModeSetting) {
	e.modesMu.Lock()
	e.modes[mode] = setting
	e.modesMu.Unlock()
	// Before the side effects below: setting DECOM homes the cursor through
	// setCursorPosition, which has to see the new value.
	if mode == ansi.ModeOrigin {
		e.cachedOrigin.Store(setting.IsSet())
	}
	switch mode {
	case ansi.ModeTextCursorEnable:
		e.scr.setCursorHidden(!setting.IsSet())
	case modeAltScreenLegacy, ansi.ModeAltScreen:
		// 47 is the original alternate screen, and older terminfo entries
		// still send "\e[?47h" for smcup. A terminal that ignores it draws a
		// full-screen program over the primary screen and never gives the
		// primary contents back.
		//
		// Neither 47 nor 1047 saves the cursor, so leaving carries the
		// cursor back from the alternate screen, as xterm and tmux do, rather
		// than returning to wherever the primary screen last had it. A reset
		// while the primary screen is already up changes nothing, so a
		// program that sends rmcup defensively does not move its own cursor.
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
	case ansi.ModeAltScreenSaveCursor: // 1049, the effects of 1047 and 1048 combined
		// Save primary screen cursor position
		// Switch to alternate screen
		// Doesn't support scrollback
		if setting.IsSet() {
			e.saveCursor()
		}
		e.setAltScreenMode(setting.IsSet())
		// Leaving restores the primary screen's cursor as DECRC does: the
		// position, the pen, the character sets and origin mode the guest
		// had when it entered. xterm and ghostty both do. Keeping the
		// primary screen's own cursor instead gave back the position but
		// left the alternate screen's character sets in force, and a client
		// restored from a snapshot, whose primary cursor is the one it was
		// sent, came back to the wrong place.
		if setting.IsReset() {
			e.restoreCursor()
		}
	case ansi.ModeOrigin:
		// DECOM changes what a cursor address means, so DEC has it home the
		// cursor on the way in and on the way out. Leaving the cursor where it
		// was lets a program that sets origin mode and then writes without
		// addressing first put its output on whatever row it happened to be
		// on, which is a different row for every guest.
		e.atPhantom = false
		e.setCursorPosition(0, 0)
	case ansi.ModeLeftRightMargin:
		if !setting.IsSet() {
			// Resetting DECLRMM has to give the columns back. Leaving the pair
			// in place confines output to a region the guest has just stopped
			// believing in, and nothing it can send afterwards would widen it.
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

	// Update thread-safe mode caches read from the render goroutine.
	e.updateMouseModeCache()
	if mode == ansi.ModeSynchronizedOutput {
		// Only an update opened from reset starts the hold clock. A guest
		// repeating 2026h inside one update does not extend it, the same
		// as the ghostty backend.
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

// autoWrapMode reports DECAWM (?7) without touching the modes map.
//
// It exists for the callers that ask once per printed character or per line
// feed; everything colder should keep using isModeSet, which reads the map that
// remains authoritative. cachedAutoWrap is kept in step by setMode and
// RestoreModes, the only two writers of that entry.
func (e *Emulator) autoWrapMode() bool {
	return e.cachedAutoWrap.Load()
}

// insertMode reports IRM (ANSI mode 4) without touching the modes map, for the
// same reason autoWrapMode exists: the print path asks once per character.
func (e *Emulator) insertMode() bool {
	return e.cachedInsertMode.Load()
}

// originMode reports DECOM (?6) without touching the modes map, for the same
// reason autoWrapMode exists: every carriage return asks.
func (e *Emulator) originMode() bool {
	return e.cachedOrigin.Load()
}

// isModeSet returns true if the mode is set.
func (e *Emulator) isModeSet(mode ansi.Mode) bool {
	return e.modeSetting(mode).IsSet()
}

// modeSetting reads one entry of the mode map under the lock.
//
// Every read of that map goes through here or through isModeSet. A bare
// e.modes[mode] on the parser goroutine races the session layer's RestoreModes,
// and a map read racing a map write is a runtime throw rather than a panic a
// recover can catch, so it takes the whole daemon down and not just the pane
// that asked.
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
