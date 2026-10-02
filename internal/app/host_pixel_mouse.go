package app

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// SGR-pixel mouse reports from the host terminal.
//
// A pane program that turns on DEC mode 1016 (a web page or a Wayland window
// drawn with kitty graphics) reads every mouse report as a pixel position.
// tuios hears its own terminal in cells, so on its own it can only tell such
// a program the centre of the cell under the pointer: one cell of precision,
// 9x20 pixels on a common font.
//
// So while a visible pane wants pixels, the local client asks its own
// terminal for 1016 too. The terminal then reports the pointer in pixels.
// The filter (FilterMouseMotion) turns each report back into the cell
// everything else in tuios works in, and keeps the offset inside that cell.
// The pane program is told its cell in its own pixels plus that offset.
//
// The wire form of a pixel report is the SGR form of a cell report, so the
// two can only be told apart by when the terminal switched. tuios sends the
// switch and a DECRQM for 1016 in one write, and the terminal answers in
// order: every report before the answer is in cells, every report after it in
// the encoding the answer names. The conversion turns on and off with the
// answer, never with the request. A terminal that does not know 1016 answers
// that, or not at all, and the conversion never turns on.
//
// The cell size the conversion divides by is asked for in the same write
// (XTWINOPS 16), and asked again on every resize, since a font size change
// resizes the grid. The startup probe's cell size is not used: when the host
// did not answer it, it is a guess.
//
// Only the local client does this, for the reason mode 2031 is local only
// (see host_colors.go): only the local client resets the terminal when it
// exits (terminal.ResetTerminal), so a served client that went away with the
// mode on would leave its terminal reporting pixels to the next program.

const (
	// hostPixelMouseOn asks for the cell size, turns 1016 on, and asks which
	// state 1016 is in. The answers arrive in this order.
	hostPixelMouseOn = "\x1b[16t\x1b[?1016h\x1b[?1016$p"
	// hostPixelMouseOff turns 1016 off and asks for its state.
	hostPixelMouseOff = "\x1b[?1016l\x1b[?1016$p"
	// hostCellSizeQuery asks for one cell's size in pixels.
	hostCellSizeQuery = "\x1b[16t"
)

// hostPixelMouse is the client's state of host SGR-pixel reporting.
type hostPixelMouse struct {
	// requested is the state last asked of the terminal.
	requested bool
	// refused says the terminal answered a request for 1016 without turning
	// it on, or without saying its cell size. It is not asked again.
	refused bool
	// active says the terminal confirmed 1016, so mouse reports arrive in
	// pixels and the filter converts them.
	active bool
	// cellW and cellH are the host cell size the terminal last reported.
	cellW, cellH int
	// subX and subY are the pointer's offset in pixels inside the host cell
	// of the mouse event being handled. subOK says they are for this event.
	subX, subY int
	subOK      bool
}

// followsHostPixelMouse reports whether this client may turn 1016 on.
func (m *OS) followsHostPixelMouse() bool {
	return m.Client == ClientLocal && !m.LearnMode
}

// paneWantsPixelMouse reports whether a pane on screen tracks the mouse in
// SGR-pixel mode.
func (m *OS) paneWantsPixelMouse() bool {
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace || w.Minimized || w.Terminal == nil {
			continue
		}
		if w.Terminal.HasPixelMouseMode() {
			return true
		}
	}
	return false
}

// noteHostPixelMouse takes the terminal's answers before the message is
// handled: the state of 1016 and the cell size. A terminal that turned 1016 on
// without saying its cell size cannot be read, so 1016 is turned off again.
func (m *OS) noteHostPixelMouse(msg tea.Msg) tea.Cmd {
	hp := &m.hostPixel
	switch msg := msg.(type) {
	case tea.ModeReportMsg:
		if msg.Mode != ansi.ModeMouseExtSgrPixel {
			return nil
		}
		on := msg.Value == ansi.ModeSet || msg.Value == ansi.ModePermanentlySet
		hp.active = on && hp.cellW > 0 && hp.cellH > 0
		if !hp.requested {
			return nil
		}
		if !on {
			hp.refused = true
			hp.requested = false
			return nil
		}
		if !hp.active {
			hp.refused = true
			hp.requested = false
			return tea.Raw(hostPixelMouseOff)
		}
	case uv.CellSizeEvent:
		if msg.Width > 0 && msg.Height > 0 {
			hp.cellW, hp.cellH = msg.Width, msg.Height
		}
	}
	return nil
}

// hostPixelMouseCmd runs after a message is handled. A mouse event can follow
// a pane that turned 1016 on or off, so after one the terminal is asked for
// the state the panes on screen want. A resize asks for the cell size again.
func (m *OS) hostPixelMouseCmd(msg tea.Msg) tea.Cmd {
	hp := &m.hostPixel
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
	case tea.WindowSizeMsg:
		if hp.requested {
			return tea.Raw(hostCellSizeQuery)
		}
		return nil
	default:
		return nil
	}
	want := m.followsHostPixelMouse() && !hp.refused && m.paneWantsPixelMouse()
	if want == hp.requested {
		return nil
	}
	hp.requested = want
	if want {
		return tea.Raw(hostPixelMouseOn)
	}
	return tea.Raw(hostPixelMouseOff)
}

// convertHostPixelMouse turns a mouse report the terminal sent in pixels into
// cells, and keeps the offset inside the cell for the pane it reaches. It runs
// in the filter, before anything reads the event's position.
func (m *OS) convertHostPixelMouse(msg tea.Msg) tea.Msg {
	hp := &m.hostPixel
	if _, ok := msg.(tea.MouseMsg); !ok {
		return msg
	}
	hp.subOK = false
	if !hp.active {
		return msg
	}
	toCell := func(mouse tea.Mouse) tea.Mouse {
		x, y := max(mouse.X, 0), max(mouse.Y, 0)
		cx := min(x/hp.cellW, max(m.Width-1, 0))
		cy := min(y/hp.cellH, max(m.Height-1, 0))
		hp.subX = min(x-cx*hp.cellW, hp.cellW-1)
		hp.subY = min(y-cy*hp.cellH, hp.cellH-1)
		hp.subOK = true
		mouse.X, mouse.Y = cx, cy
		return mouse
	}
	switch e := msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(toCell(tea.Mouse(e)))
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(toCell(tea.Mouse(e)))
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(toCell(tea.Mouse(e)))
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(toCell(tea.Mouse(e)))
	}
	return msg
}

// PointerPixelIn is the pointer's pixel position inside a pane, for the mouse
// event being handled, given the cell (termX, termY) the event reaches in
// that pane. The pane's own cell size scales the offset the host reported.
// It reports OK false when the host reported the pointer in cells.
func (m *OS) PointerPixelIn(cellW, cellH, termX, termY int) vt.MousePixel {
	hp := &m.hostPixel
	if !hp.subOK || hp.cellW <= 0 || hp.cellH <= 0 {
		return vt.MousePixel{}
	}
	if cellW <= 0 || cellH <= 0 {
		cellW, cellH = hp.cellW, hp.cellH
	}
	return vt.MousePixel{
		X:  termX*cellW + hp.subX*cellW/hp.cellW,
		Y:  termY*cellH + hp.subY*cellH/hp.cellH,
		OK: true,
	}
}
