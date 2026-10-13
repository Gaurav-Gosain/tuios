package app

import (
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The dock's space label: "space > session" for an attached session filed
// under a space, and nothing at all for one in none, so the bar of a person
// who never uses spaces is the bar it always was. It is the "space" component
// of the dock's left list (see internal/config/dock.go).

// dockSpaceSeparator joins the space and the session in the label.
const dockSpaceSeparator = " > "

// dockSpaceLabelMax is the widest the label is ever drawn, padding included.
// It is passive context next to the workspace readout, and a label that could
// take half the bar would push the minimized windows out to say it.
const dockSpaceLabelMax = 32

// dockSpaceBudget is the room the label may take on a bar of the given width:
// a quarter of it, never more than dockSpaceLabelMax.
func dockSpaceBudget(barWidth int) int {
	return min(dockSpaceLabelMax, barWidth/4)
}

// dockSpaceText is the label as the bar draws it, padded one cell either side
// like the workspace readout, or "" when the component is not placed, the
// session is in no space, or the label does not fit.
func (m *OS) dockSpaceText(barWidth int) string {
	if !m.dockPlan.Has(config.DockComponentSpace) || m.SessionSpace == "" {
		return ""
	}
	return dockSpaceLabel(printableTitle(m.SessionSpace), printableTitle(m.sessionTitle(m.SessionName)), dockSpaceBudget(barWidth))
}

// dockSpaceLabel fits "space > session" into budget cells, padding included.
//
// The space gives way first: it is cut down to one cell and an ellipsis before
// anything else. The session name is never cut. A cut session name in the
// dock would be a different string from the one the rail shows, and a label
// that changes width with every resize moves the rest of the bar, so when the
// session name and a one-cell space do not fit the label is not drawn at all.
func dockSpaceLabel(space, sessionName string, budget int) string {
	room := budget - 2 // the padding either side
	full := space + dockSpaceSeparator + sessionName
	if lipgloss.Width(full) <= room {
		return " " + full + " "
	}
	spaceRoom := room - lipgloss.Width(dockSpaceSeparator) - lipgloss.Width(sessionName)
	if spaceRoom < 1+lipgloss.Width(overlay.Ellipsis()) {
		return ""
	}
	return " " + overlay.Truncate(space, spaceRoom) + dockSpaceSeparator + sessionName + " "
}
