package vt

import (
	"io"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DcsHandler is a function that handles a DCS escape sequence.
type DcsHandler func(params ansi.Params, data []byte) bool

// CsiHandler is a function that handles a CSI escape sequence.
type CsiHandler func(params ansi.Params) bool

// OscHandler is a function that handles an OSC escape sequence.
type OscHandler func(data []byte) bool

// ApcHandler is a function that handles an APC escape sequence.
type ApcHandler func(data []byte) bool

// SosHandler is a function that handles an SOS escape sequence.
type SosHandler func(data []byte) bool

// PmHandler is a function that handles a PM escape sequence.
type PmHandler func(data []byte) bool

// EscHandler is a function that handles an ESC escape sequence.
type EscHandler func() bool

// CcHandler is a function that handles a control character.
type CcHandler func() bool

// handlers contains the terminal's escape sequence handlers.
type handlers struct {
	ccHandlers  map[byte][]CcHandler
	dcsHandlers map[int][]DcsHandler
	csiHandlers map[int][]CsiHandler
	oscHandlers map[int][]OscHandler
	escHandler  map[int][]EscHandler
	apcHandlers []ApcHandler
	sosHandlers []SosHandler
	pmHandlers  []PmHandler
}

// RegisterDcsHandler registers a DCS escape sequence handler.
func (h *handlers) RegisterDcsHandler(cmd int, handler DcsHandler) {
	if h.dcsHandlers == nil {
		h.dcsHandlers = make(map[int][]DcsHandler)
	}
	h.dcsHandlers[cmd] = append(h.dcsHandlers[cmd], handler)
}

// RegisterCsiHandler registers a CSI escape sequence handler.
func (h *handlers) RegisterCsiHandler(cmd int, handler CsiHandler) {
	if h.csiHandlers == nil {
		h.csiHandlers = make(map[int][]CsiHandler)
	}
	h.csiHandlers[cmd] = append(h.csiHandlers[cmd], handler)
}

// RegisterOscHandler registers an OSC escape sequence handler.
func (h *handlers) RegisterOscHandler(cmd int, handler OscHandler) {
	if h.oscHandlers == nil {
		h.oscHandlers = make(map[int][]OscHandler)
	}
	h.oscHandlers[cmd] = append(h.oscHandlers[cmd], handler)
}

// RegisterApcHandler registers an APC escape sequence handler.
func (h *handlers) RegisterApcHandler(handler ApcHandler) {
	h.apcHandlers = append(h.apcHandlers, handler)
}

// RegisterSosHandler registers an SOS escape sequence handler.
func (h *handlers) RegisterSosHandler(handler SosHandler) {
	h.sosHandlers = append(h.sosHandlers, handler)
}

// RegisterPmHandler registers a PM escape sequence handler.
func (h *handlers) RegisterPmHandler(handler PmHandler) {
	h.pmHandlers = append(h.pmHandlers, handler)
}

// RegisterEscHandler registers an ESC escape sequence handler.
func (h *handlers) RegisterEscHandler(cmd int, handler EscHandler) {
	if h.escHandler == nil {
		h.escHandler = make(map[int][]EscHandler)
	}
	h.escHandler[cmd] = append(h.escHandler[cmd], handler)
}

// registerCcHandler registers a control character handler.
func (h *handlers) registerCcHandler(r byte, handler CcHandler) {
	if h.ccHandlers == nil {
		h.ccHandlers = make(map[byte][]CcHandler)
	}
	h.ccHandlers[r] = append(h.ccHandlers[r], handler)
}

// handleCc handles a control character.
// It returns true if the control character was handled.
func (h *handlers) handleCc(r byte) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	for i := len(h.ccHandlers[r]) - 1; i >= 0; i-- {
		if h.ccHandlers[r][i]() {
			return true
		}
	}
	return false
}

// handleDcs handles a DCS escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handleDcs(cmd ansi.Cmd, params ansi.Params, data []byte) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	if handlers, ok := h.dcsHandlers[int(cmd)]; ok {
		for i := len(handlers) - 1; i >= 0; i-- {
			if handlers[i](params, data) {
				return true
			}
		}
	}
	return false
}

// handleCsi handles a CSI escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handleCsi(cmd ansi.Cmd, params ansi.Params) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	if handlers, ok := h.csiHandlers[int(cmd)]; ok {
		for i := len(handlers) - 1; i >= 0; i-- {
			if handlers[i](params) {
				return true
			}
		}
	}
	return false
}

// handleOsc handles an OSC escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handleOsc(cmd int, data []byte) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	if handlers, ok := h.oscHandlers[cmd]; ok {
		for i := len(handlers) - 1; i >= 0; i-- {
			if handlers[i](data) {
				return true
			}
		}
	}
	return false
}

// handleApc handles an APC escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handleApc(data []byte) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	for i := len(h.apcHandlers) - 1; i >= 0; i-- {
		if h.apcHandlers[i](data) {
			return true
		}
	}
	return false
}

// handleSos handles an SOS escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handleSos(data []byte) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	for i := len(h.sosHandlers) - 1; i >= 0; i-- {
		if h.sosHandlers[i](data) {
			return true
		}
	}
	return false
}

// handlePm handles a PM escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handlePm(data []byte) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	for i := len(h.pmHandlers) - 1; i >= 0; i-- {
		if h.pmHandlers[i](data) {
			return true
		}
	}
	return false
}

// handleEsc handles an ESC escape sequence.
// It returns true if the sequence was handled.
func (h *handlers) handleEsc(cmd int) bool {
	// Reverse iterate over the handlers so that the last registered handler
	// is the first to be called.
	if handlers, ok := h.escHandler[cmd]; ok {
		for i := len(handlers) - 1; i >= 0; i-- {
			if handlers[i]() {
				return true
			}
		}
	}
	return false
}

// registerDefaultHandlers registers the default escape sequence handlers.
func (e *Emulator) registerDefaultHandlers() {
	e.registerDefaultCcHandlers()
	e.registerDefaultCsiHandlers()
	e.registerDefaultEscHandlers()
	e.registerDefaultOscHandlers()
}

// registerDefaultCcHandlers registers the default control character handlers.
func (e *Emulator) registerDefaultCcHandlers() {
	for i := byte(ansi.NUL); i <= ansi.US; i++ {
		switch i {
		case ansi.NUL: // Null [ansi.NUL]
			// Ignored
			e.registerCcHandler(i, func() bool {
				return true
			})
		case ansi.BEL: // Bell [ansi.BEL]
			e.registerCcHandler(i, func() bool {
				if e.cb.Bell != nil {
					e.cb.Bell()
				}
				return true
			})
		case ansi.BS: // Backspace [ansi.BS]
			e.registerCcHandler(i, func() bool {
				e.backspace()
				return true
			})
		case ansi.HT: // Horizontal Tab [ansi.HT]
			e.registerCcHandler(i, func() bool {
				e.nextTab(1)
				return true
			})
		case ansi.LF, ansi.VT, ansi.FF:
			// Line Feed [ansi.LF]
			// Vertical Tab [ansi.VT]
			// Form Feed [ansi.FF]
			e.registerCcHandler(i, func() bool {
				e.linefeed()
				return true
			})
		case ansi.CR: // Carriage Return [ansi.CR]
			e.registerCcHandler(i, func() bool {
				e.carriageReturn()
				return true
			})
		}
	}

	for i := byte(ansi.PAD); i <= byte(ansi.APC); i++ {
		switch i {
		case ansi.HTS: // Horizontal Tab Set [ansi.HTS]
			e.registerCcHandler(i, func() bool {
				e.horizontalTabSet()
				return true
			})
		case ansi.RI: // Reverse Index [ansi.RI]
			e.registerCcHandler(i, func() bool {
				e.reverseIndex()
				return true
			})
		case ansi.SO: // Shift Out [ansi.SO]
			e.registerCcHandler(i, func() bool {
				e.gl = 1
				return true
			})
		case ansi.SI: // Shift In [ansi.SI]
			e.registerCcHandler(i, func() bool {
				e.gl = 0
				return true
			})
		case ansi.IND: // Index [ansi.IND]
			e.registerCcHandler(i, func() bool {
				e.index()
				return true
			})
		case ansi.SS2: // Single Shift 2 [ansi.SS2]
			e.registerCcHandler(i, func() bool {
				e.gsingle = 2
				return true
			})
		case ansi.SS3: // Single Shift 3 [ansi.SS3]
			e.registerCcHandler(i, func() bool {
				e.gsingle = 3
				return true
			})
		}
	}
}

// registerDefaultOscHandlers registers the default OSC escape sequence handlers.
func (e *Emulator) registerDefaultOscHandlers() {
	for _, cmd := range []int{
		0, // Set window title and icon name
		1, // Set icon name
		2, // Set window title
	} {
		e.RegisterOscHandler(cmd, func(data []byte) bool {
			e.handleTitle(cmd, data)
			return true
		})
	}

	e.RegisterOscHandler(7, func(data []byte) bool {
		// Report the shell current working directory
		// [ansi.NotifyWorkingDirectory].
		e.handleWorkingDirectory(7, data)
		return true
	})

	e.RegisterOscHandler(8, func(data []byte) bool {
		// Set/Query Hyperlink [ansi.SetHyperlink]
		e.handleHyperlink(8, data)
		return true
	})

	for _, cmd := range []int{
		10,  // Set/Query foreground color
		11,  // Set/Query background color
		12,  // Set/Query cursor color
		110, // Reset foreground color
		111, // Reset background color
		112, // Reset cursor color
	} {
		e.RegisterOscHandler(cmd, func(data []byte) bool {
			e.handleDefaultColor(cmd, data)
			return true
		})
	}
}

// registerDefaultEscHandlers registers the default ESC escape sequence handlers.
func (e *Emulator) registerDefaultEscHandlers() {
	e.RegisterEscHandler('=', func() bool {
		// Keypad Application Mode [ansi.DECKPAM]
		e.setMode(ansi.NumericKeypadMode, ansi.ModeSet)
		return true
	})

	e.RegisterEscHandler('>', func() bool {
		// Keypad Numeric Mode [ansi.DECKPNM]
		e.setMode(ansi.NumericKeypadMode, ansi.ModeReset)
		return true
	})

	e.RegisterEscHandler('7', func() bool {
		// Save Cursor [ansi.DECSC]
		e.scr.SaveCursor()
		return true
	})

	e.RegisterEscHandler('8', func() bool {
		// Restore Cursor [ansi.DECRC]
		e.scr.RestoreCursor()
		return true
	})

	for _, cmd := range []int{
		ansi.Command(0, '(', 'A'), // UK G0
		ansi.Command(0, ')', 'A'), // UK G1
		ansi.Command(0, '*', 'A'), // UK G2
		ansi.Command(0, '+', 'A'), // UK G3
		ansi.Command(0, '(', 'B'), // USASCII G0
		ansi.Command(0, ')', 'B'), // USASCII G1
		ansi.Command(0, '*', 'B'), // USASCII G2
		ansi.Command(0, '+', 'B'), // USASCII G3
		ansi.Command(0, '(', '0'), // Special G0
		ansi.Command(0, ')', '0'), // Special G1
		ansi.Command(0, '*', '0'), // Special G2
		ansi.Command(0, '+', '0'), // Special G3
	} {
		e.RegisterEscHandler(cmd, func() bool {
			// Select Character Set [ansi.SCS]
			c := ansi.Cmd(cmd)
			set := c.Intermediate() - '('
			switch c.Final() {
			case 'A': // UK Character Set
				e.charsets[set] = UK
			case 'B': // USASCII Character Set
				e.charsets[set] = nil // USASCII is the default
			case '0': // Special Drawing Character Set
				e.charsets[set] = SpecialDrawing
			default:
				return false
			}
			return true
		})
	}

	e.RegisterEscHandler('D', func() bool {
		// Index [ansi.IND]
		e.index()
		return true
	})

	e.RegisterEscHandler('H', func() bool {
		// Horizontal Tab Set [ansi.HTS]
		e.horizontalTabSet()
		return true
	})

	e.RegisterEscHandler('M', func() bool {
		// Reverse Index [ansi.RI]
		e.reverseIndex()
		return true
	})

	e.RegisterEscHandler('c', func() bool {
		// Reset Initial State [ansi.RIS]
		e.fullReset()
		return true
	})

	e.RegisterEscHandler('n', func() bool {
		// Locking Shift G2 [ansi.LS2]
		e.gl = 2
		return true
	})

	e.RegisterEscHandler('o', func() bool {
		// Locking Shift G3 [ansi.LS3]
		e.gl = 3
		return true
	})

	e.RegisterEscHandler('|', func() bool {
		// Locking Shift 3 Right [ansi.LS3R]
		e.gr = 3
		return true
	})

	e.RegisterEscHandler('}', func() bool {
		// Locking Shift 2 Right [ansi.LS2R]
		e.gr = 2
		return true
	})

	e.RegisterEscHandler('~', func() bool {
		// Locking Shift 1 Right [ansi.LS1R]
		e.gr = 1
		return true
	})
}

// registerDefaultCsiHandlers registers the default CSI escape sequence handlers.
func (e *Emulator) registerDefaultCsiHandlers() {
	e.RegisterCsiHandler('@', func(params ansi.Params) bool {
		// Insert Character [ansi.ICH]
		n, _, _ := params.Param(0, 1)
		e.scr.InsertCell(n)
		return true
	})

	e.RegisterCsiHandler('A', func(params ansi.Params) bool {
		// Cursor Up [ansi.CUU]
		n, _, _ := params.Param(0, 1)
		e.moveCursor(0, -n)
		return true
	})

	e.RegisterCsiHandler('B', func(params ansi.Params) bool {
		// Cursor Down [ansi.CUD]
		n, _, _ := params.Param(0, 1)
		e.moveCursor(0, n)
		return true
	})

	e.RegisterCsiHandler('C', func(params ansi.Params) bool {
		// Cursor Forward [ansi.CUF]
		n, _, _ := params.Param(0, 1)
		e.moveCursor(n, 0)
		return true
	})

	e.RegisterCsiHandler('D', func(params ansi.Params) bool {
		// Cursor Backward [ansi.CUB]
		n, _, _ := params.Param(0, 1)
		e.moveCursor(-n, 0)
		return true
	})

	e.RegisterCsiHandler('E', func(params ansi.Params) bool {
		// Cursor Next Line [ansi.CNL]
		n, _, _ := params.Param(0, 1)
		e.moveCursor(0, n)
		e.carriageReturn()
		return true
	})

	e.RegisterCsiHandler('F', func(params ansi.Params) bool {
		// Cursor Previous Line [ansi.CPL]
		n, _, _ := params.Param(0, 1)
		e.moveCursor(0, -n)
		e.carriageReturn()
		return true
	})

	e.RegisterCsiHandler('G', func(params ansi.Params) bool {
		// Cursor Horizontal Absolute [ansi.CHA]
		n, _, _ := params.Param(0, 1)
		_, y := e.scr.CursorPosition()
		e.setCursor(n-1, y)
		return true
	})

	e.RegisterCsiHandler('H', func(params ansi.Params) bool {
		// Cursor Position [ansi.CUP]
		width, height := e.Width(), e.Height()
		row, _, _ := params.Param(0, 1)
		col, _, _ := params.Param(1, 1)
		if row < 1 {
			row = 1
		}
		if col < 1 {
			col = 1
		}
		y := min(height-1, row-1)
		x := min(width-1, col-1)
		e.setCursorPosition(x, y)
		return true
	})

	e.RegisterCsiHandler('I', func(params ansi.Params) bool {
		// Cursor Horizontal Tabulation [ansi.CHT]
		n, _, _ := params.Param(0, 1)
		e.nextTab(n)
		return true
	})

	e.RegisterCsiHandler('J', func(params ansi.Params) bool {
		// Erase in Display [ansi.ED]
		n, _, _ := params.Param(0, 0)
		width, height := e.Width(), e.Height()
		x, y := e.scr.CursorPosition()
		switch n {
		case 0: // Erase screen below (from after cursor position)
			rect1 := uv.Rect(x, y, width, 1)            // cursor to end of line
			rect2 := uv.Rect(0, y+1, width, height-y-1) // next line onwards
			e.scr.FillArea(e.scr.blankCell(), rect1)
			e.scr.FillArea(e.scr.blankCell(), rect2)
		case 1: // Erase screen above (including cursor)
			rect := uv.Rect(0, 0, width, y+1)
			e.scr.FillArea(e.scr.blankCell(), rect)
		case 2: // erase screen
			e.scr.Clear()
		case 3: // erase display including scrollback
			e.scr.ClearScrollback()
			e.scr.Clear()
		default:
			return false
		}
		return true
	})

	e.RegisterCsiHandler('K', func(params ansi.Params) bool {
		// Erase in Line [ansi.EL]
		n, _, _ := params.Param(0, 0)
		// NOTE: Erase Line (EL) erases all character attributes but not cell
		// bg color.
		x, y := e.scr.CursorPosition()
		w := e.scr.Width()

		switch n {
		case 0: // Erase from cursor to end of line
			e.eraseCharacter(w - x)
		case 1: // Erase from start of line to cursor
			rect := uv.Rect(0, y, x+1, 1)
			e.scr.FillArea(e.scr.blankCell(), rect)
		case 2: // Erase entire line
			rect := uv.Rect(0, y, w, 1)
			e.scr.FillArea(e.scr.blankCell(), rect)
		default:
			return false
		}
		return true
	})

	e.RegisterCsiHandler('L', func(params ansi.Params) bool {
		// Insert Line [ansi.IL]
		n, _, _ := params.Param(0, 1)
		if e.scr.InsertLine(n) {
			// Move the cursor to the left margin.
			e.scr.setCursorX(0, true)
		}
		return true
	})

	e.RegisterCsiHandler('M', func(params ansi.Params) bool {
		// Delete Line [ansi.DL]
		n, _, _ := params.Param(0, 1)
		if e.scr.DeleteLine(n) {
			// If the line was deleted successfully, move the cursor to the
			// left.
			// Move the cursor to the left margin.
			e.scr.setCursorX(0, true)
		}
		return true
	})

	e.RegisterCsiHandler('P', func(params ansi.Params) bool {
		// Delete Character [ansi.DCH]
		n, _, _ := params.Param(0, 1)
		e.scr.DeleteCell(n)
		return true
	})

	e.RegisterCsiHandler('S', func(params ansi.Params) bool {
		// Scroll Up [ansi.SU]
		n, _, _ := params.Param(0, 1)
		e.scr.ScrollUp(n)
		return true
	})

	e.RegisterCsiHandler('T', func(params ansi.Params) bool {
		// Scroll Down [ansi.SD]
		n, _, _ := params.Param(0, 1)
		e.scr.ScrollDown(n)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'W'), func(params ansi.Params) bool {
		// Set Tab at Every 8 Columns [ansi.DECST8C]
		if len(params) == 1 && params[0] == 5 {
			e.resetTabStops()
			return true
		}
		return false
	})

	e.RegisterCsiHandler('X', func(params ansi.Params) bool {
		// Erase Character [ansi.ECH]
		n, _, _ := params.Param(0, 1)
		e.eraseCharacter(n)
		return true
	})

	e.RegisterCsiHandler('Z', func(params ansi.Params) bool {
		// Cursor Backward Tabulation [ansi.CBT]
		n, _, _ := params.Param(0, 1)
		e.prevTab(n)
		return true
	})

	e.RegisterCsiHandler('`', func(params ansi.Params) bool {
		// Horizontal Position Absolute [ansi.HPA]
		n, _, _ := params.Param(0, 1)
		width := e.Width()
		_, y := e.scr.CursorPosition()
		e.setCursorPosition(min(width-1, n-1), y)
		return true
	})

	e.RegisterCsiHandler('a', func(params ansi.Params) bool {
		// Horizontal Position Relative [ansi.HPR]
		n, _, _ := params.Param(0, 1)
		width := e.Width()
		x, y := e.scr.CursorPosition()
		e.setCursorPosition(min(width-1, x+n), y)
		return true
	})

	e.RegisterCsiHandler('b', func(params ansi.Params) bool {
		// Repeat Previous Character [ansi.REP]
		n, _, _ := params.Param(0, 1)
		e.repeatPreviousCharacter(n)
		return true
	})

	e.RegisterCsiHandler('c', func(params ansi.Params) bool {
		// Primary Device Attributes [ansi.DA1]
		n, _, _ := params.Param(0, 0)
		if n != 0 {
			return false
		}

		// Do we fully support VT220?
		_, _ = io.WriteString(e.pw, ansi.PrimaryDeviceAttributes(
			62, // VT220
			1,  // 132 columns
			6,  // Selective Erase
			22, // ANSI color
		))
		return true
	})

	e.RegisterCsiHandler(ansi.Command('>', 0, 'c'), func(params ansi.Params) bool {
		// Secondary Device Attributes [ansi.DA2]
		n, _, _ := params.Param(0, 0)
		if n != 0 {
			return false
		}

		// Do we fully support VT220?
		_, _ = io.WriteString(e.pw, ansi.SecondaryDeviceAttributes(
			1,  // VT220
			10, // Version 1.0
			0,  // ROM Cartridge is always zero
		))
		return true
	})

	e.RegisterCsiHandler('d', func(params ansi.Params) bool {
		// Vertical Position Absolute [ansi.VPA]
		n, _, _ := params.Param(0, 1)
		height := e.Height()
		x, _ := e.scr.CursorPosition()
		e.setCursorPosition(x, min(height-1, n-1))
		return true
	})

	e.RegisterCsiHandler('e', func(params ansi.Params) bool {
		// Vertical Position Relative [ansi.VPR]
		n, _, _ := params.Param(0, 1)
		height := e.Height()
		x, y := e.scr.CursorPosition()
		e.setCursorPosition(x, min(height-1, y+n))
		return true
	})

	e.RegisterCsiHandler('f', func(params ansi.Params) bool {
		// Horizontal and Vertical Position [ansi.HVP]
		width, height := e.Width(), e.Height()
		row, _, _ := params.Param(0, 1)
		col, _, _ := params.Param(1, 1)
		y := min(height-1, row-1)
		x := min(width-1, col-1)
		e.setCursor(x, y)
		return true
	})

	e.RegisterCsiHandler('g', func(params ansi.Params) bool {
		// Tab Clear [ansi.TBC]
		value, _, _ := params.Param(0, 0)
		switch value {
		case 0:
			x, _ := e.scr.CursorPosition()
			e.tabstops.Reset(x)
		case 3:
			e.tabstops.Clear()
		default:
			return false
		}

		return true
	})

	e.RegisterCsiHandler('h', func(params ansi.Params) bool {
		// Set Mode [ansi.SM] - ANSI
		e.handleMode(params, true, true)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'h'), func(params ansi.Params) bool {
		// Set Mode [ansi.SM] - DEC
		e.handleMode(params, true, false)
		return true
	})

	e.RegisterCsiHandler('l', func(params ansi.Params) bool {
		// Reset Mode [ansi.RM] - ANSI
		e.handleMode(params, false, true)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'l'), func(params ansi.Params) bool {
		// Reset Mode [ansi.RM] - DEC
		e.handleMode(params, false, false)
		return true
	})

	e.RegisterCsiHandler('m', func(params ansi.Params) bool {
		// Select Graphic Rendition [ansi.SGR]
		e.handleSgr(params)
		return true
	})

	e.RegisterCsiHandler('n', func(params ansi.Params) bool {
		// Device Status Report [ansi.DSR]
		n, _, ok := params.Param(0, 1)
		if !ok || n == 0 {
			return false
		}

		switch n {
		case 5: // Operating Status
			// We're always ready ;)
			// See: https://vt100.net/docs/vt510-rm/DSR-OS.html
			_, _ = io.WriteString(e.pw, ansi.DeviceStatusReport(ansi.DECStatusReport(0)))
		case 6: // Cursor Position Report [ansi.CPR]
			x, y := e.scr.CursorPosition()
			_, _ = io.WriteString(e.pw, ansi.CursorPositionReport(x+1, y+1))
		default:
			return false
		}

		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', 0, 'n'), func(params ansi.Params) bool {
		n, _, ok := params.Param(0, 1)
		if !ok || n == 0 {
			return false
		}

		switch n {
		case 6: // Extended Cursor Position Report [ansi.DECXCPR]
			x, y := e.scr.CursorPosition()
			_, _ = io.WriteString(e.pw, ansi.ExtendedCursorPositionReport(x+1, y+1, 0)) // We don't support page numbers //nolint:errcheck
		default:
			return false
		}

		return true
	})

	e.RegisterCsiHandler(ansi.Command(0, '$', 'p'), func(params ansi.Params) bool {
		// Request Mode [ansi.DECRQM] - ANSI
		e.handleRequestMode(params, true)
		return true
	})

	e.RegisterCsiHandler(ansi.Command('?', '$', 'p'), func(params ansi.Params) bool {
		// Request Mode [ansi.DECRQM] - DEC
		e.handleRequestMode(params, false)
		return true
	})

	e.RegisterCsiHandler(ansi.Command(0, ' ', 'q'), func(params ansi.Params) bool {
		// Set Cursor Style [ansi.DECSCUSR]
		n := 1
		if param, _, ok := params.Param(0, 0); ok && param > n {
			n = param
		}
		blink := n == 0 || n%2 == 1
		style := n / 2
		if !blink {
			style--
		}
		e.scr.setCursorStyle(CursorStyle(style), blink)
		return true
	})

	e.RegisterCsiHandler('r', func(params ansi.Params) bool {
		// Set Top and Bottom Margins [ansi.DECSTBM]
		top, _, _ := params.Param(0, 1)
		if top < 1 {
			top = 1
		}

		height := e.Height()
		bottom, _ := e.parser.Param(1, height)
		if bottom < 1 {
			bottom = height
		}

		if top >= bottom {
			return false
		}

		// Rect is [x, y) which means y is exclusive. So the top margin
		// is the top of the screen minus one.
		e.scr.setVerticalMargins(top-1, bottom)

		// Move the cursor to the top-left of the screen or scroll region
		// depending on [ansi.DECOM].
		e.setCursorPosition(0, 0)
		return true
	})

	e.RegisterCsiHandler('s', func(params ansi.Params) bool {
		// Set Left and Right Margins [ansi.DECSLRM]
		// These conflict with each other. When [ansi.DECSLRM] is set, the we
		// set the left and right margins. Otherwise, we save the cursor
		// position.

		if e.isModeSet(ansi.LeftRightMarginMode) {
			// Set Left Right Margins [ansi.DECSLRM]
			left, _, _ := params.Param(0, 1)
			if left < 1 {
				left = 1
			}

			width := e.Width()
			right, _, _ := params.Param(1, width)
			if right < 1 {
				right = width
			}

			if left >= right {
				return false
			}

			e.scr.setHorizontalMargins(left-1, right)

			// Move the cursor to the top-left of the screen or scroll region
			// depending on [ansi.DECOM].
			e.setCursorPosition(0, 0)
		} else {
			// Save Current Cursor Position [ansi.SCOSC]
			e.scr.SaveCursor()
		}

		return true
	})
}
