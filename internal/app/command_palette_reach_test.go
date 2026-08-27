package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The palette listed window entries for the attached session only, so finding a
// pane in one of the other five meant switching first and looking again. These
// are the claims about what it reaches now, and about being honest that reaching
// some of it costs a session switch.

// paletteItemNamed returns the palette entry whose name contains want, or the
// zero item.
func paletteItemNamed(items []CommandPaletteItem, want string) CommandPaletteItem {
	for _, it := range items {
		if strings.Contains(it.Name, want) {
			return it
		}
	}
	return CommandPaletteItem{}
}

// paletteReachOS is a client attached to "main" beside a session it is not
// attached to whose panes the daemon has already listed.
func paletteReachOS(t *testing.T) *OS {
	t.Helper()
	m, _ := sectionsTestOS(t, 120, 30)
	m.DaemonClient.UpdateSessionCache([]session.SessionInfo{
		{Name: "main", WindowCount: 3},
		{Name: "api", WindowCount: 2, Windows: []session.WindowSummary{
			{ID: "dddddddd4444", Title: "server", AgentState: "needs_input"},
			{ID: "eeeeeeee5555", Title: "worker"},
		}},
	})
	return m
}

// TestPaletteReachesEverySessionsWindows: a pane in another session is findable
// by name, qualified by the session it is in.
func TestPaletteReachesEverySessionsWindows(t *testing.T) {
	m := paletteReachOS(t)
	items := getSessionPaletteItems(m)

	foreign := paletteItemNamed(items, "api/server")
	if foreign.Name == "" {
		t.Fatalf("no palette entry for a pane in another session:\n%s", paletteNames(items))
	}
	if foreign.Shortcut == "" {
		t.Errorf("the entry %q does not say that selecting it switches session", foreign.Name)
	}
	// A pane of the attached session is unqualified and costs nothing to reach,
	// so it says nothing.
	own := paletteItemNamed(items, "Window: nvim")
	if own.Name == "" {
		t.Fatalf("the attached session's panes stopped being listed:\n%s", paletteNames(items))
	}
	if own.Shortcut != "" {
		t.Errorf("a pane of the attached session claims to cost a switch: %q", own.Shortcut)
	}
}

// TestPaletteWindowJumpLeavesTheRail: a pane is where the user asked to end up,
// so a palette row opened from the rail hands the keyboard back rather than
// returning it to the pane the rail borrowed from.
func TestPaletteWindowJumpLeavesTheRail(t *testing.T) {
	m := paletteReachOS(t)
	m.FocusedWindow = 0
	m.SidebarFocused = true

	item := paletteItemNamed(getSessionPaletteItems(m), "refactor")
	if item.Action == nil {
		t.Fatal("no palette entry for the attached session's refactor pane")
	}
	item.Action(m)

	if m.SidebarFocused {
		t.Error("the rail kept the keyboard after the palette jumped to a pane")
	}
	if got := m.Windows[m.FocusedWindow].ID; got != "bbbbbbbb2222" {
		t.Errorf("the palette focused %s, want the refactor pane", got)
	}
}

// paletteNames is the entry names, one per line, for a failure message.
func paletteNames(items []CommandPaletteItem) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString("  " + it.Name + "\n")
	}
	return b.String()
}

// TestPaletteStateFilterFindsWhoNeedsYou walks the token vocabulary, which is
// the whole point of the filter: one keystroke past the "@" narrows a palette
// full of commands down to the panes waiting on a human.
func TestPaletteStateFilterFindsWhoNeedsYou(t *testing.T) {
	items := []CommandPaletteItem{
		{Name: "Toggle Tiling", Category: "Layout"},
		{Name: "Window: server", Category: "Sessions", AgentState: "needs_input"},
		{Name: "Window: build", Category: "Sessions", AgentState: "working"},
		{Name: "Window: refactor", Category: "Sessions", AgentState: "done"},
		{Name: "Window: broken", Category: "Sessions", AgentState: "errored"},
	}

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"@attention", []string{"server", "broken"}},
		{"@a", []string{"server", "broken"}},
		{"@w", []string{"build"}},
		{"@d", []string{"refactor"}},
		{"@e", []string{"broken"}},
		{"@n", []string{"server"}},
		// A bare "@" is the halfway house while typing: anything running an agent,
		// and no static commands.
		{"@", []string{"server", "build", "refactor", "broken"}},
		// The scored matcher still runs over what is left.
		{"@a serv", []string{"server"}},
		// A token naming nothing narrows to nothing rather than quietly not applying.
		{"@zzz", nil},
	} {
		got := FilterCommandPalette(items, tc.query)
		if len(got) != len(tc.want) {
			t.Errorf("%q matched %d entries, want %d (%v)", tc.query, len(got), len(tc.want), paletteNames(got))
			continue
		}
		for i, w := range tc.want {
			if !strings.Contains(got[i].Name, w) {
				t.Errorf("%q matched %q at %d, want %q", tc.query, got[i].Name, i, w)
			}
		}
	}
}

// TestPaletteStateFilterDropsCommands: filtering by state is a question about
// panes, so the static commands go whatever their names say.
func TestPaletteStateFilterDropsCommands(t *testing.T) {
	items := append(GetCommandPaletteItems(&config.Global),
		CommandPaletteItem{Name: "Window: idle-looking", Category: "Sessions", AgentState: "working"})
	got := FilterCommandPalette(items, "@w")
	if len(got) != 1 || !strings.Contains(got[0].Name, "idle-looking") {
		t.Errorf("@w over the full palette matched %d entries, want the one pane:\n%s", len(got), paletteNames(got))
	}
}

// TestPaletteQueryWithoutATokenIsUnchanged: the filter is opt-in, and a query
// that does not open with "@" must rank exactly as it did.
func TestPaletteQueryWithoutATokenIsUnchanged(t *testing.T) {
	items := GetCommandPaletteItems(&config.Global)
	got := FilterCommandPalette(items, "split")
	if len(got) == 0 {
		t.Fatal("an ordinary query stopped matching")
	}
	for _, it := range got {
		if !strings.Contains(strings.ToLower(it.Name+it.Category), "split") {
			t.Errorf("%q matched a query it does not contain", it.Name)
		}
	}
}
