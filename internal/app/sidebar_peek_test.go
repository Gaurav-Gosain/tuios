package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// sessionRowY is the screen row a session's rail row was drawn on.
func sessionRowY(t *testing.T, m *OS, id string) int {
	t.Helper()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowSession && h.SessionID == id {
			return h.Y0
		}
	}
	t.Fatalf("no session row for %q", id)
	return 0
}

// TestPeekCostsNoExtraRebuild is why committing on the first event is
// affordable: the pointer's own cell is already in the rail signature, so a
// motion event that crosses a session row rebuilds the rail whether or not the
// preview moves with it. Previewing per event buys correctness for nothing, and
// the debounce the pair rule existed to provide was never paying for a rebuild.
func TestPeekCostsNoExtraRebuild(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.sidebarPanelLinesForTree(tree)
	api, docs := sessionRowY(t, m, "api"), sessionRowY(t, m, "docs")

	m.SidebarMotion(1, api)
	onAPI := m.sidebarSignature()
	m.SidebarMotion(1, docs)
	if m.sidebarSignature() == onAPI {
		t.Fatal("crossing a session row leaves the rail signature alone, so the cost claim needs rechecking")
	}

	// The same crossing with the preview held still: the signature moves anyway.
	m.sidebarClearPeek()
	m.SidebarHoverX, m.SidebarHoverY = 1, api
	held := m.sidebarSignature()
	m.SidebarHoverY = docs
	if m.sidebarSignature() == held {
		t.Error("the hovered cell is not in the rail signature; a hover move would not repaint the band")
	}
}

// TestPeekNeedsNoTick: the whole preview rides arriving motion events, so a
// live peek must leave the idle gate exactly where it found it.
func TestPeekNeedsNoTick(t *testing.T) {
	m, tree := sectionsTestOS(t, 120, 30)
	m.Windows = nil
	m.sidebarPanelLinesForTree(tree)
	if m.tickNeedsWork() {
		t.Skip("the fixture is not idle to begin with")
	}
	m.SidebarPeek = "api"
	if m.tickNeedsWork() {
		t.Error("a live peek woke the maintenance tick")
	}
}

var _ = sessiontree.Tree{}
