package tuie2e

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestDockTabsFlatStyle drives [dock.tabs] through the real binary. The
// session is one lone workspace, where the pill strip draws its "1" pill and a
// "+" pill and the flat strip is a status line: the tab alone, uncapped, with
// no "+" unless show_add asks for it. {icon} in the tab format reads the
// number_glyphs entry for the workspace: the first entry is workspace 1.
//
// Negative controls: with Icon indexing by n instead of n-1, workspace 1
// prints "two" and the dock never draws "one"; with the flat-strip rule in
// buildDockWorkspaceTabs removed (`&& (!flat || m.dockTabs().ShowAdd)`), the "flat" case fails with a
// "+" on the row; with the style gate in dockTab removed, it fails with caps.
// The "pill" and "show_add" cases are the positive halves: they prove a "+"
// can be on this row to be absent from it. "flat_caps" is a flat strip with
// caps = true, so it has no "+" and the caps of a pill.
func TestDockTabsFlatStyle(t *testing.T) {
	const format = "dock_workspace_tab_format = \"{icon}\"\n"
	for _, tc := range []struct {
		name   string
		config string
		flat   bool
		add    bool
	}{
		{"flat", "[appearance]\n" + format + "[dock.tabs]\nstyle = \"flat\"\nnumber_glyphs = [\"one\", \"two\"]\n", true, false},
		{"show_add", "[appearance]\n" + format + "[dock.tabs]\nstyle = \"flat\"\nshow_add = true\nnumber_glyphs = [\"one\", \"two\"]\n", true, true},
		{"flat_caps", "[appearance]\n" + format + "[dock.tabs]\nstyle = \"flat\"\ncaps = true\nnumber_glyphs = [\"one\", \"two\"]\n", false, false},
		{"pill", "[appearance]\n" + format + "[dock.tabs]\nnumber_glyphs = [\"one\", \"two\"]\n", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config)
			term := startIn(t, base, startOpts{cols: 120, rows: 30})
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(dockRow(s), "one")
			}, bootTimeout); err != nil {
				t.Fatalf("the dock never drew the {icon} tab: %v\n%s", err, term.Snapshot())
			}
			row := dockRow(term.Screen())
			if got := strings.Contains(row, "+"); got != tc.add {
				t.Fatalf("%s: \"+\" tab on the dock row = %v, want %v\n%s", tc.name, got, tc.add, row)
			}
			// The mode chip keeps its caps; the tab's would make a third pair.
			caps := dockCapsIn(row)
			if tc.flat && len(caps) > 2 {
				t.Fatalf("%s: flat tab drawn with caps %q\n%s", tc.name, caps, row)
			}
			if !tc.flat && len(caps) < 4 {
				t.Fatalf("%s: the tabs lost their caps %q\n%s", tc.name, caps, row)
			}
		})
	}
}

// twoWorkspaces leaves workspaces 1 and 2 with a window each, workspace 1
// current, and waits until the dock says so.
func twoWorkspaces(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	newWindow(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	waitDockWorkspace(t, term, 2)
	newWindow(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "1"); err != nil {
		t.Fatalf("switch to workspace 1: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "1:1")
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 1 never came back with its window: %v\n%s", err, term.Snapshot())
	}
}

// dockRowIndex is the screen row dockRow reads.
func dockRowIndex(s tuitest.Screen) int {
	_, rows := s.Size()
	for y := rows - 1; y >= 0; y-- {
		if strings.TrimRight(s.Line(y), " ") != "" {
			return y
		}
	}
	return -1
}

// TestDockTabsFlatStripTwoWorkspaces: a flat strip over two workspaces joins
// the tabs with [dock.tabs] separator, paints only the current tab in
// active_bg, shows zoom_marker on the current tab while its pane is zoomed,
// and a click on a tab (which sits past the separator, so it only lands if
// the hit rects moved with the separator's width) switches to that workspace.
//
// Negative controls: with dockTabGap returning dockWorkspacePillGap, the
// separator still draws but the hit rects lose its width, so the click lands
// off tab 2; with the `n == m.dockWorkspace()` gate dropped from
// workspaceTabText, the marker appears on the other tab too; with
// `zoom = tabs.ZoomMarker` blanked, it never appears; with the active branch
// of flatWorkspaceTab disabled, no tab is filled. The unzoomed and inactive
// assertions are the positive halves.
func TestDockTabsFlatStripTwoWorkspaces(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[appearance]
dock_workspace_tab_format = "{name}{zoom}~{icon}"

[dock.tabs]
style = "flat"
separator = " | "
active_bg = "#a7c080"
zoom_marker = "ZM"
number_glyphs = ["A"]
bell_marker = "!"
activity_marker = "#"
`)
	// Attached to a daemon, as tuios runs by default. The client streams
	// only the panes it shows, so the bell and output of workspace 1 reach
	// it only as the daemon's pane mark.
	term := startIn(t, base, startOpts{cols: 120, rows: 30, args: []string{"new", "e2e"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "1:0")
	}, bootTimeout); err != nil {
		t.Fatalf("the dock never drew: %v\n%s", err, term.Snapshot())
	}

	// A lone workspace is already one flat tab, with no separator and no "+".
	// Only the strip is read, up to the window readout: a daemon client
	// draws its own "+" for a new session at the right end of the bar.
	if row := dockRow(term.Screen()); strings.ContainsAny(row[:strings.Index(row, "1:0")], "|+") {
		t.Fatalf("a lone flat tab drew a separator or a \"+\": %s", row)
	}

	// {icon} is the first number_glyphs entry on workspace 1, and the number
	// itself on workspace 2, which the list does not reach.
	twoWorkspaces(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), " 1~A  |  2~2 ")
	}, uiTimeout); err != nil {
		t.Fatalf("the strip never joined the tabs with the separator: %v\n%s", err, term.Snapshot())
	}

	// Only the current tab is filled. The harness terminal quantises the
	// colour to its palette, so the fill is read as "not the default ground"
	// rather than as the configured RGB; inactive tabs set no inactive_bg.
	activeBg := func(s tuitest.Screen, col int) bool {
		return s.Cell(col, dockRowIndex(s)).Bg.Kind != tuitest.ColorDefault
	}
	row := dockRow(term.Screen())
	one, two := pillColumn(row, " 1~A  |"), pillColumn(row, "|  2~2")+3
	if !activeBg(term.Screen(), one) || activeBg(term.Screen(), two) {
		t.Fatalf("back on workspace 1: tab 1 filled = %v, tab 2 filled = %v\n%s",
			activeBg(term.Screen(), one), activeBg(term.Screen(), two), row)
	}
	if strings.Contains(row, "ZM") {
		t.Fatalf("no pane is zoomed, but the row carries the zoom marker: %s", row)
	}

	// Zooming marks the current tab only.
	if err := term.SendKeys(tuitest.Ctrl('b'), "z"); err != nil {
		t.Fatalf("zoom: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(dockRow(s), "1ZM~A")
	}, uiTimeout); err != nil {
		t.Fatalf("the zoomed workspace's tab never took the zoom marker: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(dockRow(term.Screen()), "2ZM") {
		t.Fatalf("the zoom marker is on a tab that is not current: %s", dockRow(term.Screen()))
	}

	// A click on tab 2 switches to it, and the fill follows.
	row = dockRow(term.Screen())
	// The click is on the tab's last column, where a hit rect that lost the
	// separator's width has already ended.
	leftClick(t, term, pillColumn(row, "2~2")+3, dockRowIndex(term.Screen()))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := dockRow(s)
		return strings.Contains(r, "2:1") && activeBg(s, pillColumn(r, "|  2~2")+3)
	}, uiTimeout); err != nil {
		t.Fatalf("a click on the second flat tab did not switch to workspace 2: %v\n%s", err, term.Snapshot())
	}

	// A bell and output on workspace 1 while workspace 2 is in view put
	// bell_marker and activity_marker on tab 1. Looking at it clears them.
	row = dockRow(term.Screen())
	leftClick(t, term, pillColumn(row, "1~A")+1, dockRowIndex(term.Screen()))
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(dockRow(s), "1:1") }, uiTimeout); err != nil {
		t.Fatalf("a click on the first tab did not switch back to workspace 1: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	sendKeys(t, term, "sleep 2; printf '\\a'; echo MARKED", tuitest.Enter)
	row = dockRow(term.Screen())
	leftClick(t, term, pillColumn(row, "2~2")+1, dockRowIndex(term.Screen()))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := dockRow(s)
		return strings.Contains(r, "2:1") && strings.Contains(r, "1~A!#")
	}, uiTimeout); err != nil {
		t.Fatalf("a bell and output on workspace 1 never marked its tab: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "marked")
	row = dockRow(term.Screen())
	leftClick(t, term, pillColumn(row, "1~A")+1, dockRowIndex(term.Screen()))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := dockRow(s)
		return strings.Contains(r, "1:1") && !strings.Contains(r, "!#")
	}, uiTimeout); err != nil {
		t.Fatalf("looking at workspace 1 did not clear its marks: %v\n%s", err, term.Snapshot())
	}
}

// TestDockTabsFlatColourFallbacks: a flat strip that sets no colour, or sets
// one that is not a colour, still says which workspace is current. The current
// tab sits on the palette's accent and the other is dim text on the bar's own
// ground. A bad value is dropped where it is drawn: handed to lipgloss as is,
// "a7c080" resolves to no colour, which paints the terminal's default over the
// tab, and the strip stops showing where you are.
//
// Negative controls: with tabColor returning lipgloss.Color(s) for any value,
// the bad_* cases fail with the current tab unfilled or the other one filled;
// with the accent fallback in flatWorkspaceTab (`bg = pal.Accent`) removed,
// every case fails with the current tab unfilled. The unset case is the
// positive half: it proves the fill the bad values must fall back to.
func TestDockTabsFlatColourFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, tabs string
	}{
		{"unset", ""},
		{"bad_active_bg", "active_bg = \"a7c080\"\n"},
		{"bad_active_fg", "active_fg = \"nope\"\n"},
		{"bad_inactive_bg", "inactive_bg = \"oops\"\n"},
		{"bad_inactive_fg", "inactive_fg = \"#12345\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, "[dock.tabs]\nstyle = \"flat\"\nseparator = \" | \"\n"+tc.tabs)
			term := startIn(t, base, startOpts{cols: 120, rows: 30})
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(dockRow(s), "1:0")
			}, bootTimeout); err != nil {
				t.Fatalf("the dock never drew: %v\n%s", err, term.Snapshot())
			}
			twoWorkspaces(t, term)
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(dockRow(s), " 1  |  2 ")
			}, uiTimeout); err != nil {
				t.Fatalf("the strip never drew two tabs: %v\n%s", err, term.Snapshot())
			}
			s := term.Screen()
			y, row := dockRowIndex(s), dockRow(s)
			cur, other := s.Cell(pillColumn(row, " 1  |")+1, y), s.Cell(pillColumn(row, "|  2 ")+3, y)
			if cur.Bg.Kind == tuitest.ColorDefault || cur.Fg.Kind == tuitest.ColorDefault {
				t.Errorf("the current tab is not marked: fg %+v bg %+v\n%s", cur.Fg, cur.Bg, row)
			}
			if other.Bg.Kind != tuitest.ColorDefault {
				t.Errorf("the other tab is filled: bg %+v\n%s", other.Bg, row)
			}
		})
	}
}

// TestDockTabsConfigWarnings: a [dock.tabs] with an unknown style and a value
// that is not a colour is reported at load, one problem each, and a valid one
// is not. caps = true on the pill style is a problem too: pills always have
// caps, so the key does nothing there. The count is what the notification says. The valid case writes a
// colour in each form: hex, a slot name, a role and an ANSI index.
//
// Negative controls: with the `validateDockColors(result, "dock.tabs", ...)`
// call removed from validateDock the bad case says "1 config problem"; with the
// style check removed it says "1 config problem" as well; with the caps check
// removed it says "2 config problems". The valid case is
// the positive half: the same keys, spelled right, raise nothing.
func TestDockTabsConfigWarnings(t *testing.T) {
	t.Run("bad", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, "[dock.tabs]\nstyle = \"round\"\nactive_bg = \"lime\"\ncaps = true\n")
		term := startIn(t, base, startOpts{cols: 120, rows: 30})
		if err := term.WaitForText("3 config problems", bootTimeout); err != nil {
			t.Fatalf("a bad style, a bad colour and caps on pills were not all reported: %v\n%s", err, term.Snapshot())
		}
	})
	t.Run("valid", func(t *testing.T) {
		base := t.TempDir()
		writeConfig(t, base, "[dock.tabs]\nstyle = \"flat\"\nactive_bg = \"#a7c080\"\n"+
			"inactive_fg = \"bright_black\"\nseparator_fg = \"muted\"\nactive_fg = \"231\"\n"+
			"caps = true\nactive_underline = false\n")
		term := startIn(t, base, startOpts{cols: 120, rows: 30})
		if err := term.WaitForText("1:0", bootTimeout); err != nil {
			t.Fatalf("the dock never drew: %v\n%s", err, term.Snapshot())
		}
		time.Sleep(time.Second)
		if strings.Contains(term.Screen().Text(), "config problem") {
			t.Fatalf("a valid [dock.tabs] was reported as a config problem\n%s", term.Snapshot())
		}
	})
}

// TestDockTabAttentionInk: [dock.tabs] attention_fg draws the tab of a
// workspace out of view in that ink while a pane on it waits for a person.
// The state is set the way a pane's shim sets it, through set-agent-state.
//
// How this could pass wrongly: the ink is one no default draws, read off the
// tab's own cells, and it must be absent before the state is set.
func TestDockTabAttentionInk(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[dock.tabs]\nstyle = \"flat\"\nattention_fg = \"#ff00ff\"\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 30, args: []string{"new", "e2e"}, env: []string{"COLORTERM=truecolor"}})
	killDaemon(t, base)
	waitBoot(t, term)
	newWindow(t, term)
	renameWindow(t, term, "AGENTPANE")
	sendKeys(t, term, tuitest.Ctrl('b'), "w", "2")
	waitDockWorkspace(t, term, 2)
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(dockRow(s), "2:1") }, uiTimeout); err != nil {
		t.Fatalf("workspace 2 never got its window: %v\n%s", err, term.Snapshot())
	}
	magenta := func(s tuitest.Screen) bool {
		row := dockRow(s)
		y := dockRowIndex(s)
		col := pillColumn(row, " 1 ")
		if col < 0 {
			return false
		}
		c := s.Cell(col+1, y)
		return c.Fg.Kind == tuitest.ColorRGB && c.Fg.R == 0xff && c.Fg.G == 0 && c.Fg.B == 0xff
	}
	if magenta(term.Screen()) {
		t.Fatalf("tab 1 wore attention_fg before any pane waited\n%s", term.SnapshotStyled())
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "e2e", "-w", "AGENTPANE"); err != nil {
		t.Fatalf("set-agent-state failed: %v\n%s", err, out)
	}
	if err := term.WaitFor(magenta, uiTimeout); err != nil {
		t.Fatalf("tab 1 never took attention_fg while its pane waited: %v\n%s", err, term.SnapshotStyled())
	}
	saveArtifact(t, term, artifactDir(t), "attention")
}

// TestDockTabsCapsAndColours: [dock.tabs] caps puts rounded ends on flat tabs,
// each drawn in its own tab's ground; the pill style takes the [dock.tabs]
// colours; and active_underline says whether the current tab is underlined,
// whatever the style. The colours are hex so the cells can be read exactly.
//
// Each case saves a frame. The "pill" and "flat" cases set no new key: their
// styled frames are the ones compared against a build from before the keys
// existed, to show the default strip did not change.
//
// Negative controls: with flatWorkspaceTab returning the tab without capTab,
// "flat_caps" fails with no cap beside the tabs; with workspacePill reading no
// [dock.tabs] colour, "pill_colours" fails on the current tab's ground; with
// the underline hard-coded on the active pill again, "pill_colours" fails on
// the underline. "pill" and "flat_underline" are the positive halves of the
// underline check.
func TestDockTabsCapsAndColours(t *testing.T) {
	const (
		head    = "[appearance]\ndock_workspace_tab_format = \"{icon}\"\n[dock.tabs]\nnumber_glyphs = [\"AA\", \"BB\"]\n"
		colours = "active_bg = \"#bd93f9\"\nactive_fg = \"#282a36\"\ninactive_bg = \"#44475a\"\ninactive_fg = \"#f8f8f2\"\n"
	)
	type rgb struct{ r, g, b uint8 }
	activeBg, activeFg, inactiveBg := rgb{0xbd, 0x93, 0xf9}, rgb{0x28, 0x2a, 0x36}, rgb{0x44, 0x47, 0x5a}
	is := func(c tuitest.Color, want rgb) bool {
		return c.Kind == tuitest.ColorRGB && c.R == want.r && c.G == want.g && c.B == want.b
	}
	for _, tc := range []struct {
		name, tabs string
		coloured   bool // the tabs carry the colours above
		capped     bool // each tab has a cap either side
		underline  bool // the current tab is underlined
	}{
		{"pill", "", false, true, true},
		{"flat", "style = \"flat\"\n", false, false, false},
		{"flat_caps", "style = \"flat\"\ncaps = true\nactive_bold = true\n" + colours, true, true, false},
		{"pill_colours", "active_underline = false\n" + colours, true, true, false},
		{"flat_underline", "style = \"flat\"\nactive_underline = true\n" + colours, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, head+tc.tabs)
			term := startIn(t, base, startOpts{cols: 100, rows: 12, env: []string{"COLORTERM=truecolor"}})
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return strings.Contains(dockRow(s), "AA")
			}, bootTimeout); err != nil {
				t.Fatalf("the dock never drew: %v\n%s", err, term.Snapshot())
			}
			twoWorkspacesAt(t, term, "BB")
			s := term.Screen()
			y, row := dockRowIndex(s), dockRow(s)
			a, b := pillColumn(row, "AA"), pillColumn(row, "BB")
			label := s.Cell(a, y)
			if tc.coloured && (!is(label.Bg, activeBg) || !is(label.Fg, activeFg)) {
				t.Errorf("the current tab is not drawn in active_fg on active_bg: fg %+v bg %+v\n%s", label.Fg, label.Bg, row)
			}
			if tc.coloured && !is(s.Cell(b, y).Bg, inactiveBg) {
				t.Errorf("the other tab is not on inactive_bg: %+v\n%s", s.Cell(b, y).Bg, row)
			}
			if label.Underline != tc.underline {
				t.Errorf("the current tab underlined = %v, want %v\n%s", label.Underline, tc.underline, term.SnapshotStyled())
			}
			// A tab is " AA ": its caps sit a column outside the padding.
			for _, c := range []struct {
				col    int
				ground rgb
			}{{a - 2, activeBg}, {a + 3, activeBg}, {b - 2, inactiveBg}, {b + 3, inactiveBg}} {
				cell := s.Cell(c.col, y)
				capped := slices.Contains(dockCapGlyphs, cell.Content)
				if capped != tc.capped {
					t.Errorf("column %d holds %q, want a cap = %v\n%s", c.col, cell.Content, tc.capped, row)
					continue
				}
				if capped && tc.coloured && !is(cell.Fg, c.ground) {
					t.Errorf("the cap at column %d is %+v, not its tab's ground\n%s", c.col, cell.Fg, row)
				}
			}
			dir := artifactDir(t)
			saveArtifact(t, term, dir, "dock")
			savePNG(t, s, shot.XTermPalette(), dir, "dock")
		})
	}
}

// twoWorkspacesAt is twoWorkspaces for a strip whose labels are not the
// workspace numbers: it waits until the dock draws the second label.
func twoWorkspacesAt(t *testing.T, term *tuitest.Terminal, second string) {
	t.Helper()
	newWindow(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "w", "2")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(dockRow(s), second) }, uiTimeout); err != nil {
		t.Fatalf("workspace 2 never reached the dock: %v\n%s", err, term.Snapshot())
	}
	newWindow(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "w", "1")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(dockRow(s), "1:1") }, uiTimeout); err != nil {
		t.Fatalf("workspace 1 never came back with its window: %v\n%s", err, term.Snapshot())
	}
}
