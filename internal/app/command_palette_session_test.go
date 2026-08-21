package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sessionPaletteTestOS builds an OS with two windows and no daemon client, the
// same standalone shape session_tree_test.go exercises, ready for the palette
// builder and its actions.
func sessionPaletteTestOS(t *testing.T) (*OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	first := &terminal.Window{ID: "w1", CustomName: "first"}
	second := &terminal.Window{ID: "w2", CustomName: "second"}
	return &OS{
		Windows:        []*terminal.Window{first, second},
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
	}, first, second
}

// TestGetSessionPaletteItemsStandalone checks the shape of the entries built
// from a standalone (no daemon) session tree: one "Sessions"-category entry
// for the session and one for each of its windows, named and categorised the
// way the palette row renderer and the filter both expect.
func TestGetSessionPaletteItemsStandalone(t *testing.T) {
	m, _, _ := sessionPaletteTestOS(t)

	items := getSessionPaletteItems(m)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (1 session + 2 windows)", len(items))
	}

	for _, it := range items {
		if it.Category != "Sessions" {
			t.Errorf("item %q Category = %q, want %q", it.Name, it.Category, "Sessions")
		}
		if it.Action == nil {
			t.Errorf("item %q has a nil Action", it.Name)
		}
	}

	if got := items[0].Name; got != "Session: local" {
		t.Errorf("session item Name = %q, want %q", got, "Session: local")
	}
	if got := items[1].Name; got != "Window: first" {
		t.Errorf("window item Name = %q, want %q", got, "Window: first")
	}
	if got := items[2].Name; got != "Window: second" {
		t.Errorf("window item Name = %q, want %q", got, "Window: second")
	}
}

// TestSessionPaletteItemAgentGlyph checks the glyph prefix comes from
// agentStateIndicator, the same function the window title bar uses, so a
// window's state reads the same way on both surfaces.
func TestSessionPaletteItemAgentGlyph(t *testing.T) {
	m, _, second := sessionPaletteTestOS(t)
	second.AgentState = "errored"

	items := getSessionPaletteItems(m)
	want := "Window: " + agentStateIndicator("errored") + " second"
	if got := items[2].Name; got != want {
		t.Errorf("window item Name = %q, want %q", got, want)
	}
	// The session rolls up to the worst state among its windows.
	wantSession := "Session: " + agentStateIndicator("errored") + " local"
	if got := items[0].Name; got != wantSession {
		t.Errorf("session item Name = %q, want %q", got, wantSession)
	}
}

// TestSessionPaletteItemSelectCurrentSessionIsNoOp checks that selecting the
// palette entry for the session the client is already on shows an
// informational notice rather than calling SwitchToSession, which would error
// out immediately in standalone mode (no daemon client).
func TestSessionPaletteItemSelectCurrentSessionIsNoOp(t *testing.T) {
	m, _, _ := sessionPaletteTestOS(t)

	items := getSessionPaletteItems(m)
	items[0].Action(m)
	if len(m.Notifications) != 1 {
		t.Fatalf("Notifications = %d, want 1", len(m.Notifications))
	}
	if !strings.Contains(m.Notifications[0].Message, "Already on this session") {
		t.Errorf("notification = %q, want it to mention already being on this session", m.Notifications[0].Message)
	}
}

// TestSessionPaletteItemSelectWindowFocuses checks that selecting a window
// entry focuses that exact window and invalidates its render cache, mirroring
// what a direct focus change (click, cycle) does elsewhere.
func TestSessionPaletteItemSelectWindowFocuses(t *testing.T) {
	m, first, second := sessionPaletteTestOS(t)
	first.CachedContent = "stale"
	second.CachedContent = "stale"

	items := getSessionPaletteItems(m)
	// items[2] is "Window: second".
	items[2].Action(m)

	if m.FocusedWindow != 1 {
		t.Fatalf("FocusedWindow = %d, want 1 (second)", m.FocusedWindow)
	}
	if second.CachedContent != "" {
		t.Errorf("focused window's cache was not invalidated: CachedContent = %q", second.CachedContent)
	}
}

// TestOpenCommandPalettePopulatesSessionItems checks OpenCommandPalette resets
// the palette's navigation state and (re)builds the session/window entries, so
// every call site that opens the palette (there are several, across two
// packages) gets the same behaviour through one function.
func TestOpenCommandPalettePopulatesSessionItems(t *testing.T) {
	m, _, _ := sessionPaletteTestOS(t)
	m.CommandPaletteQuery = "stale"
	m.CommandPaletteSelected = 3
	m.CommandPaletteScroll = 2

	m.OpenCommandPalette()

	if !m.ShowCommandPalette {
		t.Error("ShowCommandPalette not set")
	}
	if m.CommandPaletteQuery != "" || m.CommandPaletteSelected != 0 || m.CommandPaletteScroll != 0 {
		t.Errorf("navigation state not reset: query=%q selected=%d scroll=%d",
			m.CommandPaletteQuery, m.CommandPaletteSelected, m.CommandPaletteScroll)
	}
	if len(m.PaletteSessionItems) != 3 {
		t.Fatalf("PaletteSessionItems = %d, want 3", len(m.PaletteSessionItems))
	}
}

// TestFilteredPaletteItemsMatchesSessionEntries checks that typing a query
// unique to a window's name ranks that entry first in the merged (static +
// dynamic) list, which is the whole point of merging them: the palette becomes
// a way to jump straight to a session or window by name.
//
// It asserts the top row rather than the row count. A scored matcher admits
// anything the query is a subsequence of, so "second" also reaches
// "Scroll: Cycle Column Width"; what has to hold is that it reaches it a long
// way below the window actually called second.
func TestFilteredPaletteItemsMatchesSessionEntries(t *testing.T) {
	m, _, _ := sessionPaletteTestOS(t)
	m.OpenCommandPalette()
	m.CommandPaletteQuery = "second"

	filtered := m.filteredPaletteItems()
	if len(filtered) == 0 {
		t.Fatal("filtered = 0, want the window named second")
	}
	if filtered[0].Name != "Window: second" {
		names := make([]string, len(filtered))
		for i, it := range filtered {
			names[i] = it.Name
		}
		t.Errorf("filtered[0].Name = %q, want %q; full list %v", filtered[0].Name, "Window: second", names)
	}
	if len(filtered[0].Match) == 0 {
		t.Error("the top row carries no match positions, so the renderer cannot highlight it")
	}
}
