package app

import (
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// DockSessionAction names one of the dock's session controls.
type DockSessionAction int

// The session controls. DockSessionNone is the zero value so an unset hover
// field means "the pointer is on neither".
const (
	// DockSessionNone is no control.
	DockSessionNone DockSessionAction = iota
	// DockSessionLeave quits this client and leaves the session running.
	DockSessionLeave
	// DockSessionClose ends the session and every process in it.
	DockSessionClose
	// DockSessionNew makes a session, asking which machine when there is more
	// than one to choose from.
	//
	// It is here because creating a session was the one thing with no control
	// anywhere on screen: it lived on a one-cell "+" on a rail heading, which
	// is only there when the rail is open, only reachable with a pointer or
	// after walking the rail with the keyboard, and carries no label. Making a
	// session is the first thing anybody does and the last thing that was
	// visible.
	DockSessionNew
	// DockSessionAgents opens the agents view. It is drawn only while the
	// session holds an agent pane, and it carries the count of agents that
	// wait on the person.
	//
	// The view was reachable only through ctrl+b i, the palette and the rail,
	// and a person who did not know the key had no control on screen that
	// led there. The dock is the one row that is always up.
	DockSessionAgents
)

type dockSessionHit struct {
	X0, X1, Y int
	Action    DockSessionAction
}

const dockSessionIconMinWidth = 34

func dockSessionControlsFit(renderWidth int) bool {
	return renderWidth >= dockSessionIconMinWidth
}

const (
	dockSessionLeaveLabel = "Leave running"
	dockSessionCloseLabel = "Close session"
	dockSessionNewLabel   = "New session"
	dockSessionAgentLabel = "Agents view"
)

func dockSessionIcon(a DockSessionAction, s *config.Settings) string {
	switch a {
	case DockSessionNew:
		return s.GetRailAddGlyph()
	case DockSessionAgents:
		return s.GetDockIconAgents()
	case DockSessionLeave:
		return s.GetDockIconLeaveRunning()
	default:
		return s.GetDockIconCloseSession()
	}
}

func dockSessionLabel(a DockSessionAction) string {
	switch a {
	case DockSessionNew:
		return dockSessionNewLabel
	case DockSessionAgents:
		return dockSessionAgentLabel
	case DockSessionLeave:
		return dockSessionLeaveLabel
	default:
		return dockSessionCloseLabel
	}
}

func (m *OS) dockAgents() (present bool, waiting int) {
	if !m.agentsOn() {
		return false, 0
	}
	for _, w := range m.Windows {
		if w == nil || (w.AgentState == "" && w.AgentHarness == "") {
			continue
		}
		present = true
		if sidebarAttention(m.windowMarkState(w)) {
			waiting++
		}
	}
	return present, waiting
}

func (m *OS) dockSessionTooltipText(a DockSessionAction) string {
	if a != DockSessionAgents {
		return dockSessionLabel(a)
	}
	text := dockSessionAgentLabel
	if _, waiting := m.dockAgents(); waiting > 0 {
		text += ", " + strconv.Itoa(waiting) + " " + agentNeedsYou(waiting)
	}
	return text + " (" + m.sidebarAgentKeyOr("prefix_inbox", "ctrl+b i") + ")"
}

// OpenAgentsFromDock opens the agents view from the dock's agents control. The
// control's hover and label go first: the pointer is still on the control, and
// its label would otherwise stay up over the view's header until the pointer
// moves.
func (m *OS) OpenAgentsFromDock() {
	m.dockSessionHover = DockSessionNone
	if m.Tooltip.Source == tooltipDockSession {
		m.tooltipClear()
	}
	m.OpenInbox("")
}

// CanCreateSession reports whether there is a daemon to create a session on.
// Under plain `tuios` there is none, so the control is left out of the frame
// entirely rather than drawn dead, for the reason CanLeaveRunning gives.
func (m *OS) CanCreateSession() bool {
	return m.IsDaemonSession && m.DaemonClient != nil
}

// CanLeaveRunning reports whether there is a session to leave running, which is
// the same thing as whether a daemon is holding it.
//
// Under plain `tuios` there is no daemon: the panes belong to this process and
// quitting takes them with it, so a control offering to leave them running
// would be a lie. It is left out of the frame entirely rather than drawn
// disabled, because a button that is always dead teaches people the whole strip
// is decoration.
func (m *OS) CanLeaveRunning() bool {
	return m.IsDaemonSession && m.DaemonClient != nil
}

// DockSessionActionAt returns the session control covering the absolute cell
// (x, y), or DockSessionNone.
func (m *OS) DockSessionActionAt(x, y int) DockSessionAction {
	for _, h := range m.dockSessionHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			return h.Action
		}
	}
	return DockSessionNone
}

// DockSessionHoverAt points the hover highlight at whatever control is under
// (x, y), clearing it when the pointer is on neither, and arms the tooltip that
// says what the glyph means. It reports whether the pointer is on a control, so
// a caller can consume the motion.
//
// The recessed control brightens here and nowhere else: it is muted at rest so
// it does not compete with the one beside it, and loud under the pointer so the
// button about to be clicked is the button that looks clickable.
func (m *OS) DockSessionHoverAt(x, y int) bool {
	x, y = m.ScreenPoint(x, y)
	a := m.DockSessionActionAt(x, y)
	m.dockSessionHover = a
	m.dockSessionTooltipTrack(a)
	return a != DockSessionNone
}
