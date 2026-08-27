package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// withSections sets the rail's layout for one test and puts it back after.
func withSections(t *testing.T, spec string) {
	t.Helper()
	prev := config.SidebarSections
	config.SidebarSections = spec
	t.Cleanup(func() { config.SidebarSections = prev })
}

// TestRailLayoutOrdersAndSelectsSections is the layout option, checked on the
// drawn rail rather than on the parse: the order the sections are listed in is
// the order they are stacked, and a section left out is not drawn at all.
//
// Negative control, confirmed red: iterate the section enum instead of the
// plans in the draw loop, and both the order case and the omitted case fail.
func TestRailLayoutOrdersAndSelectsSections(t *testing.T) {
	dir := fileViewTree(t)

	rowOf := func(lines []string, label string) int {
		for i, ln := range lines {
			if strings.HasPrefix(ln, " "+label) {
				return i
			}
		}
		return -1
	}

	t.Run("order", func(t *testing.T) {
		withSections(t, "files:30,agents:20,sessions:20,terminals")
		m := sidebarTestOS(t, 120, 40, "left")
		openFilesOn(t, m, dir)
		lines := railLines(t, m)
		files, agents := rowOf(lines, "files"), rowOf(lines, "agents")
		sessions, terminals := rowOf(lines, "sessions"), rowOf(lines, "terminals")
		for _, h := range []struct {
			name string
			row  int
		}{{"files", files}, {"agents", agents}, {"sessions", sessions}, {"terminals", terminals}} {
			if h.row < 0 {
				t.Fatalf("the rail drew no %s header:\n%s", h.name, strings.Join(lines, "\n"))
			}
		}
		if !(files < agents && agents < sessions && sessions < terminals) {
			t.Errorf("the sections are stacked files=%d agents=%d sessions=%d terminals=%d, want that order:\n%s",
				files, agents, sessions, terminals, strings.Join(lines, "\n"))
		}
	})

	t.Run("omitted", func(t *testing.T) {
		withSections(t, "sessions,files")
		m := sidebarTestOS(t, 120, 40, "left")
		openFilesOn(t, m, dir)
		out := strings.Join(railLines(t, m), "\n")
		if !strings.Contains(out, " sessions") || !strings.Contains(out, " files") {
			t.Fatalf("a two-section layout dropped one of the two it names:\n%s", out)
		}
		if strings.Contains(out, " terminals") || strings.Contains(out, " agents") {
			t.Errorf("a layout that names neither still drew one of them:\n%s", out)
		}
		// A section that is not drawn also publishes no rows for the mouse or
		// the keyboard to land on, or the rail would hold click targets for
		// lines it never painted.
		for _, h := range m.SidebarHits {
			if h.Kind == sidebarRowWindow || h.Kind == sidebarRowAgent {
				t.Errorf("a section left out of the layout published a %v rectangle", h.Kind)
			}
		}
	})
}

// TestRailSectionsNeverOverlapAtAnyHeight is the invariant the layout work
// earlier this week was opened for, applied to a rail that now has four
// sections and a configurable split: at every height, for every layout, the
// bands the sections are placed in are disjoint and inside the rail.
//
// Two sections landing on the same line is the failure mode: the second one
// paints over the first, and the hit rectangles recorded on those lines send a
// click to whichever of them the loop published last.
//
// Negative control, confirmed red: drop the clamp that holds each band inside
// the rail's own rows, and this fails at height 4 with a band at 2..3 on a rail
// that has two lines.
//
// The control that was tried first (removing the give-up loop from
// sidebarBudgetLines) leaves this green, because the clamp catches an
// overrunning budget as well. That budget is measured directly by
// TestBudgetNeverOverrunsOrInvents, which does go red for it. Two defences, one
// test each.
func TestRailSectionsNeverOverlapAtAnyHeight(t *testing.T) {
	dir := fileViewTree(t)
	for _, spec := range []string{
		config.SidebarDefaultSections,
		"files:60,sessions:20,terminals,agents:20",
		"terminals,files",
		"agents:50,files:50,sessions,terminals",
		"sessions:90,terminals:90,files:90,agents:90",
	} {
		for h := 4; h <= 40; h++ {
			withSections(t, spec)
			m := sidebarTestOS(t, 120, h, "left")
			openFilesOn(t, m, dir)
			lines, w := m.sidebarPanelLines()
			if len(lines) != m.GetUsableHeight() {
				t.Fatalf("%s at height %d drew %d rows, want %d", spec, h, len(lines), m.GetUsableHeight())
			}
			top, bottom := m.GetTopMargin(), m.GetTopMargin()+m.GetUsableHeight()
			var bands [][2]int
			for _, band := range m.sidebarSectionY {
				if band[1] <= band[0] {
					continue
				}
				if band[0] < top || band[1] > bottom {
					t.Fatalf("%s at height %d placed a band at %v, outside the rail's %d..%d",
						spec, h, band, top, bottom)
				}
				bands = append(bands, band)
			}
			for i := range bands {
				for j := i + 1; j < len(bands); j++ {
					a, b := bands[i], bands[j]
					if a[0] < b[1] && b[0] < a[1] {
						t.Fatalf("%s at height %d overlaps bands %v and %v", spec, h, a, b)
					}
				}
			}
			// And every recorded rectangle is on a line the rail drew.
			for _, hit := range m.SidebarHits {
				if hit.Y0 < top || hit.Y1 > bottom {
					t.Fatalf("%s at height %d recorded a rectangle at %d..%d, outside %d..%d",
						spec, h, hit.Y0, hit.Y1, top, bottom)
				}
				if hit.X1 > hit.X0+w {
					t.Fatalf("%s at height %d recorded a rectangle %d cells wide on a %d-cell rail",
						spec, h, hit.X1-hit.X0, w)
				}
			}
		}
	}
}

// TestBudgetNeverOverrunsOrInvents sweeps the allocator directly, which is
// where the arithmetic that the render then trusts actually lives.
//
// Two claims: the sections never claim more lines than the rail has, and never
// claim lines for rows that do not exist. The first is what keeps two sections
// off the same line; the second is what keeps a section from drawing a blank
// row that a click lands nowhere in.
//
// Negative control, confirmed red: remove the per-section row cap after the
// ladder, and the "claims lines for rows that do not exist" case fails on the
// flexible section as soon as the shares leave it more lines than it has rows.
func TestBudgetNeverOverrunsOrInvents(t *testing.T) {
	plans := []sidebarSectionPlan{
		{Section: sidebarSectionSessions, Share: 25},
		{Section: sidebarSectionTerminals},
		{Section: sidebarSectionFiles, Share: 25},
		{Section: sidebarSectionAgents, Share: 34},
	}
	for _, agentH := range []int{1, sidebarAgentRowTall} {
		rowH := []int{1, 1, 1, agentH}
		for avail := 0; avail <= 24; avail++ {
			for nS := 0; nS <= 5; nS++ {
				for nT := 0; nT <= 5; nT++ {
					for nF := 0; nF <= 5; nF++ {
						for nA := 0; nA <= 5; nA++ {
							rows := []int{nS, nT, nF, nA}
							out := sidebarBudgetLines(avail, plans, rows, rowH)
							total := 0
							for i, n := range out {
								if n < 0 {
									t.Fatalf("budget(%d, %v, h=%d) = %v has a negative section", avail, rows, agentH, out)
								}
								if n > rows[i]*rowH[i] {
									t.Fatalf("budget(%d, %v, h=%d) = %v claims lines for rows that do not exist",
										avail, rows, agentH, out)
								}
								total += n
							}
							if total > avail {
								t.Fatalf("budget(%d, %v, h=%d) = %v overruns the rail", avail, rows, agentH, out)
							}
						}
					}
				}
			}
		}
	}
}

// TestASpareRailIsSpentOnTheSectionThatWantsIt. A share is a ceiling, not a
// reservation: lines nobody claimed go to a section that still has names below
// its fold, rather than being left blank.
//
// This is the difference between a listing of two hundred names showing eight
// of them over thirty empty lines and one that fills the rail.
//
// Negative control, confirmed red: drop the grow pass from sidebarBudgetLines
// and the section stays at its quarter share with the rail half empty.
func TestASpareRailIsSpentOnTheSectionThatWantsIt(t *testing.T) {
	plans := []sidebarSectionPlan{
		{Section: sidebarSectionSessions, Share: 25},
		{Section: sidebarSectionTerminals},
		{Section: sidebarSectionFiles, Share: 25},
		{Section: sidebarSectionAgents, Share: 34},
	}
	rowH := []int{1, 1, 1, 1}

	// One session, two panes, a hundred names, no agents, forty lines. The
	// listing is the only thing with rows left, so it gets what the other three
	// cannot use.
	out := sidebarBudgetLines(40, plans, []int{1, 2, 100, 0}, rowH)
	if out[0] != 1 || out[1] != 2 || out[3] != 0 {
		t.Fatalf("the small sections were not given their own rows: %v", out)
	}
	if out[2] != 37 {
		t.Errorf("the listing was given %d lines of the 37 nothing else wanted: %v", out[2], out)
	}

	// And a rail where every list fits keeps its gap, which is what leaves the
	// pinned block floating at the bottom.
	out = sidebarBudgetLines(40, plans, []int{1, 2, 3, 0}, rowH)
	if got := out[0] + out[1] + out[2] + out[3]; got != 6 {
		t.Errorf("a rail with six rows of content claimed %d lines: %v", got, out)
	}

	// Two hungry sections split what is left rather than the first taking all.
	out = sidebarBudgetLines(20, plans, []int{1, 100, 100, 0}, rowH)
	if out[1] < 5 || out[2] < 5 {
		t.Errorf("one hungry section starved the other: %v", out)
	}

	// The last section does not grow, whatever it has left below its fold. It is
	// the one pinned to the rail's bottom edge, and the gap above it is what
	// makes it read as a block rather than as the end of the list over it.
	out = sidebarBudgetLines(40, plans, []int{1, 2, 0, 100}, rowH)
	if out[3] > 40*34/100 {
		t.Errorf("the pinned section grew past its share to %d lines: %v", out[3], out)
	}
	if got := out[0] + out[1] + out[2] + out[3]; got >= 40 {
		t.Errorf("the pinned section swallowed the gap above it: %v", out)
	}
}

// TestFileIconsDegrade is the "it must degrade" half of the icon work: the nerd
// font layer is the top one, and underneath it are three glyph set roles with
// ASCII spellings of their own.
//
// The expected marks are written out from the glyph set, not read back from the
// function under test.
//
// Negative controls, both confirmed red: drop the fileIconsOn test from
// fileRowMark and the ASCII case draws private-use codepoints at a terminal
// that cannot show them; drop the UseASCII clause from fileIconsOn and the same
// case fails while the option case still passes.
func TestFileIconsDegrade(t *testing.T) {
	type row struct {
		name    string
		dir     bool
		parent  bool
		wantPUA bool
	}
	rows := []row{
		{name: "main.go", wantPUA: true},
		{name: "src", dir: true, wantPUA: true},
		{name: "..", dir: true, parent: true, wantPUA: true},
		{name: "nothing.unknownext", wantPUA: true},
	}

	// On, with a font: every row wears a private use codepoint, and a folder,
	// a parent and a file wear three different ones.
	seen := map[string]string{}
	for _, r := range rows {
		got := fileRowGlyphFor(r.name, r.dir, r.parent)
		if !isPUA(got) {
			t.Errorf("with icons on, %q drew %q, which is not a nerd font icon", r.name, got)
		}
		seen[r.name] = got
	}
	if seen["src"] == seen[".."] || seen["src"] == seen["main.go"] {
		t.Errorf("a folder, a parent and a file share an icon: %q %q %q",
			seen["src"], seen[".."], seen["main.go"])
	}
	if seen["main.go"] == seen["nothing.unknownext"] {
		t.Error("a known and an unknown extension drew the same icon")
	}

	// Off, and in ASCII, the three glyph set roles draw instead.
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
		want  [3]string
	}{
		{"option off", func(t *testing.T) {
			prev := config.SidebarFileIcons
			config.SidebarFileIcons = false
			t.Cleanup(func() { config.SidebarFileIcons = prev })
		}, [3]string{"▸", "▴", "·"}},
		{"ascii", func(t *testing.T) {
			prevCfg := config.UseASCIIOnly
			config.UseASCIIOnly = true
			overlay.SetASCII(true)
			t.Cleanup(func() {
				config.UseASCIIOnly = prevCfg
				overlay.SetASCII(false)
			})
		}, [3]string{">", "^", "."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			got := [3]string{
				fileRowGlyphFor("src", true, false),
				fileRowGlyphFor("..", true, true),
				fileRowGlyphFor("main.go", false, false),
			}
			if got != tc.want {
				t.Errorf("folder/parent/file drew %q, want %q", got, tc.want)
			}
		})
	}

	// And the glyph column can be switched off entirely, which is the option
	// every other row on the rail already answers to.
	prev := config.SidebarShowGlyphs
	config.SidebarShowGlyphs = false
	t.Cleanup(func() { config.SidebarShowGlyphs = prev })
	if got := fileRowGlyphFor("main.go", false, false); got != " " {
		t.Errorf("with the rail's glyphs off a file row drew %q, want a blank cell", got)
	}
}

// fileRowGlyphFor is the mark a name draws in the glyph column, resolved the
// way the listing resolves it and then drawn the way a row draws it. The two
// halves are separate in the app because the lookup happens once per file and
// the draw happens once per frame; the tests below are about the pair.
func fileRowGlyphFor(name string, dir, parent bool) string {
	return fileRowMark(fileIconFor(name, dir), dir, parent).Glyph
}

// isPUA reports whether every rune of s is in a private use area, which is
// where nerd font icons live and where nothing a plain font can draw does.
func isPUA(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 0xe000 && r <= 0xf8ff) && r < 0xf0000 {
			return false
		}
	}
	return true
}

// TestEveryFileIconIsOneCell. Every rail row budgets exactly one column for its
// glyph, so a two-cell icon moves the name beside it and puts the row's click
// target under a different column than the one the pointer is tested against.
// The icon table is generated upstream and a bump can put anything in it, so
// fileIconFit is what keeps the promise; this is the check that it does.
//
// Negative controls, both confirmed red: return icon unchanged from
// fileIconFit, and the wide-glyph case draws two cells; drop the Glyph == ""
// arm of fileRowMark and the same case draws nothing at all.
func TestEveryFileIconIsOneCell(t *testing.T) {
	// A glyph the layout cannot place is dropped, and the row falls back to a
	// mark it can. "🚀" is two cells in every width table there is.
	for _, tc := range []struct{ name, glyph string }{
		{"a two-cell glyph", "🚀"},
		{"two glyphs", ""},
		{"no glyph", ""},
	} {
		if got := fileIconFit(fileIcon{Glyph: tc.glyph, Hex: "#FF0000"}); got.Glyph != "" || got.Hex != "" {
			t.Errorf("%s survived the fit as %+v, want it dropped", tc.name, got)
		}
	}
	// And the pair together: a dropped icon leaves the row a mark it can place,
	// rather than an empty glyph column that would move the name.
	wide := fileRowMark(fileIconFit(fileIcon{Glyph: "🚀", Hex: "#FF0000"}), false, false)
	if lipgloss.Width(wide.Glyph) != 1 {
		t.Errorf("a row whose icon was dropped drew %q, %d cells wide", wide.Glyph, lipgloss.Width(wide.Glyph))
	}

	// And every mark the section actually draws is measured, through the same
	// pair of calls the app makes.
	for _, name := range []string{
		"main.go", "lib.rs", "a.py", "a.ts", "a.tsx", "a.svg", "a.txt", "a.log",
		"a.zst", "a.pdf", "a.mp4", "a.so", "Makefile", "Dockerfile", ".gitignore",
		"LICENSE", "src", "..", "unknown.zzz", "no-extension",
	} {
		dir := name == "src" || name == ".."
		if got := lipgloss.Width(fileRowGlyphFor(name, dir, name == "..")); got != 1 {
			t.Errorf("the glyph column for %q came out %d cells wide", name, got)
		}
	}
}

// TestALayoutStringIsForgivingAndSaysWhy. The rail parses the layout on every
// frame it rebuilds, so an unusable string has to leave it drawing something;
// the validator is where the person who typed it is told what happened.
//
// Negative control, confirmed red: return the parsed list unchanged when it is
// empty, and the "nonsense" case draws a rail with no sections at all.
func TestALayoutStringIsForgivingAndSaysWhy(t *testing.T) {
	for _, tc := range []struct {
		spec      string
		wantNames []string
		wantWhy   string
	}{
		{"sessions,nosuchsection,files", []string{"sessions", "files"}, "no rail section"},
		{"sessions,sessions,files", []string{"sessions", "files"}, "listed twice"},
		{"sessions:abc,files", []string{"sessions", "files"}, "not a whole number"},
		{"sessions:400,files", []string{"sessions", "files"}, "clamped"},
		{"nonsense", nil, "no rail section"},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			got := config.ParseSidebarSections(tc.spec)
			if tc.wantNames != nil {
				var names []string
				for _, e := range got {
					names = append(names, e.Name)
				}
				if strings.Join(names, ",") != strings.Join(tc.wantNames, ",") {
					t.Errorf("parsed %v, want %v", names, tc.wantNames)
				}
			} else if len(got) == 0 {
				t.Error("an unusable layout left the rail with no sections at all")
			}
			why := strings.Join(config.SidebarSectionProblems(tc.spec), "\n")
			if !strings.Contains(why, tc.wantWhy) {
				t.Errorf("the validator said %q, which does not mention %q", why, tc.wantWhy)
			}
		})
	}

	// A share out of range is clamped rather than dropped, so the section keeps
	// its place in the order.
	got := config.ParseSidebarSections("sessions:400,files")
	if got[0].Share != 100 {
		t.Errorf("a share of 400 percent came through as %d", got[0].Share)
	}
	if got[1].Share != 0 {
		t.Errorf("a section with no share is not flexible: %d", got[1].Share)
	}
}

// TestAStaleListingIsDropped is the generation guard, which is the whole reason
// the read can be asynchronous at all.
//
// A read of a directory on a mount that has stopped answering can come back
// long after the user moved on, and applying it would replace the listing they
// are looking at with one they left. Comparing the path would not be enough:
// walking out of a folder and straight back into it is the same path twice.
//
// Negative control, confirmed red: drop the generation test from HandleFileList
// and the late reply overwrites the listing.
func TestAStaleListingIsDropped(t *testing.T) {
	root := fileViewTree(t)
	m := &OS{}
	m.filesView.Show = 1

	// A read is asked for and its answer is held back.
	slow := m.requestFileList(root, "", true)
	if slow == nil {
		t.Fatal("no read was scheduled")
	}
	stale := slow().(fileListMsg)

	// The user walks somewhere else, and that answer lands first.
	m.loadFileViewNow(t, root+"/apple")
	want := m.FileViewDir()

	m.HandleFileList(stale)
	if got := m.FileViewDir(); got != want {
		t.Errorf("a late reply moved the listing to %q; it was showing %q", got, want)
	}
	if len(m.filesView.Entries) != 0 {
		t.Errorf("a late reply put %d names from the old directory on screen", len(m.filesView.Entries))
	}
}
