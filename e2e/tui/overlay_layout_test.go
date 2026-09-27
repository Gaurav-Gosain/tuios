package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The layout of the overlays that list keys and commands: the command palette
// under category headers, the which-key menu in sections and columns, key-hint
// footers that shorten rather than wrap, and empty states that say why a list
// is empty in the middle of it.
//
// Every step saves the frame as text, styled text and a PNG drawn by tuios's
// own renderer (internal/shot), at 120x40 and 80x24, on a dark terminal and
// under a light theme, and at 256 and 16 colours at 120x40.

// overlayLayoutRun is one terminal the layouts are drawn in.
type overlayLayoutRun struct {
	name       string
	cols, rows int
	theme      string
	env        []string
}

func overlayLayoutRuns() []overlayLayoutRun {
	var runs []overlayLayoutRun
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		for _, look := range chromeLooks {
			runs = append(runs, overlayLayoutRun{
				name: look.name + "-" + itoa(size[0]) + "x" + itoa(size[1]) + "-truecolor",
				cols: size[0], rows: size[1], theme: look.theme,
				env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"},
			})
		}
	}
	for _, d := range chromeDepths[:2] {
		for _, look := range chromeLooks {
			runs = append(runs, overlayLayoutRun{
				name: look.name + "-120x40-" + d.name,
				cols: 120, rows: 40, theme: look.theme, env: d.env,
			})
		}
	}
	return runs
}

// span is the cells [from, to) of line, padded with blanks: a screen line
// comes back with its trailing blanks trimmed.
func span(line string, from, to int) string {
	r := []rune(line)
	for len(r) < to {
		r = append(r, ' ')
	}
	return string(r[from:to])
}

// runeCol is the cell column of the first needle in line, or -1. The overlays
// here draw only single-width glyphs, so a rune is a cell.
func runeCol(line, needle string) int {
	i := strings.Index(line, needle)
	if i < 0 {
		return -1
	}
	return len([]rune(line[:i]))
}

// TestOverlayLayouts opens the palette, the which-key menu and an empty list
// in each run and holds each to its layout.
//
//   - Palette, nothing typed: the first list row is the "Run" header, set at
//     the body's left edge; every command name starts two cells in, on one
//     column; no row carries a bracketed category tag.
//   - Palette, "win" typed: no header rows and no tags; the category is in a
//     column on the right.
//   - Palette, no match: "No matching commands" is centred in the body with
//     "esc close" under it.
//   - Which-key: sections with headings, at least two headings on one row, the
//     submenus marked with a leading +, no "..." anywhere, and no "+N more" at
//     either size. At 120x40 the menu takes at most 16 rows, where it took 36
//     as one column.
//   - Which-key at 16 colours: a hairline frame round it. Its surface is the
//     ground there, and without the frame the panel had no edge and cut the
//     window border under it off mid-line.
//
// How this could pass wrongly, written down first:
//   - The palette could be found on a stale frame. Each step waits for text
//     only that state draws: "Run a program" under the "Run" header, a
//     category word right of "win" matches, the empty message.
//   - A header could be mistaken for a command and a command for a header.
//     Headers are found by their exact category text at the body's edge, and
//     names by their known text.
//   - The which-key height could be measured on a partial frame. It is read
//     once "+Workspace" and "Help" are both on screen, a middle column and the
//     last one. The headings are not waited for, so a menu drawn without them
//     fails the heading check rather than the wait.
//
// Negative controls (NEGATIVE_CONTROLS.md): with paletteGrouped always false,
// the header check fails; with the prefix menu handed over as one untitled
// group, the shared-row check fails; with the list overlay's empty state back
// to a line at the top left, the centring check fails; without FrameBlock,
// the 16 colour frame check fails.
func TestOverlayLayouts(t *testing.T) {
	for _, run := range overlayLayoutRuns() {
		t.Run(run.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			useShippedLooks(base)
			if run.theme != "" {
				writeConfig(t, base, "[appearance]\ntheme = \""+run.theme+"\"\n")
			}
			if out, err := tuiosCLI(t, base, "new", "e2e-layout", "--detach"); err != nil {
				t.Fatalf("create session: %v\n%s", err, out)
			}
			term := startIn(t, base, startOpts{cols: run.cols, rows: run.rows,
				args: []string{"attach", "e2e-layout"}, shippedLooks: true, env: run.env})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			dir := artifactDir(t)
			host := hostPalette(t, run.theme)
			shot := func(name string) {
				t.Helper()
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("%s: the screen never settled: %v", name, err)
				}
				saveArtifact(t, term, dir, name)
				savePNG(t, term.Screen(), host, dir, name)
			}

			// The palette with nothing typed.
			sendKeys(t, term, legacyCtrlP)
			waitScreen(t, term, "the palette never opened", paletteTitle, "Run a program")
			shot("palette-grouped")
			checkPaletteGrouped(t, term)

			// Filtering.
			sendKeys(t, term, "w", "i", "n")
			waitScreen(t, term, "the palette never filtered", "win█", "Navigation")
			shot("palette-filtered")
			checkPaletteFiltered(t, term)

			// Nothing matches.
			sendKeys(t, term, "q", "q", "q")
			waitScreen(t, term, "the palette never emptied", "No matching commands")
			shot("palette-empty")
			checkCentredEmpty(t, term, "No matching commands", "esc close")
			closePalette(t, term, "after the empty state")

			// Which-key.
			sendKeys(t, term, tuitest.Ctrl('b'))
			waitScreen(t, term, "which-key never drew", "prefix", "+Workspace", "Help")
			shot("which-key")
			checkWhichKey(t, term, run.cols >= 120)
			if strings.HasSuffix(run.name, "-16") {
				checkWhichKeyFramed(t, term)
			}
			sendKeys(t, term, tuitest.Esc)
			waitGone(t, term, "which-key", "+Workspace")

			// An empty list overlay: the session switcher with a query nothing
			// matches.
			sendKeys(t, term, tuitest.Ctrl('b'), "S")
			waitScreen(t, term, "the session switcher never opened", "e2e-layout")
			sendKeys(t, term, "q", "q", "q", "q")
			waitScreen(t, term, "the switcher never emptied", "No match")
			shot("switcher-empty")
			checkCentredEmpty(t, term, "No match", "create it")
			sendKeys(t, term, tuitest.Esc)
			waitGone(t, term, "the session switcher", "No match")
			t.Logf("frames in %s", dir)
		})
	}
}

// paletteBody is the palette's body edge and its first list row.
func paletteBody(t *testing.T, term *tuitest.Terminal) (s tuitest.Screen, left, right, first, last int) {
	t.Helper()
	s = term.Screen()
	title := rowWith(s, paletteTitle)
	if title < 0 {
		t.Fatalf("no palette on screen\n%s", term.Snapshot())
	}
	col, w := longestRuleRun(s.Line(title + 3))
	if w < 20 {
		t.Fatalf("no search rule under the palette's title\n%s", term.Snapshot())
	}
	count := -1
	_, rows := s.Size()
	for y := title + 4; y < rows; y++ {
		if strings.Contains(s.Line(y), " commands") || strings.TrimSpace(span(s.Line(y), col, col+w)) == "" {
			count = y
			break
		}
	}
	if count < 0 {
		t.Fatalf("no end to the palette's list\n%s", term.Snapshot())
	}
	return s, col, col + w, title + 4, count - 1
}

func checkPaletteGrouped(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	s, left, right, first, last := paletteBody(t, term)
	cell := func(y int) string { return span(s.Line(y), left, right) }
	if got := strings.TrimSpace(cell(first)); got != "Run" {
		t.Fatalf("the palette's first row is %q, want the Run header\n%s", got, term.Snapshot())
	}
	headers := 0
	for y := first; y <= last; y++ {
		row := cell(y)
		if strings.Contains(row, "[") {
			t.Errorf("row %d carries a bracketed tag: %q\n%s", y, row, term.Snapshot())
		}
		if !strings.HasPrefix(row, " ") {
			headers++ // a header starts at the edge
			continue
		}
		// A command: the marker cells, then the name on the third cell.
		r := []rune(row)
		if len(r) < 3 || r[2] == ' ' {
			t.Errorf("row %d does not start its name on the name column: %q\n%s", y, row, term.Snapshot())
		}
	}
	if headers < 2 {
		t.Errorf("the grouped palette shows %d headers, want at least two\n%s", headers, term.Snapshot())
	}
}

func checkPaletteFiltered(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	s, left, right, first, last := paletteBody(t, term)
	categories := 0
	for y := first; y <= last; y++ {
		row := span(s.Line(y), left, right)
		if strings.Contains(row, "[") {
			t.Errorf("row %d carries a bracketed tag: %q\n%s", y, row, term.Snapshot())
		}
		r := []rune(row)
		if len(r) < 3 || r[2] == ' ' || !strings.HasPrefix(row, "  ") && !strings.HasPrefix(row, "› ") {
			t.Errorf("row %d is not a command on the name column: %q\n%s", y, row, term.Snapshot())
		}
		for _, c := range []string{"Window", "Navigation", "Layout", "Session"} {
			if strings.HasSuffix(strings.TrimRight(row, " "), c) {
				categories++
				break
			}
		}
	}
	if categories == 0 {
		t.Errorf("no filtered row names its category on the right\n%s", term.Snapshot())
	}
}

// checkCentredEmpty holds an empty state to its layout: the message centred
// between the panel's rule ends, and the hint under it, centred too.
func checkCentredEmpty(t *testing.T, term *tuitest.Terminal, message, hint string) {
	t.Helper()
	s := term.Screen()
	y := rowWith(s, message)
	if y < 0 {
		t.Fatalf("no %q on screen\n%s", message, term.Snapshot())
	}
	// The panel's width is its footer rule's, the longest run of rule glyphs
	// below the message.
	_, rows := s.Size()
	left, width := -1, 0
	for r := y; r < rows; r++ {
		if col, w := longestRuleRun(s.Line(r)); w > width {
			left, width = col, w
		}
	}
	if width < 20 {
		t.Fatalf("no rule under %q\n%s", message, term.Snapshot())
	}
	line := s.Line(y)
	text := strings.TrimSpace(span(line, left, left+width))
	start := runeCol(line, text)
	mid := start + len([]rune(text))/2
	if d := mid - (left + width/2); d < -1 || d > 1 {
		t.Errorf("%q is centred on column %d, the body's middle is %d\n%s", text, mid, left+width/2, term.Snapshot())
	}
	hy := -1
	for r := y + 1; r < min(y+4, rows); r++ {
		if strings.Contains(span(s.Line(r), left, left+width), hint) {
			hy = r
			break
		}
	}
	if hy < 0 {
		t.Fatalf("no %q under %q\n%s", hint, message, term.Snapshot())
	}
	hline := s.Line(hy)
	hstart := runeCol(hline, hint)
	if d := hstart + len(hint)/2 - (left + width/2); d < -1 || d > 1 {
		t.Errorf("the hint %q is centred on column %d, the body's middle is %d\n%s", hint, hstart+len(hint)/2, left+width/2, term.Snapshot())
	}
}

func checkWhichKey(t *testing.T, term *tuitest.Terminal, wide bool) {
	t.Helper()
	s := term.Screen()
	text := s.Text()
	if strings.Contains(text, "...") {
		t.Errorf("which-key still marks submenus with a trailing ...\n%s", term.Snapshot())
	}
	if strings.Contains(text, " more") {
		t.Errorf("which-key left keys out\n%s", term.Snapshot())
	}
	for _, sub := range []string{"+Workspace", "+Minimize", "+Window", "+Layout", "+Tape", "+Debug"} {
		if !strings.Contains(text, sub) {
			t.Errorf("which-key does not mark the %s submenu\n%s", sub, term.Snapshot())
		}
	}
	top := rowWith(s, "prefix")
	_, rows := s.Size()
	bottom := top
	for y := top; y < rows; y++ {
		for _, w := range []string{"Create window", "Equalize splits", "Quit", "Focus sidebar", "+Debug", "Help"} {
			if strings.Contains(s.Line(y), w) {
				bottom = max(bottom, y)
			}
		}
	}
	// The key lines run from two rows under the title to the last row
	// holding a known entry.
	height := bottom - (top + 2)
	if wide && height > 16 {
		t.Errorf("which-key takes %d rows at 120 columns, want at most 16\n%s", height, term.Snapshot())
	}
	headings := []string{"Windows", "Panes", "Sessions", "Modes", "Menus", "Tools"}
	shared := false
	for y := top; y <= bottom && !shared; y++ {
		n := 0
		for _, h := range headings {
			if strings.Contains(s.Line(y), h+" ") {
				n++
			}
		}
		shared = n >= 2
	}
	if !shared {
		t.Errorf("no two which-key headings share a row: the sections are not in columns\n%s", term.Snapshot())
	}
}

// checkWhichKeyFramed holds the which-key panel to a frame at 16 colours: the
// row above its title has a top-left corner two cells left of the title, and
// the title row has the left wall there.
func checkWhichKeyFramed(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	s := term.Screen()
	y := rowWith(s, "prefix")
	if y < 1 {
		t.Fatalf("no which-key title on screen\n%s", term.Snapshot())
	}
	title := []rune(s.Line(y))
	x := runeCol(s.Line(y), "prefix")
	above := []rune(s.Line(y - 1))
	if x < 2 || x-2 >= len(above) || above[x-2] != '╭' {
		t.Errorf("the which-key panel has no frame at 16 colours\n%s", term.Snapshot())
		return
	}
	if !strings.ContainsRune(string(above[x-2:]), '╮') {
		t.Errorf("the which-key frame has no top-right corner\n%s", term.Snapshot())
	}
	if title[x-2] != '│' {
		t.Errorf("the which-key title row has no left wall\n%s", term.Snapshot())
	}
}

// TestHintFootersShortenInTiers opens the session switcher on screens too
// narrow for its footer and holds the footer to one row, shortened in the
// documented order: modifiers first (ctrl+r is ^r), then the labels from the
// end. The last tier, whole hints dropped for an ellipsis, needs a body
// narrower than the keys alone, which no screen tuios draws a panel on
// reaches for this footer.
//
// The switcher's footer is "↵ switch   ctrl+r rename   ctrl+d delete   esc
// close", 52 cells. At 50 columns the panel's body is 46 wide, so the short
// modifiers are enough; at 44 it is 40 wide, and "esc" loses its label; at 40
// it is 36, and "^d" loses its label too.
//
// How this could pass wrongly, written down first:
//   - The footer could be read off a stale frame: it is read once the
//     switcher's own session row is drawn.
//   - A wrapped second footer row could be missed: the row under the footer
//     is required to hold none of the hint keys.
//
// Negative control (NEGATIVE_CONTROLS.md): with footerRows wrapping as it did,
// the 50-column case fails on the footer taking two rows and on "ctrl+r"
// never becoming "^r".
func TestHintFootersShortenInTiers(t *testing.T) {
	cases := []struct {
		cols       int
		want, gone []string
	}{
		{cols: 50, want: []string{"^r rename", "^d delete", "esc close"}, gone: []string{"ctrl+"}},
		{cols: 44, want: []string{"^r rename", "^d delete", "esc"}, gone: []string{"ctrl+", "close"}},
		{cols: 40, want: []string{"^r rename", "^d   esc"}, gone: []string{"ctrl+", "delete", "close"}},
	}
	for _, c := range cases {
		t.Run(itoa(c.cols)+"cols", func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			if out, err := tuiosCLI(t, base, "new", "e2e-hints", "--detach"); err != nil {
				t.Fatalf("create session: %v\n%s", err, out)
			}
			term := startIn(t, base, startOpts{cols: c.cols, rows: 24, args: []string{"attach", "e2e-hints"}})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			// The prefix works from either mode, and the status line that says
			// which mode is in force does not fit on a screen this narrow.
			sendKeys(t, term, tuitest.Ctrl('b'), "S")
			waitScreen(t, term, "the session switcher never opened", "e2e-hints", "switch")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("the screen never settled: %v", err)
			}
			dir := artifactDir(t)
			saveArtifact(t, term, dir, "switcher")
			s := term.Screen()
			savePNG(t, s, hostPalette(t, ""), dir, "switcher")

			y := rowWith(s, "switch")
			// "switch" is also the session switcher's own title word; the
			// footer is the last row holding it.
			_, rows := s.Size()
			for r := rows - 1; r > y; r-- {
				if strings.Contains(s.Line(r), "switch") {
					y = r
					break
				}
			}
			footer := s.Line(y)
			for _, w := range c.want {
				if !strings.Contains(footer, w) {
					t.Errorf("the footer %q does not hold %q\n%s", footer, w, term.Snapshot())
				}
			}
			for _, g := range c.gone {
				if strings.Contains(footer, g) {
					t.Errorf("the footer %q still holds %q\n%s", footer, g, term.Snapshot())
				}
			}
			if y+1 < rows {
				next := s.Line(y + 1)
				for _, k := range []string{"esc", "^d", "ctrl+d", "close", "delete"} {
					if strings.Contains(next, k) {
						t.Errorf("the footer wrapped onto a second row: %q\n%s", next, term.Snapshot())
					}
				}
			}
		})
	}
}
