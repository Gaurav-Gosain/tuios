package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// BenchmarkSidebarPanelLinesCached measures the steady-state cost of composing
// the rail when nothing changed: the common case, a pane printing output while
// the sidebar sits still. It must not rebuild or restyle.
func BenchmarkSidebarPanelLinesCached(b *testing.B) {
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	defer func() { config.Global.SidebarEnabled = false }()

	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "w" + string(rune('a'+i)), CustomName: "window"})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 120, Height: 40, SessionName: "s"}
	m.sidebarPanelLines() // prime the cache

	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanelLines()
	}
}

// BenchmarkSidebarPanelCached is the same steady state as
// BenchmarkSidebarPanelLinesCached, but through the call the compositor
// actually makes. renderSidebar needs the rail as one string, and joining the
// rows there rebuilt the whole rail on every composed frame, including the
// frames the row cache had just declared unchanged.
func BenchmarkSidebarPanelCached(b *testing.B) {
	config.Global.SidebarEnabled = true
	config.Global.SidebarPosition = "left"
	config.Global.SidebarWidth = config.SidebarDefaultWidth
	defer func() { config.Global.SidebarEnabled = false }()

	wins := make([]*terminal.Window, 0, 6)
	for i := range 6 {
		wins = append(wins, &terminal.Window{ID: "w" + string(rune('a'+i)), CustomName: "window"})
	}
	m := &OS{Settings: config.Global, Windows: wins, Width: 120, Height: 40, SessionName: "s"}
	m.sidebarPanel() // prime the cache

	b.ReportAllocs()
	for b.Loop() {
		m.sidebarPanel()
	}
}

func sidebarText(t *testing.T, m *OS) string {
	t.Helper()
	lines, w := m.sidebarPanelLines()
	if w <= 0 || lines == nil {
		t.Fatalf("sidebar reserved no columns (w=%d); test setup too narrow", w)
	}
	return strings.Join(lines, "\n")
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
		Settings:      config.Global,
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

	m.SidebarScroll[sidebarSectionSessions] = 2
	if m.sidebarSignature() == base {
		t.Fatal("sessions scroll did not change the signature")
	}
	m.SidebarScroll[sidebarSectionSessions] = 0

	m.SidebarScroll[sidebarSectionTerminals] = 2
	if m.sidebarSignature() == base {
		t.Fatal("terminals scroll did not change the signature")
	}
	m.SidebarScroll[sidebarSectionTerminals] = 0

	m.SidebarScroll[sidebarSectionAgents] = 2
	if m.sidebarSignature() == base {
		t.Fatal("agents scroll did not change the signature")
	}
	m.SidebarScroll[sidebarSectionAgents] = 0

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
		Settings:     config.Global,
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

// TestSidebarSignatureFollowsTheGlyphSet walks the appearance switches that
// change the characters the rail draws. The signature decides whether the next
// frame is served from the cache, so a switch that moves the rail without
// moving the signature hands back the previous rail.
//
// ASCII mode swaps the collapse chevrons and the agent-state indicators for
// their fallbacks, and it and the border style pick the edge rule facing the
// panes. Neither input was folded in.
func TestSidebarSignatureFollowsTheGlyphSet(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	prevASCII, prevStyle := config.Global.UseASCIIOnly, config.Global.BorderStyle
	t.Cleanup(func() {
		config.Global.UseASCIIOnly, config.Global.BorderStyle = prevASCII, prevStyle
		overlay.SetASCII(prevASCII)
	})

	flips := []struct {
		name string
		flip func()
	}{
		{"ascii-only", func() { config.Global.UseASCIIOnly = true }},
		{"border-style", func() { config.Global.BorderStyle = "double" }},
	}
	for _, f := range flips {
		t.Run(f.name, func(t *testing.T) {
			config.Global.UseASCIIOnly, config.Global.BorderStyle = false, "rounded"
			overlay.SetASCII(false)
			m := &OS{
				Settings:      config.Global,
				Windows:       []*terminal.Window{{ID: "w1", CustomName: "ALPHA", AgentState: "needs_input"}},
				FocusedWindow: 0,
				Width:         120,
				Height:        40,
				SessionName:   "s",
			}
			before, sig := sidebarText(t, m), m.sidebarSignature()

			f.flip()
			overlay.SetASCII(m.Settings.UseASCIIOnly)
			// The cache is dropped so this render shows what the rail draws now,
			// which is what the signature has to have followed.
			m.sidebarCache.invalidate()
			after := sidebarText(t, m)

			if after != before && m.sidebarSignature() == sig {
				t.Errorf("the rail redrew and the signature stayed at %d", sig)
			}
		})
	}
}

// TestRailCacheMovesTheCursorWithItsRow is the deterministic regression for a
// stale rail the rail check found in CI (TestSidebarFileEditor). The draw finds
// the cursor's row as SidebarNav[SidebarCursor], and SidebarNav is what the
// last build wrote. This forces the order that breaks a key holding only the
// index:
//
//  1. The rail has the keyboard and the cursor is on the files section's ".."
//     row, so the frame draws the cursor there.
//  2. The listing moves to "/", which has no "..". That build looks for ".."
//     and finds nothing, so it draws no cursor, and it hands the cursor to the
//     first row of the listing, which now stands at the same index.
//  3. The next frame changes nothing the key folded. Served from the cache, it
//     shows no cursor; built fresh, it shows the cursor on the first entry.
//
// Negative control, confirmed red: with sidebarSignature folding only
// SidebarCursor, step 3 reports the first entry drawn as the cursor's row in a
// fresh build and not in the cached rail.
func TestRailCacheMovesTheCursorWithItsRow(t *testing.T) {
	root := fileViewTree(t)
	m := sidebarTestOS(t, 120, 40, "left")
	m.Windows[0].Cwd = root
	m.FocusedWindow = 0
	m.loadFileViewNow(t, root)
	m.SidebarFocused = true

	railLines(t, m)
	up := m.sidebarFirstRowOfKind(sidebarRowFileUp)
	if up < 0 {
		t.Fatalf("the listing of %s published no .. row", root)
	}
	m.SidebarCursor = up
	railLines(t, m)

	m.loadFileViewNow(t, "/")
	railLines(t, m)
	if m.sidebarFirstRowOfKind(sidebarRowFileUp) >= 0 {
		t.Fatal("the listing of / still has a .. row, so the order this test needs did not happen")
	}
	if m.SidebarCursor != up {
		t.Fatalf("the cursor moved from index %d to %d, so the order this test needs did not happen", up, m.SidebarCursor)
	}

	// The frame the cache serves, against a build of the same state.
	m.sidebarPanelLines()
	if !m.sidebarCache.valid {
		t.Fatal("the rail was not cached")
	}
	cached := m.sidebarCache
	fresh, w := m.sidebarPanelLinesForTree(m.BuildSessionTree())
	if diff := railCacheDiff(&cached, fresh, w, m); diff != "" {
		t.Fatalf("the cache serves a rail that a fresh build draws differently:\n%s", diff)
	}
}

// TestRailCheckIgnoresAListingThatLandsMidFrame is the deterministic
// regression for a false report the rail check made in CI on main
// (TestChromeLooksAndFooters, string frames): the foreign session "tests"
// drawn errored in the check's rebuild and not in the cached rail.
//
// The daemon client installs a listing under its lock and moves CacheGen after
// it, on a goroutine of its own, and the key reads CacheGen without the lock.
// A listing that lands after this frame's key is read and before the check's
// rebuild is in the rebuild and not in the key. That is not a key missing an
// input: the generation has moved, and the frame the listing's message asks
// for draws it. This test lands the listing at exactly that point.
//
// Negative control, confirmed red: with railCacheMiss comparing whatever the
// generations say, the first assertion reports row 2 ("· tests" against
// "× tests").
func TestRailCheckIgnoresAListingThatLandsMidFrame(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	client := session.NewTUIClient()
	listing := func(state string) []session.SessionInfo {
		return []session.SessionInfo{
			{Name: "s", WindowCount: 1},
			{Name: "tests", WindowCount: 1, Windows: []session.WindowSummary{{
				ID: "t1", Title: "go", AgentState: state, AgentHarness: "codex", AgentMessage: "go test ./... failed",
			}}},
		}
	}
	client.UpdateSessionCache(listing(""))
	m := &OS{
		Settings: config.Global, Windows: []*terminal.Window{{ID: "w1", CustomName: "ALPHA"}},
		Width: 120, Height: 40, SessionName: "s", DaemonClient: client,
	}
	before := sidebarText(t, m)
	sidebarText(t, m) // a frame served from the cache

	// The next frame reads its key here, and the listing lands before the
	// check builds the rail again.
	ext := m.railExternalGens()
	client.UpdateSessionCache(listing("errored"))
	if diff := m.railCacheMiss(ext); diff != "" {
		t.Fatalf("the check reported a listing that landed during the frame as a key miss:\n%s", diff)
	}

	// The check still reports a difference when the key read the same
	// generations as the rebuild, so the guard above is not a check that
	// stopped comparing.
	if diff := m.railCacheMiss(m.railExternalGens()); diff == "" {
		t.Fatal("the check found no difference between the cached rail and a build of a newer listing")
	}

	// And the frame after the listing draws it.
	if after := sidebarText(t, m); after == before {
		t.Fatal("the frame after the listing landed still draws the old rail")
	}
}
