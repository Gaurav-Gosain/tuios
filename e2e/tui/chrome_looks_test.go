package tuie2e

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuitest"
	"github.com/charmbracelet/x/ansi"
)

// The chrome under a light theme and a dark terminal, at each colour depth and
// at two sizes, over every overlay that has a key-hint footer.
//
// Three findings from the visual QA of wave01 are held here:
//
//  1. On a light theme every dialog, the palette, which-key, settings, the
//     Inbox and help drew as a dark slab, and the 30% dim behind a modal
//     pushed the light screen toward a mid grey.
//  2. A footer too narrow for its hints dropped labels from the end first, so
//     the Inbox at 120 and 80 columns ended "d   esc": two keys with nothing
//     saying what they do, "esc close" among them.
//  3. The palette's "71 of 71 commands" sat indented in the list, where it
//     read as one more command.
//
// Each run saves every overlay as text, styled text and a PNG drawn by
// tuios's own renderer, under artifactDir. With TUIOS_E2E_QA set the matrix
// is the whole QA set (both looks, three depths, two sizes); without it, the
// runs that hold each finding.

// chromeLook is a theme and whether its ground is light.
type chromeLook struct {
	name, theme string
	light       bool
}

var (
	lookDark   = chromeLook{name: "dark"}
	lookLatte  = chromeLook{name: "latte", theme: "catppuccin_latte", light: true}
	lookGruvbx = chromeLook{name: "gruvbox_light", theme: "gruvbox_light", light: true}
)

// chromeRun is one client: a look, a depth and a size.
type chromeRun struct {
	look       chromeLook
	depth      chromeDepth
	cols, rows int
}

func (r chromeRun) name() string {
	return fmt.Sprintf("%s-%dx%d-%s", r.look.name, r.cols, r.rows, r.depth.name)
}

func chromeRuns() []chromeRun {
	var runs []chromeRun
	sizes := [][2]int{{120, 40}, {80, 24}}
	if os.Getenv("TUIOS_E2E_QA") != "" {
		for _, look := range []chromeLook{lookDark, lookLatte} {
			for _, size := range sizes {
				for _, d := range chromeDepths {
					runs = append(runs, chromeRun{look, d, size[0], size[1]})
				}
			}
		}
		for _, d := range chromeDepths {
			runs = append(runs, chromeRun{lookGruvbx, d, 120, 40})
		}
		return runs
	}
	// Each light theme at every depth, the dark look as the half that must
	// not move, and the footers at both sizes.
	for _, look := range []chromeLook{lookLatte, lookGruvbx} {
		for _, d := range chromeDepths {
			runs = append(runs, chromeRun{look, d, 120, 40})
		}
	}
	runs = append(runs,
		chromeRun{lookDark, chromeDepths[2], 120, 40},
		chromeRun{lookDark, chromeDepths[0], 120, 40},
		chromeRun{lookDark, chromeDepths[2], 80, 24},
		chromeRun{lookLatte, chromeDepths[1], 80, 24},
	)
	return runs
}

// chromeStep is one overlay: the keys that open it from a closed screen, the
// text that says it is drawn (its title first), the keys that close it, and
// whether it has a key-hint footer.
type chromeStep struct {
	name   string
	open   []any
	want   []string
	close  []any
	footer bool
}

func chromeSteps() []chromeStep {
	esc := []any{tuitest.Esc}
	return []chromeStep{
		{name: "palette", open: []any{tuitest.Ctrl('p')}, want: []string{paletteTitle}, close: esc, footer: true},
		{name: "which-key", open: []any{tuitest.Ctrl('b')}, want: []string{"prefix", "Focus pane"}, close: esc},
		{name: "help", open: []any{tuitest.Ctrl('b'), "?"}, want: []string{"Keybindings", "search"}, close: esc, footer: true},
		{name: "settings", open: []any{","}, want: []string{"Settings"}, close: esc, footer: true},
		{name: "inbox", open: []any{tuitest.Ctrl('b'), "i"}, want: []string{"Inbox", "risky: approve Bash", "allow"}, close: esc, footer: true},
		{name: "launcher", open: []any{altSpace}, want: []string{launcherTitle}, close: esc, footer: true},
		{name: "sessions", open: []any{tuitest.Ctrl('b'), "S"}, want: []string{"Sessions", "e2e-agent"}, close: esc, footer: true},
		{name: "workspaces", open: []any{tuitest.Ctrl('b'), "W"}, want: []string{"Workspaces"}, close: esc, footer: true},
		{name: "mail", open: []any{tuitest.Ctrl('b'), "M", "m"}, want: []string{"No mail."}, close: []any{tuitest.Esc, tuitest.Esc}, footer: true},
		{name: "keybinds", open: []any{tuitest.Ctrl('b'), "k"}, want: []string{keybindTitle}, close: esc, footer: true},
		{name: "quit", open: []any{tuitest.Ctrl('b'), "q"}, want: []string{"Detach", "Kill session"}, close: esc, footer: true},
	}
}

// chromeClient is groundClient for one run: the shipped looks, the run's
// theme and depth, no dim behind a modal (the overlay is found by the cells
// that change when it opens), two panes, and an Inbox holding a risky
// approval that takes a reason, a plan, an error and a finished job.
func chromeClient(t *testing.T, r chromeRun) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[appearance]\nmodal_dim = 0\n"
	if r.look.theme != "" {
		cfg += "theme = \"" + r.look.theme + "\"\n"
	}
	cfg += "\n[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 300\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"new", "e2e-home", "--detach"},
		{"new-window", "second", "-s", "e2e-home", "--no-focus"},
		{"new", "e2e-agent", "--detach"},
		{"new-window", "planner", "-s", "e2e-agent", "--no-focus"},
		{"new", "tests", "--detach"},
		{"set-agent-state", "-s", "tests", "errored", "--harness", "codex", "-m", "go test ./... failed: 2 packages"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	startHookIn(t, base, "e2e-agent", "0", `{"hook_event_name":"PermissionRequest","session_id":"e2e-risk","tool_name":"Bash","tool_input":{"command":"rm -rf build/ && git push --force origin main"}}`)

	term := attachIn(t, base, "e2e-home", startOpts{cols: r.cols, rows: r.rows, shippedLooks: true, env: r.depth.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 2 }, bootTimeout); err != nil {
		t.Fatalf("the two panes never showed: %v\n%s", err, term.Snapshot())
	}
	enableTiling(t, term)
	return term, base
}

// TestChromeLooksAndFooters opens every overlay with a footer, and which-key,
// under each run, and holds:
//
//   - every footer: each hint that is shown has its label, and the way out
//     ("esc close", "esc cancel") always has its label;
//   - on a light theme at 256 colours and truecolor: the panel's ground is
//     light, and every glyph in the panel clears 3:1 on the ground it is
//     drawn on, which is the floor the quiet ink is held to, so the accent,
//     the keys, the selection and the muted ink are all measured;
//   - on the dark look: the panel's ground is dark, as it was;
//   - the palette: its match count is on the search line, right-aligned and
//     quieter than a command's name, and on no row of the list;
//   - the dim behind a modal: on a light theme it keeps the screen's ground
//     where it was and fades the text into it; on the dark look it still
//     turns the text down.
//
// How this could pass wrongly, written down first:
//   - The footer could be read off the wrong row, or off the rail beside the
//     panel. The footer row is the last row of the panel holding "esc", and
//     only the panel's own columns are read: the overlay's rectangle in
//     colour, the frame's sides at 16 colours.
//   - A footer that shed every hint but esc would pass the label rule. The
//     Inbox, the one that lost its labels, is also required to keep its
//     answers ("allow", "deny") and "dismiss" at both sizes.
//   - A cell in the default colour cannot be measured. The light runs name
//     their theme, so the chrome writes explicit colours; a default ground in
//     a colour run fails as "not light" rather than passing.
//   - The dim could be read before it is applied. The scrim step waits for
//     the rail's text to change colour before measuring anything.
//   - A panel that stayed dark at 16 colours would pass trivially, since the
//     16-colour chrome paints no ground at all; those runs check the footers
//     and that the panel still paints nothing.
//
// Negative controls are in NEGATIVE_CONTROLS.md.
func TestChromeLooksAndFooters(t *testing.T) {
	for _, run := range chromeRuns() {
		t.Run(run.name(), func(t *testing.T) {
			term, base := chromeClient(t, run)
			dir := artifactDir(t)
			host := hostPalette(t, run.look.theme)
			colour := run.depth.name != "16"
			for _, st := range chromeSteps() {
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("%s: the screen never settled before opening: %v", st.name, err)
				}
				closed := readCells(term.Screen())
				sendKeys(t, term, st.open...)
				waitScreen(t, term, st.name+" never drew", st.want...)
				if err := term.WaitStable(uiTimeout); err != nil {
					t.Fatalf("%s: the screen never settled: %v", st.name, err)
				}
				s := term.Screen()
				saveArtifact(t, term, dir, st.name)
				savePNG(t, s, host, dir, st.name)

				titleRow := rowWith(s, st.want[0])
				var x0, x1 int
				if colour {
					r := findOverlay(closed, readCells(s), modeBg(closed))
					if r.cells < 20 {
						t.Fatalf("%s: found no overlay in the cells that changed\n%s", st.name, term.Snapshot())
					}
					x0, x1 = r.x0, r.x1
					checkPanelGround(t, term, st.name, run.look, r.panel)
					if run.look.light {
						checkPanelInk(t, term, st.name, run.depth.name, s, r, host)
					}
				}
				if st.footer {
					checkFooter(t, term, st.name, s, titleRow, x0, x1, colour)
				}
				if st.name == "palette" {
					checkPaletteCount(t, term, s, colour)
				}
				sendKeys(t, term, st.close...)
				waitGone(t, term, st.name, st.want[0])
			}
			if colour {
				checkScrim(t, term, base, run.look, host)
			}
			t.Logf("frames in %s", dir)
		})
	}
}

// isLight is theme.GroundIsLight: dark ink reads better on c than light ink.
func isLight(c color.Color) bool {
	return overlay.ContrastRatio(c, color.Black) > overlay.ContrastRatio(c, color.White)
}

// cellColour is a cell's colour as the host terminal paints it, def standing
// in for the default.
func cellColour(c tuitest.Color, host *shot.Palette, def shot.Color) color.Color {
	s := resolveCell(c, host, def)
	return color.RGBA{R: s.R, G: s.G, B: s.B, A: 0xff}
}

func checkPanelGround(t *testing.T, term *tuitest.Terminal, name string, look chromeLook, panel tuitest.Color) {
	t.Helper()
	if panel.Kind == tuitest.ColorDefault {
		t.Errorf("%s: the panel's ground is the terminal default in a colour run\n%s", name, term.SnapshotStyled())
		return
	}
	g := tuiColor(panel)
	if isLight(g) != look.light {
		t.Errorf("%s: the panel's ground %v is light=%v under the %s look, want light=%v\n%s",
			name, g, isLight(g), look.name, look.light, term.Snapshot())
	}
}

// inkExempt are glyphs whose colour is a picture rather than ink: swatches
// and bars that show a colour, not text written in one.
func inkExempt(s string) bool {
	return strings.ContainsAny(s, "█▀▄▌▐■▔▁━─│╭╮╰╯")
}

// checkPanelInk measures every glyph in the panel against the ground it is
// drawn on and fails the first under the 3:1 mark floor.
func checkPanelInk(t *testing.T, term *tuitest.Terminal, name, depth string, s tuitest.Screen, r overlayRect, host *shot.Palette) {
	t.Helper()
	fgDef, bgDef := host.FG, host.BG
	worst := 99.0
	var at string
	for y := r.y0; y <= r.y1; y++ {
		for x := r.x0; x <= r.x1; x++ {
			c := s.Cell(x, y)
			if c.Width == 0 || strings.TrimSpace(c.Content) == "" || inkExempt(c.Content) {
				continue
			}
			fg, bg := cellColour(c.Fg, host, fgDef), cellColour(c.Bg, host, bgDef)
			if c.Reverse {
				fg, bg = bg, fg
			}
			if structureInk(fg, bg, depth) {
				// Furniture such as the keybinds' scope column is drawn in
				// overlay.Structure on purpose, aimed at StructureTarget
				// rather than at a floor.
				continue
			}
			ratio := overlay.ContrastRatio(fg, bg)
			if ratio < worst {
				worst, at = ratio, fmt.Sprintf("%q at (%d,%d), %v on %v", c.Content, x, y, fg, bg)
			}
		}
	}
	if worst < overlay.MarkFloor {
		t.Errorf("%s: a glyph measures %.2f:1, under the %.1f:1 floor: %s\n%s", name, worst, overlay.MarkFloor, at, term.SnapshotStyled())
	}
}

// footerLine is the panel's footer: the last row from the title down that
// holds the way out ("close", "cancel", or a bare esc), cut to the panel's columns, with the frame's own glyphs
// taken out. At 16 colours the columns are the frame's corners.
func footerLine(s tuitest.Screen, titleRow, x0, x1 int, colour bool) (int, string) {
	_, rows := s.Size()
	row := -1
	for y := max(titleRow, 0); y < rows; y++ {
		line := []rune(s.Line(y))
		lo, hi := x0, x1
		if !colour {
			lo, hi = 0, len(line)-1
		}
		if lo >= len(line) {
			continue
		}
		seg := string(line[lo:min(hi+1, len(line))])
		if strings.Contains(seg, " close") || strings.Contains(seg, " cancel") ||
			strings.HasSuffix(strings.TrimRight(seg, " │─╯"), "esc") {
			row = y
		}
	}
	if row < 0 {
		return -1, ""
	}
	line := []rune(s.Line(row))
	if !colour {
		// The panel's frame: the corner or side left and right of the hints.
		x0, x1 = 0, len(line)-1
		text := s.Line(row)
		at := strings.LastIndex(text, " c")
		if at < 0 {
			at = max(strings.LastIndex(text, "esc"), 0)
		}
		start := len([]rune(text[:at]))
		for i := start; i >= 0 && i < len(line); i-- {
			if strings.ContainsRune("│╰", line[i]) {
				x0 = i + 1
				break
			}
		}
		for i := x0; i < len(line); i++ {
			if strings.ContainsRune("│╯", line[i]) {
				x1 = i - 1
				break
			}
		}
	}
	seg := string(line[max(x0, 0):min(x1+1, len(line))])
	seg = strings.Map(func(r rune) rune {
		if strings.ContainsRune("│─╰╯", r) {
			return ' '
		}
		return r
	}, seg)
	return row, seg
}

// footerHints splits a footer into its hints: a hint is a key, a space and a
// label, and hints are two or more spaces apart.
func footerHints(line string) []string {
	var out []string
	for f := range strings.SplitSeq(line, "  ") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func checkFooter(t *testing.T, term *tuitest.Terminal, name string, s tuitest.Screen, titleRow, x0, x1 int, colour bool) {
	t.Helper()
	row, line := footerLine(s, titleRow, x0, x1, colour)
	if row < 0 {
		// A screen too short for the body and the footer drops the footer
		// whole (panelBody); that keeps no bare key either.
		t.Logf("%s: no footer on screen", name)
		return
	}
	hints := footerHints(line)
	var labelled []string
	for _, h := range hints {
		if h == "…" {
			continue
		}
		if !strings.Contains(h, " ") {
			t.Errorf("%s: the footer shows %q with no label: %q\n%s", name, h, line, term.Snapshot())
			continue
		}
		labelled = append(labelled, h)
	}
	// The way out keeps its label: esc on most panels, ? on help.
	outOK := false
	for _, h := range labelled {
		if strings.HasSuffix(h, " close") || strings.HasSuffix(h, " cancel") {
			outOK = true
		}
	}
	if !outOK {
		t.Errorf("%s: the footer has no labelled way out: %q\n%s", name, line, term.Snapshot())
	}
	if name == "inbox" {
		for _, want := range []string{"1 allow", "3 deny", "d dismiss", "esc close"} {
			if !strings.Contains(line, want) {
				t.Errorf("inbox: the footer lost %q: %q\n%s", want, line, term.Snapshot())
			}
		}
	}
}

// checkPaletteCount holds the palette's match count to the search line,
// right-aligned against the rule under it, and quieter than a command.
func checkPaletteCount(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, colour bool) {
	t.Helper()
	p := findPalette(t, term)
	countRow := rowWith(s, "commands")
	for y := p.searchRow + 1; y <= p.bottomRow; y++ {
		if strings.Contains(s.Line(y), " of ") && strings.Contains(s.Line(y), "commands") {
			t.Errorf("palette: the match count is in the list, on row %d\n%s", y, term.Snapshot())
		}
	}
	if countRow != p.searchRow {
		t.Errorf("palette: the match count is on row %d, want the search line %d\n%s", countRow, p.searchRow, term.Snapshot())
		return
	}
	line := []rune(s.Line(countRow))
	end := p.ruleEnd - 1
	if end >= len(line) || line[end] != 's' {
		t.Errorf("palette: the count does not end on the rule's last column %d: %q\n%s", end, string(line), term.Snapshot())
		return
	}
	if !colour {
		return
	}
	count, name := s.Cell(end, countRow), s.Cell(p.ruleStart+2, p.selRow+1)
	host := shot.XTermPalette()
	bg := cellColour(count.Bg, host, host.BG)
	cr := overlay.ContrastRatio(cellColour(count.Fg, host, host.FG), bg)
	nr := overlay.ContrastRatio(cellColour(name.Fg, host, host.FG), cellColour(name.Bg, host, host.BG))
	if cr >= nr {
		t.Errorf("palette: the count measures %.2f:1, no quieter than a command's name at %.2f:1\n%s", cr, nr, term.SnapshotStyled())
	}
	if cr < overlay.MarkFloor {
		t.Errorf("palette: the count measures %.2f:1, under the %.1f:1 floor\n%s", cr, overlay.MarkFloor, term.SnapshotStyled())
	}
}

// checkScrim turns the dim behind a modal on and opens the palette over the
// panes. On a light theme the ground behind stays the theme's own and the
// rail's text fades toward it; on the dark look the text is turned down.
func checkScrim(t *testing.T, term *tuitest.Terminal, base string, look chromeLook, host *shot.Palette) {
	t.Helper()
	if o, err := tuiosCLI(t, base, "set-config", "appearance.modal_dim", "30"); err != nil {
		t.Fatalf("set the modal dim: %v\n%s", err, o)
	}
	time.Sleep(time.Second)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	s := term.Screen()
	railX := railHeaderColumn(s)
	row, col, ok := textAt(s, "e2e-home", railX)
	if !ok {
		t.Fatalf("the attached session is not on the rail\n%s", term.Snapshot())
	}
	_, rows := s.Size()
	groundAt := [2]int{2, rows - 3}
	before := s.Cell(col, row)
	groundBefore := s.Cell(groundAt[0], groundAt[1])

	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "for the scrim")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		c := s.Cell(col, row)
		return c.Fg != before.Fg || c.Faint != before.Faint
	}, uiTimeout); err != nil {
		t.Fatalf("the rail's text never changed behind the palette with the dim at 30: %v\n%s", err, term.SnapshotStyled())
	}
	s = term.Screen()
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "palette-dimmed")
	savePNG(t, s, host, dir, "palette-dimmed")
	after := s.Cell(col, row)
	groundAfter := s.Cell(groundAt[0], groundAt[1])

	ground := cellColour(tuitest.Color{}, host, host.BG)
	gb, ga := cellColour(groundBefore.Bg, host, host.BG), cellColour(groundAfter.Bg, host, host.BG)
	fb, fa := cellColour(before.Fg, host, host.FG), cellColour(after.Fg, host, host.FG)
	if look.light {
		if ratio := overlay.ContrastRatio(gb, ga); ratio > 1.1 {
			t.Errorf("the dim moved the light ground behind the palette from %v to %v, %.2f:1 apart\n%s", gb, ga, ratio, term.SnapshotStyled())
		}
		if !isLight(ga) {
			t.Errorf("the ground behind the palette is %v, no longer light\n%s", ga, term.SnapshotStyled())
		}
		if overlay.ContrastRatio(fa, ground) >= overlay.ContrastRatio(fb, ground) {
			t.Errorf("the rail's text did not fade toward the ground: %v then %v on %v\n%s", fb, fa, ground, term.SnapshotStyled())
		}
	} else if !after.Faint && overlay.ContrastRatio(fa, color.Black) >= overlay.ContrastRatio(fb, color.Black) {
		t.Errorf("the dim did not turn the rail's text down on the dark look: %v then %v\n%s", fb, fa, term.SnapshotStyled())
	}
	closePalette(t, term, "after the scrim")
}

// structureInk reports whether fg is overlay.Structure's ink for bg at the
// run's depth, which is where tuios picks the palette entry at 256 colours.
func structureInk(fg, bg color.Color, depth string) bool {
	if depth == "256" {
		prev := overlay.CurrentDepth()
		overlay.SetDepth(overlay.Depth256)
		defer overlay.SetDepth(prev)
	}
	ink := overlay.Structure(bg)
	if idx, ok := ink.(ansi.IndexedColor); ok {
		ink = tuiColor(tuitest.Color{Kind: tuitest.ColorIndexed, Index: uint8(idx)})
	}
	ar, ag, ab, _ := fg.RGBA()
	br, bgc, bb, _ := ink.RGBA()
	return ar>>8 == br>>8 && ag>>8 == bgc>>8 && ab>>8 == bb>>8
}
