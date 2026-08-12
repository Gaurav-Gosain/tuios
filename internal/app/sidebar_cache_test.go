package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func sidebarText(t *testing.T, m *OS) string {
	t.Helper()
	lines, w := m.sidebarPanelLines()
	if w <= 0 || lines == nil {
		t.Fatalf("sidebar reserved no columns (w=%d); test setup too narrow", w)
	}
	return strings.Join(lines, "\n")
}

// BenchmarkSidebarPanelLinesCached measures the steady-state cost of composing
// the rail when nothing changed: the common case, a pane printing output while
// the sidebar sits still. It must not rebuild or restyle.
func BenchmarkSidebarPanelLinesCached(b *testing.B) {
	config.SidebarEnabled = true
	config.SidebarPosition = "left"
	config.SidebarWidth = config.SidebarDefaultWidth
	defer func() { config.SidebarEnabled = false }()

	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "w" + string(rune('a'+i)), CustomName: "window"})
	}
	m := &OS{Windows: wins, Width: 120, Height: 40, SessionName: "s"}
	m.sidebarPanelLines() // prime the cache

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.sidebarPanelLines()
	}
}

// TestSidebarCacheServesAndInvalidates is the stale-row regression guard. The
// rail is cached between frames, so the danger is serving a row that no longer
// matches state. This walks the cases that must invalidate: a renamed window, a
// focus move, a hover move, each section's own scroll offset, a foreign-cache
// update, and MarkAllDirty. Each must show through; an unchanged frame must
// reuse the cache.
func TestSidebarCacheServesAndInvalidates(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	win := &terminal.Window{ID: "w1", CustomName: "ALPHA"}
	win2 := &terminal.Window{ID: "w2", CustomName: "BRAVO"}
	m := &OS{
		Windows:       []*terminal.Window{win, win2},
		FocusedWindow: 0,
		Width:         120,
		Height:        40,
		SessionName:   "s",
	}

	first := sidebarText(t, m)
	if !strings.Contains(first, "ALPHA") || !strings.Contains(first, "BRAVO") {
		t.Fatalf("first render missing window titles:\n%s", first)
	}
	if !m.sidebarCache.valid {
		t.Fatal("cache not populated after first render")
	}
	sig := m.sidebarCache.sig

	// No change: the cache is reused and the signature holds.
	if again := sidebarText(t, m); again != first {
		t.Fatalf("cached render diverged with no state change")
	}
	if m.sidebarCache.sig != sig {
		t.Fatal("signature moved with no state change")
	}

	// A rename must not serve the stale label.
	win.CustomName = "GAMMA"
	renamed := sidebarText(t, m)
	if strings.Contains(renamed, "ALPHA") {
		t.Fatalf("stale title ALPHA survived a rename:\n%s", renamed)
	}
	if !strings.Contains(renamed, "GAMMA") {
		t.Fatalf("new title GAMMA missing after rename:\n%s", renamed)
	}

	// Signature-affecting view changes must each move the signature.
	base := m.sidebarSignature()
	m.FocusedWindow = 1
	if m.sidebarSignature() == base {
		t.Fatal("focus move did not change the signature")
	}
	m.FocusedWindow = 0

	m.SidebarHoverActive = true
	m.SidebarHoverX, m.SidebarHoverY = 2, 3
	if m.sidebarSignature() == base {
		t.Fatal("hover did not change the signature")
	}
	m.SidebarHoverActive = false

	m.SidebarScrollS = 2
	if m.sidebarSignature() == base {
		t.Fatal("sessions scroll did not change the signature")
	}
	m.SidebarScrollS = 0

	m.SidebarScrollT = 2
	if m.sidebarSignature() == base {
		t.Fatal("terminals scroll did not change the signature")
	}
	m.SidebarScrollT = 0

	m.SidebarScrollA = 2
	if m.sidebarSignature() == base {
		t.Fatal("agents scroll did not change the signature")
	}
	m.SidebarScrollA = 0

	// MarkAllDirty drops the cache so a theme or config change restyles.
	sidebarText(t, m)
	if !m.sidebarCache.valid {
		t.Fatal("cache should be valid before MarkAllDirty")
	}
	m.MarkAllDirty()
	if m.sidebarCache.valid {
		t.Fatal("MarkAllDirty did not invalidate the sidebar cache")
	}
}

// TestSidebarCacheFollowsForeignCache guards the foreign-session path: the rail
// summarises other sessions by the client cache generation, so a refresh that
// adds or drops a foreign session must rebuild the rail rather than serve a
// stale one.
func TestSidebarCacheFollowsForeignCache(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{
		{Name: "s"},
		{Name: "other"},
	})
	m := &OS{
		Windows:      []*terminal.Window{{ID: "w1", CustomName: "ALPHA"}},
		Width:        120,
		Height:       40,
		SessionName:  "s",
		DaemonClient: client,
	}

	if before := sidebarText(t, m); !strings.Contains(before, "other") {
		t.Fatalf("foreign session missing from first render:\n%s", before)
	}

	// A refresh that drops the foreign session bumps CacheGen; the rail must follow.
	client.UpdateSessionCache([]session.SessionInfo{{Name: "s"}})
	if after := sidebarText(t, m); strings.Contains(after, "other") {
		t.Fatalf("stale foreign session 'other' survived a cache update:\n%s", after)
	}
}
