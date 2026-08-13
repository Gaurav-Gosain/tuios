package app

import (
	"image/color"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

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

// glyphCell is the second content column of a rail row, which is the glyph the
// gutter sits beside, stripped of styling.
func glyphCell(row string) string {
	plain := []rune(stripANSIForTrace(row))
	if len(plain) < 2 {
		return ""
	}
	return string(plain[1])
}

// withSessionColors pins the config key for one test and puts it back.
func withSessionColors(t *testing.T, on bool) {
	t.Helper()
	prev := config.SessionColors
	config.SessionColors = on
	t.Cleanup(func() { config.SessionColors = prev })
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

// TestSessionColoursUseTheThemesChromaticSlots pins what the automatic colour is
// allowed to be: one of the six hues of the theme's own ANSI sixteen, never the
// two achromatic ones, which are the rail's ink and its ground. All six have to
// come up, or the fold is wasting the palette and colliding more than it needs
// to.
func TestSessionColoursUseTheThemesChromaticSlots(t *testing.T) {
	seen := map[int]bool{}
	for i := range 2000 {
		a := sessionAutoAccent("session-" + strconv.Itoa(i))
		if !a.IsSlot() || a.Slot < sessionAccentSlotFirst || a.Slot >= sessionAccentSlotFirst+sessionAccentSlotCount {
			t.Fatalf("session-%d landed on %v, outside the chromatic slots", i, a)
		}
		seen[a.Slot] = true
	}
	if len(seen) != sessionAccentSlotCount {
		t.Errorf("only %d of the %d hues are reachable: %v", len(seen), sessionAccentSlotCount, seen)
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

// TestSessionColourSurvivesItsNeighbours: a session whose hue nobody else asked
// for keeps it as sessions come and go, which is what makes the colour worth
// learning. Only a session that was already in a collision can be moved.
func TestSessionColourSurvivesItsNeighbours(t *testing.T) {
	// A pair with different preferences, so neither is arbitrated.
	var a, b string
	for i := range 200 {
		n := "s" + strconv.Itoa(i)
		switch {
		case a == "":
			a = n
		case sessionPreferredSlot(n) != sessionPreferredSlot(a):
			b = n
		}
		if b != "" {
			break
		}
	}
	if b == "" {
		t.Fatal("could not find two names that prefer different hues")
	}

	alone := assignSessionColors([]string{a}, [sessionAccentSlotCount]bool{})
	pair := assignSessionColors([]string{a, b}, [sessionAccentSlotCount]bool{})
	gone := assignSessionColors([]string{b}, [sessionAccentSlotCount]bool{})
	if alone[a] != pair[a] {
		t.Errorf("%q changed colour when %q appeared: %v then %v", a, b, alone[a], pair[a])
	}
	if pair[b] != gone[b] {
		t.Errorf("%q changed colour when %q went away: %v then %v", b, a, pair[b], gone[b])
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

// TestExplicitSessionAccentWinsAndClearingFallsBack is the precedence chain. An
// accent the user set with set-session-accent beats the one we derived, and
// taking it away returns the session to its automatic colour rather than to no
// colour at all: a cleared accent is not a request to be anonymous.
func TestExplicitSessionAccentWinsAndClearingFallsBack(t *testing.T) {
	withSessionColors(t, true)
	m, tree := sessionColorOS(t, 120, 40)

	auto, ok := m.SessionColor("main")
	if !ok {
		t.Fatal("the attached session has no automatic colour")
	}

	m.SessionAccent = "bright blue"
	want, ok := ParseAccent("bright blue")
	if !ok {
		t.Fatal("the accent vocabulary does not know bright blue")
	}
	if want == auto {
		t.Fatal("this fixture cannot prove anything: the explicit accent is the automatic one")
	}
	got, ok := m.SessionColor("main")
	if !ok || got != want {
		t.Fatalf("the explicit accent lost to the automatic one: got %v, want %v", got, want)
	}
	row := styledRow(t, railStyled(t, m, tree), "main")
	if !strings.Contains(row, fgParams(theme.Readable(want.RGB(), theme.TerminalBg()))) {
		t.Errorf("the attached session's mark is not the explicit accent: %q", row)
	}

	// Clearing it falls back, and falls back on screen.
	m.SessionAccent = ""
	back, ok := m.SessionColor("main")
	if !ok || back != auto {
		t.Fatalf("clearing the accent left %v, not the automatic %v (ok=%v)", back, auto, ok)
	}
	row = styledRow(t, railStyled(t, m, tree), "main")
	if !strings.Contains(row, fgParams(theme.Readable(auto.RGB(), theme.TerminalBg()))) {
		t.Errorf("the cleared row fell back to nothing instead of its automatic colour: %q", row)
	}

	// An accent nobody can parse is an unset one, not a blank one.
	m.SessionAccent = "chartreuse"
	if unknown, ok := m.SessionColor("main"); !ok || unknown != auto {
		t.Errorf("an unreadable accent left %v, not the automatic %v (ok=%v)", unknown, auto, ok)
	}

	// A hue the user claimed is not handed out again. Walk the vocabulary so
	// this holds for the hue each other session would otherwise have preferred,
	// not just for one that happened not to clash.
	for _, word := range []string{"bright red", "bright green", "bright yellow", "bright blue", "bright purple", "bright cyan"} {
		m.SessionAccent = word
		railStyled(t, m, tree)
		mine, _ := m.SessionColor("main")
		for _, other := range []string{"api", "docs"} {
			if theirs, _ := m.SessionColor(other); theirs == mine {
				t.Errorf("with main set to %s, %q was handed the same colour %v", word, other, theirs)
			}
		}
	}
}

// TestSessionColoursClearTheContrastFloor measures every automatic colour
// against every ground it can actually land on, on several themes. A hue from a
// theme's own sixteen against that theme's own background is legible for some
// themes and a smudge on others, and which is which is not a thing to settle by
// eye.
func TestSessionColoursClearTheContrastFloor(t *testing.T) {
	withSessionColors(t, true)
	m, _ := sessionColorOS(t, 120, 40)

	prev := theme.CurrentThemeID()
	t.Cleanup(func() { _ = theme.Initialize(prev) })

	// Every theme that ships, not a hand-picked few: the colours are the
	// theme's own sixteen against the theme's own background, so the pairing
	// that fails is the one nobody thought to check.
	names := []string{"main", "api", "docs", "work", "scratch", "deploy", "notes", "infra", "a"}
	themes := append([]string{""}, theme.AvailableThemes()...)
	if len(themes) < 10 {
		t.Fatalf("the theme registry came back with %d themes, so this proves nothing", len(themes))
	}
	t.Logf("measured %d session colours across %d themes", len(names)*3*len(themes), len(themes))
	for _, themeID := range themes {
		if err := theme.Initialize(themeID); err != nil {
			t.Fatalf("theme %q: %v", themeID, err)
		}
		pal := theme.UI()
		for _, ground := range []struct {
			what string
			bg   color.Color
		}{
			{"the rail's own ground", theme.TerminalBg()},
			{"the band under the pointer", pal.Surface},
			{"the switcher's selected row", pal.RowSel},
		} {
			for _, name := range names {
				ink := m.sessionTint(name, ground.bg)
				if ink == nil {
					t.Fatalf("theme %q: %q has no ink", themeID, name)
				}
				if got := theme.ContrastRatio(ink, ground.bg); got < theme.ContrastFloor {
					t.Errorf("theme %q: %q on %s measures %.2f:1, under the %.1f:1 floor",
						themeID, name, ground.what, got, theme.ContrastFloor)
				}
			}
		}
	}
}

// TestSessionColoursOffRendersWhatItAlwaysDid: the key off is not a dimmer, it
// is an off switch. Nothing the feature reads can move a cell, and the three
// marks it touches are back to the colours they had before it existed.
func TestSessionColoursOffRendersWhatItAlwaysDid(t *testing.T) {
	withSessionColors(t, false)
	m, tree := sessionColorOS(t, 120, 40)
	pal := theme.UI()

	before := strings.Join(railStyled(t, m, tree), "\n")

	// Every input the colours are derived from, moved at once.
	m.SessionAccent = "bright blue"
	m.SetWindowAccent("dddddddd4444", SlotAccent(4))
	if after := strings.Join(railStyled(t, m, tree), "\n"); after != before {
		t.Error("the frame moved while the colours were off")
	}

	// The attached session's gutter is the rail accent again, not a hue.
	if row := styledRow(t, railStyled(t, m, tree), "main"); !strings.Contains(row, fgParams(pal.Accent)) {
		t.Errorf("the attached session's gutter is not the rail accent: %q", row)
	}
	// A session at rest wears the muted dot it always wore.
	if row := styledRow(t, railStyled(t, m, tree), "docs"); !strings.Contains(row, fgParams(pal.FgMute)) {
		t.Errorf("a resting session row lost its muted dot: %q", row)
	}
	// And no row has grown a mark.
	if strings.Contains(stripANSIForTrace(before), accentMark()) {
		t.Errorf("an identity mark was drawn with the colours off:\n%s", stripANSIForTrace(before))
	}

	// The switcher, the other surface, is untouched too.
	m.SessionSwitcherItems = tree.Sessions
	out, _, _ := m.renderSessionSwitcher()
	if strings.Contains(stripANSIForTrace(out), accentMark()) {
		t.Errorf("the switcher grew an identity mark with the colours off:\n%s", out)
	}
}

// TestSessionColoursDegradeToShapeAndName documents what is left when the
// colour is not: the rail's own cells are unchanged, because the colour rides
// marks the rows already drew, and the one mark the feature adds is on the
// agent rows that live in another session, where it says "somewhere else" with
// no colour at all. The names, which are the identity the colour is shorthand
// for, are printed either way.
func TestSessionColoursDegradeToShapeAndName(t *testing.T) {
	m, tree := sessionColorOS(t, 120, 40)

	withSessionColors(t, false)
	off := railPlain(t, m, tree)
	config.SessionColors = true
	on := railPlain(t, m, tree)

	if len(off) != len(on) {
		t.Fatalf("the rail changed height: %d rows off, %d on", len(off), len(on))
	}
	for i := range on {
		if on[i] == off[i] {
			continue
		}
		// The only licensed difference is a mark in the gutter, which was blank.
		if strings.TrimPrefix(on[i], accentMark()) != strings.TrimPrefix(off[i], " ") {
			t.Errorf("row %d differs somewhere other than its gutter:\n on: %q\noff: %q", i, on[i], off[i])
			continue
		}
		// And it is only spent on a row that had no free cell to put the colour
		// in. A quiet dot in the glyph column is a free cell, so a row showing
		// one has no business claiming a second.
		if glyphCell(on[i]) == "·" {
			t.Errorf("row %d took a gutter cell while its dot was free: %q", i, on[i])
		}
	}

	// ASCII-only: the marks are the ASCII ones the rail already had, and the
	// name the colour is shorthand for is still printed beside them.
	prevASCII := config.UseASCIIOnly
	config.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.UseASCIIOnly = prevASCII
		overlay.SetASCII(prevASCII)
	})
	ascii := railPlain(t, m, tree)
	if got := gutterCell(styledRow(t, railStyled(t, m, tree), "main")); got != ">" {
		t.Errorf("the attached session's ASCII gutter mark is %q", got)
	}
	for _, want := range []string{"|", ".", "api/server"} {
		if !strings.Contains(strings.Join(ascii, "\n"), want) {
			t.Errorf("the ASCII rail is missing %q:\n%s", want, strings.Join(ascii, "\n"))
		}
	}
}

// TestSessionColourInputsAreInTheRailSignature: the rail's cache is keyed on
// everything it draws and on nothing it does not. The session accent is the one
// input nothing else was folding, and it is folded only while it is being drawn.
func TestSessionColourInputsAreInTheRailSignature(t *testing.T) {
	withSessionColors(t, true)
	m, _ := sessionColorOS(t, 120, 40)

	base := m.sidebarSignature()
	m.SessionAccent = "cyan"
	if m.sidebarSignature() == base {
		t.Error("an explicit session accent does not move the signature")
	}
	m.SessionAccent = ""
	if m.sidebarSignature() != base {
		t.Error("the signature did not come back when the accent was cleared")
	}

	config.SessionColors = false
	off := m.sidebarSignature()
	if off == base {
		t.Error("turning the colours off does not move the signature")
	}
	m.SessionAccent = "cyan"
	if m.sidebarSignature() != off {
		t.Error("an accent that draws nothing is folded into the signature")
	}
}

// TestSessionSwitcherRowsCarryTheRailsMark is the point of colouring the
// switcher at all: the row picked there is recognisably the row being looked at
// on the rail, in the same mark and the same colour.
func TestSessionSwitcherRowsCarryTheRailsMark(t *testing.T) {
	withSessionColors(t, true)
	m, tree := sessionColorOS(t, 120, 40)
	m.SessionSwitcherItems = tree.Sessions
	m.ShowSessionSwitcher = true
	pal := theme.UI()

	out, _, _ := m.renderSessionSwitcher()
	t.Logf("\n%s", out)
	rows := strings.Split(out, "\n")

	for i, name := range []string{"main", "api", "docs"} {
		// The selected row sits on its own band, so its ink is measured there.
		ground := pal.Surface
		if i == m.SessionSwitcherSelected {
			ground = pal.RowSel
		}
		row := styledRow(t, rows, name)
		if !strings.Contains(stripANSIForTrace(row), accentMark()) {
			t.Errorf("the %q row has no identity mark: %q", name, row)
		}
		if !strings.Contains(row, fgParams(m.sessionTint(name, ground))) {
			t.Errorf("the %q row's mark is not its session colour: %q", name, row)
		}
	}
}

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
