package app

import (
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"tuios.dev/vt"
)

const (
	hostPixelMouseOn      = "\x1b[16t\x1b[?1016h\x1b[?1016$p"
	hostPixelMouseOff     = "\x1b[?1016l\x1b[?1006h\x1b[?1016$p"
	hostPixelMouseRelease = "\x1b[?1016l\x1b[?1006h"
	hostCellSizeQuery     = "\x1b[16t"
)

type hostPixelMouse struct {
	requested    bool
	refused      bool
	active       bool
	cellW, cellH int
	subX, subY   int
	subOK        bool
}

func (hp *hostPixelMouse) forget() {
	hp.requested, hp.active, hp.subOK = false, false, false
}

func (m *OS) releaseHostPixelMouse() tea.Cmd {
	hp := &m.hostPixel
	was := hp.requested || hp.active
	hp.forget()
	if !was {
		return nil
	}
	return tea.Raw(hostPixelMouseRelease)
}

type hostTerminalBackMsg struct{}

func (m *OS) followsHostPixelMouse() bool {
	return m.Client == ClientLocal && !m.LearnMode
}

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

func (m *OS) noteHostPixelMouse(msg tea.Msg) tea.Cmd {
	if mr, ok := msg.(tea.ModeReportMsg); ok && m.screen != nil {
		m.screen.ModeReport(mr)
	}
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
	case tea.ResumeMsg, hostTerminalBackMsg:
		hp.forget()
	case uv.CellSizeEvent:
		if msg.Width > 0 && msg.Height > 0 {
			hp.cellW, hp.cellH = msg.Width, msg.Height
		}
	}
	return nil
}

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
