package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The dock's two session controls, at the bar's right-hand end.
//
// They exist because a multiplexer asks people to hold two ideas apart that
// look the same from the outside: quitting the client while the session keeps
// running, and ending the session and everything in it. Only one of those is
// recoverable.
//
// So they are deliberately not a matched pair. Leaving is the prominent
// control, drawn in the dock's normal text colour and bold. Closing is
// recessed, muted until the pointer is on it, and it always goes through a
// confirmation that names what would die. Two equally weighted buttons an inch
// apart is the layout that produces the misclick, and this misclick cannot be
// taken back.
//
// They sit apart from the workspace strip on purpose: the strip is
// workspace-scoped and these are session-scoped, and the two ended up next to
// each other in an early sketch where "close" read as "close this workspace".

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
)

// dockSessionHit is where a session control was drawn on the last frame.
// Recorded by the renderer for the same reason the minimized entries are: the
// strip's columns depend on the bar's total width and on whether the leave
// control is there at all, and a handler that works that out again is a second
// implementation that can disagree with the frame on screen.
type dockSessionHit struct {
	X0, X1, Y int
	Action    DockSessionAction
}

// dockSessionTier is how much of a control the dock has room to draw.
type dockSessionTier int

const (
	// dockSessionTierOff draws no controls at all.
	dockSessionTierOff dockSessionTier = iota
	// dockSessionTierIcon draws the glyphs alone.
	dockSessionTierIcon
	// dockSessionTierLabel draws the glyphs with their words.
	dockSessionTierLabel
)

const (
	// dockSessionLabelMinWidth is the narrowest dock that still spells the
	// controls out. The labels are the dock's first concession on a narrowing
	// screen: the mode pill, the workspace strip and the meters each carry a
	// number or a state you cannot get anywhere else, where these two words only
	// name a glyph that stays put.
	dockSessionLabelMinWidth = 110

	// dockSessionIconMinWidth is the narrowest dock that carries the controls at
	// all. Below it the bar has nothing left to give and they go.
	dockSessionIconMinWidth = 34
)

// dockSessionLabels are the words on the controls. Plain on purpose: "logout"
// and "shutdown" are what the two actions are, not what they do, and the desktop
// metaphor is already carried by the glyphs beside them.
const (
	dockSessionLeaveLabel = "Leave running"
	dockSessionCloseLabel = "Close session"
)

// dockSessionIcon is a control's glyph, following the configured glyph set.
func dockSessionIcon(a DockSessionAction) string {
	if a == DockSessionLeave {
		return config.GetDockIconLeaveRunning()
	}
	return config.GetDockIconCloseSession()
}

// dockSessionLabel is a control's word.
func dockSessionLabel(a DockSessionAction) string {
	if a == DockSessionLeave {
		return dockSessionLeaveLabel
	}
	return dockSessionCloseLabel
}

// dockSessionTierFor picks the tier from the dock's width alone, so the layout
// pass and the render pass cannot land on different ones.
func dockSessionTierFor(renderWidth int) dockSessionTier {
	switch {
	case renderWidth >= dockSessionLabelMinWidth:
		return dockSessionTierLabel
	case renderWidth >= dockSessionIconMinWidth:
		return dockSessionTierIcon
	}
	return dockSessionTierOff
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
// (x, y), clearing it when the pointer is on neither. It reports whether the
// pointer is on a control, so a caller can consume the motion.
//
// The recessed control brightens here and nowhere else: it is muted at rest so
// it does not compete with the one beside it, and loud under the pointer so the
// button about to be clicked is the button that looks clickable.
func (m *OS) DockSessionHoverAt(x, y int) bool {
	a := m.DockSessionActionAt(x, y)
	m.dockSessionHover = a
	return a != DockSessionNone
}
