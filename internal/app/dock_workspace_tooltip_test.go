package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// pillTooltipName is longer than a pill can carry, so the strip has to cut it
// and the hover is the only way to the rest of it.
const pillTooltipName = "release/hotfix-payments"

// pillTooltipOS is a dock with three occupied workspaces, the middle one named
// past what its pill can print.
func pillTooltipOS(t *testing.T) *OS {
	t.Helper()
	m := newNarrowOS(t, 140, 30)
	m.NumWorkspaces = 9
	m.CurrentWorkspace = 1
	for _, ws := range []int{1, 2, 3} {
		win := newTestWindow(t, fmt.Sprintf("pill-ws%d", ws), 40, 10)
		win.Workspace = ws
		m.Windows = append(m.Windows, win)
	}
	m.adoptSessionLabels(&session.SessionState{
		WorkspaceNames: map[int]string{2: pillTooltipName, 3: "docs"},
	})

	prevTabs, prevTip := m.Settings.DockWorkspaceTabs, m.Settings.DockWorkspaceTooltip
	m.Settings.DockWorkspaceTabs, m.Settings.DockWorkspaceTooltip = true, true
	t.Cleanup(func() { m.Settings.DockWorkspaceTabs, m.Settings.DockWorkspaceTooltip = prevTabs, prevTip })
	return m
}

// pillFrame is the whole screen as the app would draw it, colours dropped. The
// label has to be found in the frame rather than in the layer it came from: a
// layer that composes to nothing readable is the failure worth catching.
func pillFrame(m *OS) []string {
	return strings.Split(stripANSIForTrace(lipgloss.Sprint(m.GetCanvas(true).Render())), "\n")
}

// pillRect is where workspace ws was drawn on the last frame.
func pillRect(t *testing.T, m *OS, ws int) dockWorkspaceHit {
	t.Helper()
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == ws {
			return h
		}
	}
	t.Fatalf("the dock drew no pill for workspace %d", ws)
	return dockWorkspaceHit{}
}

// TestWorkspacePillTooltipCostsNoIdleTick: pending is the only state that holds
// the maintenance tick, and it closes on the frame that draws the label. There
// is no standing tick behind any of this, so a label left up must not become
// one.
func TestWorkspacePillTooltipCostsNoIdleTick(t *testing.T) {
	m := pillTooltipOS(t)
	_ = pillFrame(m)
	if m.TooltipPending() {
		t.Fatal("a fresh dock is already pending a label")
	}

	rect := pillRect(t, m, 2)
	m.DockWorkspaceHoverAt(rect.X0, rect.Y)
	if !m.TooltipPending() {
		t.Fatal("hovering a clipped pill armed nothing")
	}
	m.Tooltip.At = time.Now().Add(-2 * tooltipDelay)
	_ = pillFrame(m)
	if m.TooltipPending() {
		t.Error("the label has been drawn and is still holding the tick open")
	}
}
