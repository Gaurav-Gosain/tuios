package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// backdate moves a window's adopted-title timestamp into the past so the
// debounce window can be crossed without sleeping.
func backdate(m *OS, id string, d time.Duration) {
	e := m.sidebarTitles[id]
	e.at = e.at.Add(-d)
	m.sidebarTitles[id] = e
}

// TestRailTitleDebounce pins the coalescing: a new title seeds immediately, a
// burst within the interval is held on the last adopted value, and once the
// interval passes the current (final) title is adopted. Correctness of the
// final title is the property that matters most.
func TestRailTitleDebounce(t *testing.T) {
	win := &terminal.Window{ID: "w1"}
	win.SetTitle("cmd-a")
	m := &OS{Windows: []*terminal.Window{win}}

	// Seed: a first title shows at once.
	if changed := m.updateRailTitles(); changed {
		t.Fatal("seeding a first title should not report a change")
	}
	if got := m.railTitleShown(win); got != "cmd-a" {
		t.Fatalf("shown after seed = %q, want %q", got, "cmd-a")
	}

	// A burst right after adoption is held: the rail keeps the last adopted title
	// and the change stays pending.
	win.SetTitle("cmd-b")
	if changed := m.updateRailTitles(); changed {
		t.Fatal("a change inside the debounce window must not be adopted yet")
	}
	if got := m.railTitleShown(win); got != "cmd-a" {
		t.Fatalf("shown mid-burst = %q, want the held %q", got, "cmd-a")
	}
	if !m.sidebarTitlePending {
		t.Fatal("a deferred change must leave a pending flag so the tick settles it")
	}

	// More churn, then the interval passes: the CURRENT title wins, not an
	// intermediate one, and the pending flag clears.
	win.SetTitle("cmd-final")
	backdate(m, "w1", railTitleDebounce)
	if changed := m.updateRailTitles(); !changed {
		t.Fatal("after the interval the drifted title must be adopted")
	}
	if got := m.railTitleShown(win); got != "cmd-final" {
		t.Fatalf("final shown = %q, want %q", got, "cmd-final")
	}
	if m.sidebarTitlePending {
		t.Fatal("pending must clear once the rail is in sync")
	}
}

// TestRailTitleDebounceDropsClosedWindows guards the map against unbounded
// growth as windows come and go.
func TestRailTitleDebounceDropsClosedWindows(t *testing.T) {
	a := &terminal.Window{ID: "a"}
	b := &terminal.Window{ID: "b"}
	a.SetTitle("A")
	b.SetTitle("B")
	m := &OS{Windows: []*terminal.Window{a, b}}
	m.updateRailTitles()
	if len(m.sidebarTitles) != 2 {
		t.Fatalf("seeded entries = %d, want 2", len(m.sidebarTitles))
	}

	m.Windows = []*terminal.Window{a}
	m.updateRailTitles()
	if _, ok := m.sidebarTitles["b"]; ok {
		t.Fatal("closed window's title entry was not dropped")
	}
}

// TestSidebarHoldsTitleDuringChurn ties the debounce to the rendered rail: the
// row keeps the adopted title while the live title churns, so the sidebar does
// not thrash.
func TestSidebarHoldsTitleDuringChurn(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	win := &terminal.Window{ID: "w1"}
	win.SetTitle("stable")
	m := &OS{Windows: []*terminal.Window{win}, Width: 120, Height: 40, SessionName: "s"}
	m.updateRailTitles()

	first := sidebarText(t, m)
	if !strings.Contains(first, "stable") {
		t.Fatalf("rail missing the seeded title:\n%s", first)
	}

	// Churn the live title; without a tick adopting it, the rail must hold.
	win.SetTitle("churn-1")
	win.SetTitle("churn-2")
	m.updateRailTitles() // inside the interval: holds
	held := sidebarText(t, m)
	if strings.Contains(held, "churn") {
		t.Fatalf("rail adopted a churning title instead of holding:\n%s", held)
	}
	if !strings.Contains(held, "stable") {
		t.Fatalf("rail lost the held title during churn:\n%s", held)
	}
}
