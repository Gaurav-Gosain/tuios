package app

import (
	"image/color"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestSessionAccentVocabulary pins what a session accent may be written as. The
// daemon records the string verbatim and has never read it, so anything already
// on disk has to keep meaning what it meant, and anything unreadable has to read
// as unset rather than as a colour nobody chose.
func TestSessionAccentVocabulary(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Accent
		ok   bool
	}{
		{"cyan", SlotAccent(13), true},
		{"CYAN", SlotAccent(13), true},
		{"bright cyan", SlotAccent(6), true},
		{"bright-cyan", SlotAccent(6), true},
		{"Bright_Cyan", SlotAccent(6), true},
		{"magenta", SlotAccent(12), true},
		{"purple", SlotAccent(12), true},
		{"#89b4fa", RGBAccent(color.RGBA{R: 0x89, G: 0xb4, B: 0xfa, A: 0xff}), true},
		{"#f0a", RGBAccent(color.RGBA{R: 0xff, G: 0x00, B: 0xaa, A: 0xff}), true},
		{"", Accent{}, false},
		{"   ", Accent{}, false},
		{"chartreuse", Accent{}, false},
		{"#12345", Accent{}, false},
	} {
		got, ok := ParseAccent(tc.in)
		if ok != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("ParseAccent(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// sessionColorOS is a rail attached to "main" beside two sessions that carry
// panes of their own, which is the only shape the colours exist for: more than
// one session on screen at once.
func sessionColorOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "nvim", Width: 40, Height: 20, Workspace: 1},
		{ID: "bbbbbbbb2222", CustomName: "refactor", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
	}
	m.FocusedWindow = 0
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil
	return m, sessionColorTree()
}

func sessionColorTree() sessiontree.Tree {
	return sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
			{ID: "bbbbbbbb2222", Title: "refactor", AgentState: "working", Workspace: 1},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", AgentState: "working", Workspace: 1},
		}},
		{Name: "docs", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "ffffffff6666", Title: "site", AgentState: "idle", Workspace: 1},
		}},
	})
}

// railStyled renders the rail with its styling intact, which is what a claim
// about colour has to be made against.
func railStyled(t *testing.T, m *OS, tree sessiontree.Tree) []string {
	t.Helper()
	lines, _ := m.sidebarPanelLinesForTree(tree)
	return lines
}

// styledRow is the first rendered row whose text contains want.
func styledRow(t *testing.T, lines []string, want string) string {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(stripANSIForTrace(l), want) {
			return l
		}
	}
	t.Fatalf("no rendered row contains %q", want)
	return ""
}

// withSessionColors pins the config key for one test and puts it back.
func withSessionColors(t *testing.T, on bool) {
	t.Helper()
	prev := config.Global.SessionColors
	config.Global.SessionColors = on
	t.Cleanup(func() { config.Global.SessionColors = prev })
}

// TestSessionColourIsStableAndShared is the whole case for deriving the colour
// from the session's name instead of handing out indices in creation order: two
// clients attached to different sessions agree about every session's colour
// without exchanging anything, and a client built from scratch (which is what a
// restart is) lands on the same colours the last one had.
func TestSessionColourIsStableAndShared(t *testing.T) {
	withSessionColors(t, true)
	here, tree := sessionColorOS(t, 120, 40)

	// A second client, attached elsewhere, with its own focus and its own local
	// window accents: everything that differs between two attached terminals.
	there, thereTree := sessionColorOS(t, 120, 40)
	here.Settings = config.Global
	there.SessionName = "api"
	there.FocusedWindow = 1
	there.SetWindowAccent("aaaaaaaa1111", SlotAccent(9))
	// The rail's row order is a local drag order, so the two clients are given
	// the same sessions in different orders on purpose.
	slices.Reverse(thereTree.Sessions)

	// Both have drawn once, so both are answering with the arbitrated colours
	// rather than with a bare preference.
	railStyled(t, here, tree)
	railStyled(t, there, thereTree)

	for _, name := range []string{"main", "api", "docs"} {
		a, oka := here.SessionColor(name)
		b, okb := there.SessionColor(name)
		if !oka || !okb {
			t.Fatalf("%q has no colour: here=%v there=%v", name, oka, okb)
		}
		if a != b {
			t.Errorf("%q is %v on one client and %v on the other", name, a, b)
		}
	}

	// On screen: the row for a session neither client is attached to is drawn in
	// the same ink in both frames.
	want := styledRow(t, railStyled(t, here, tree), "docs")
	got := styledRow(t, railStyled(t, there, thereTree), "docs")
	ink := fgParams(here.sessionTint("docs", theme.TerminalBg()))
	if !strings.Contains(want, ink) || !strings.Contains(got, ink) {
		t.Errorf("the docs row is not drawn in its session colour on both clients:\n here: %q\nthere: %q", want, got)
	}
}

// TestSessionColoursAreDistinctUpToThePalette is what the arbitration buys.
// Three sessions asking at random for one of six hues collide about half the
// time, and two sessions in one colour is the exact thing the colours exist to
// prevent, so the preference alone is not enough. Up to the palette's size,
// nobody shares.
func TestSessionColoursAreDistinctUpToThePalette(t *testing.T) {
	var names []string
	for i := range sessionAccentSlotCount {
		names = append(names, "session-"+strconv.Itoa(i))
		got := assignSessionColors(names, [sessionAccentSlotCount]bool{})
		seen := map[Accent]string{}
		for _, name := range names {
			if other, dup := seen[got[name]]; dup {
				t.Fatalf("with %d sessions, %q and %q share %v", i+1, other, name, got[name])
			}
			seen[got[name]] = name
		}
	}
}

// TestSessionColourIgnoresTheOrderItIsAsked: the rail's row order is a local
// drag order, so an assignment that depended on it would give two clients
// different colours for the same sessions.
func TestSessionColourIgnoresTheOrderItIsAsked(t *testing.T) {
	names := []string{"main", "api", "docs", "infra", "notes"}
	want := assignSessionColors(names, [sessionAccentSlotCount]bool{})
	shuffled := slices.Clone(names)
	slices.Reverse(shuffled)
	if got := assignSessionColors(shuffled, [sessionAccentSlotCount]bool{}); !maps.Equal(got, want) {
		t.Errorf("the order the sessions were listed in changed the colours:\n%v\n%v", want, got)
	}
}
