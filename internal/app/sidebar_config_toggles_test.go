package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Each knob is checked by what it removes from the rendered frame, not by the
// field being set: a toggle that changes nothing on screen is the failure these
// tests exist to catch.

// swapBool sets a config global for the duration of the test.
func swapBool(t *testing.T, p *bool, v bool) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// TestSidebarShowAgentsTogglesSection checks the agents section is drawn by
// default and gone when the knob is off, leaving the session tree intact.
func TestSidebarShowAgentsTogglesSection(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	if !strings.Contains(sidebarText(t, m), "agents") {
		t.Fatal("agents section missing with the default config; the test premise is wrong")
	}

	swapBool(t, &config.SidebarShowAgents, false)
	m = sidebarTestOS(t, 120, 40, "left")
	out := sidebarText(t, m)
	if strings.Contains(out, "agents") {
		t.Error("show_agents = false still drew the agents section")
	}
	if !strings.Contains(out, "sessions") {
		t.Error("show_agents = false took the session tree with it")
	}
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowAgent {
			t.Fatal("show_agents = false left an agent row clickable")
		}
	}
}

// TestDockWorkspaceTabsToggle checks the dock strip is off by config alone, and
// that the columns it used to own stop routing to a workspace.
func TestDockWorkspaceTabsToggle(t *testing.T) {
	m := dockTabTestOS(t, 1, 1, 3, 7)
	m.renderDockString()
	if len(m.dockWorkspaceHits) == 0 {
		t.Fatal("dock strip missing with the default config; the test premise is wrong")
	}
	y := m.GetDockbarContentYPosition()
	was := m.dockWorkspaceHits[0]

	swapBool(t, &config.DockWorkspaceTabs, false)
	m = dockTabTestOS(t, 1, 1, 3, 7)
	m.renderDockString()
	if got := len(m.dockWorkspaceHits); got != 0 {
		t.Errorf("dock_workspace_tabs = false still recorded %d tabs", got)
	}
	if got := m.DockWorkspaceAt(was.X0, y); got != 0 {
		t.Errorf("column %d still routes to workspace %d with the strip off", was.X0, got)
	}
}

// TestSidebarMarqueeToggle checks the knob reaches the scroll itself: with it
// off an overflowing hovered title is truncated and the render tick is allowed
// to idle.
func TestSidebarMarqueeToggle(t *testing.T) {
	const long = "a-very-long-window-name-that-will-not-fit"

	m := &OS{}
	m.sidebarMarquee("w:1", long, 10, true)
	if !m.SidebarMarqueeActive() {
		t.Fatal("a hovered overflowing row did not arm the marquee by default")
	}

	swapBool(t, &config.SidebarMarquee, false)
	m = &OS{}
	off := m.sidebarMarquee("w:1", long, 10, true)
	if m.SidebarMarqueeActive() {
		t.Error("marquee = false still armed the scroll, so the tick never idles")
	}
	if plain := m.sidebarMarquee("w:1", long, 10, false); off != plain {
		t.Errorf("marquee = false rendered %q for a hovered row, want the unhovered %q", off, plain)
	}
}
