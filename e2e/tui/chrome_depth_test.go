package tuie2e

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuitest"
)

// The chrome at each colour depth a terminal can have. The depth is what the
// test terminal's TERM and COLORTERM say, which is how a user's terminal
// selects it too: xterm is 16 colours, xterm-256color without COLORTERM is 256
// (Apple Terminal, mosh, tmux without Tc), and COLORTERM=truecolor is 24-bit.
//
// Each run saves the frame as text, as styled text and as a PNG drawn by
// tuios's own renderer (internal/shot) from the cells the terminal received,
// with the palette a terminal of that kind would paint the sixteen slots with.

// chromeDepth is one terminal the chrome is drawn for.
type chromeDepth struct {
	name string
	env  []string
}

var chromeDepths = []chromeDepth{
	{name: "16", env: []string{"TERM=xterm", "COLORTERM="}},
	{name: "256", env: []string{"TERM=xterm-256color", "COLORTERM="}},
	{name: "truecolor", env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
}

// chromeLooks are the two grounds: no theme on a dark terminal, and a light
// theme.
var chromeLooks = []struct{ name, theme string }{
	{name: "dark", theme: ""},
	{name: "light", theme: "catppuccin_latte"},
}

// hostPalette is the palette the picture paints default and indexed colours
// with: the xterm defaults for the dark look, and the theme's own sixteen for
// a themed one, since a user on a light theme runs a terminal set to it.
func hostPalette(t *testing.T, themeName string) *shot.Palette {
	t.Helper()
	if themeName == "" {
		return shot.XTermPalette()
	}
	if err := theme.Initialize(themeName); err != nil {
		t.Fatalf("load theme %s: %v", themeName, err)
	}
	defer func() { _ = theme.Initialize("") }()
	p := &shot.Palette{}
	for i, c := range theme.GetANSIPalette() {
		p.ANSI[i] = shotColor(c)
	}
	p.FG, p.BG = shotColor(theme.TerminalFg()), shotColor(theme.TerminalBg())
	return p
}

func shotColor(c color.Color) shot.Color {
	r, g, b, _ := c.RGBA()
	return shot.RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

// resolveCell is a tuitest colour as the host terminal paints it.
func resolveCell(c tuitest.Color, p *shot.Palette, def shot.Color) shot.Color {
	switch c.Kind {
	case tuitest.ColorIndexed:
		if c.Index < 16 {
			return p.ANSI[c.Index]
		}
		return shot.XTerm256(int(c.Index))
	case tuitest.ColorRGB:
		return shot.RGB(c.R, c.G, c.B)
	}
	return def
}

// savePNG draws the screen through internal/shot and writes it beside the
// text artifacts, so the frame can be looked at the way the terminal shows it.
func savePNG(t *testing.T, s tuitest.Screen, p *shot.Palette, dir, name string) {
	t.Helper()
	cols, rows := s.Size()
	g := shot.NewGrid(cols, rows, p.FG, p.BG)
	for y := range rows {
		for x := range cols {
			c := s.Cell(x, y)
			fg, bg := resolveCell(c.Fg, p, p.FG), resolveCell(c.Bg, p, p.BG)
			bgDefault := c.Bg.Kind == tuitest.ColorDefault
			if c.Reverse {
				fg, bg, bgDefault = bg, fg, false
			}
			cell := shot.Cell{Cluster: c.Content, Width: uint8(max(min(c.Width, 2), 0)), FG: fg, BG: bg,
				BGDefault: bgDefault, Bold: c.Bold, Faint: c.Faint, Italic: c.Italic, Strike: c.Strikethrough}
			if cell.Cluster == " " {
				cell.Cluster = ""
			}
			if c.Underline {
				cell.Underline = shot.UnderlineSingle
			}
			g.Cells[y][x] = cell
		}
	}
	data, err := shot.RenderPNG(g, nil, nil)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".png"), data, 0o644); err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
}

// palettePanel is where the command palette landed on screen.
type palettePanel struct {
	titleRow  int // the row of the title chip
	left      int // the panel's first column
	right     int // one past its last column
	ruleStart int // the first column of the search rule, the body's left edge
	ruleEnd   int // one past its last
	selRow    int // the selected command's row
	bottomRow int // the panel's last row
	searchRow int // the search line
}

// findPalette reads the palette's geometry off the screen: the title, then the
// search rule two rows under it, whose run of rule glyphs is the body's width.
func findPalette(t *testing.T, term *tuitest.Terminal) palettePanel {
	t.Helper()
	s := term.Screen()
	var p palettePanel
	p.titleRow = rowWith(s, paletteTitle)
	if p.titleRow < 0 {
		t.Fatalf("no palette on screen\n%s", term.Snapshot())
	}
	p.searchRow = p.titleRow + 2
	col, w := longestRuleRun(s.Line(p.titleRow + 3))
	if w < 20 {
		t.Fatalf("no search rule under the palette's title\n%s", term.Snapshot())
	}
	p.ruleStart, p.ruleEnd = col, col+w
	p.left, p.right = p.ruleStart-overlay.DefaultPanelPadding, p.ruleEnd+overlay.DefaultPanelPadding
	p.selRow = p.titleRow + 4
	if !strings.Contains(s.Line(p.selRow), "›") {
		t.Fatalf("row %d is not the palette's selected row\n%s", p.selRow, term.Snapshot())
	}
	_, rows := s.Size()
	p.bottomRow = p.selRow
	for y := p.selRow; y < rows; y++ {
		if s.Cell(p.left+1, y).Bg != s.Cell(p.left+1, p.searchRow).Bg {
			break
		}
		p.bottomRow = y
	}
	return p
}

// TestChromeAtEveryColourDepth opens the command palette under each colour
// depth, on a dark terminal with no theme and under a light theme, and holds
// the chrome to what each depth is designed to show.
//
//   - 256 colours: every ground in the panel is on the grey ramp, and no cell
//     on screen has the navy (17) or blue (4) ground the stepped-down truecolor
//     ramp gave the panel band and the selected row.
//   - 16 colours: the panel paints no ground at all, it frames itself with a
//     hairline instead, and the selected row is reverse video.
//   - truecolor: the selected row's ground stands out from the surface.
//
// How this could pass wrongly, written down first:
//   - The profile could be ignored and every run drawn in truecolor. The 256
//     and 16 runs require indexed and default grounds, which truecolor never
//     writes.
//   - The palette could be found on a stale frame before it is drawn. Its
//     geometry is read off its own search rule and selected row, and each is
//     required to be there.
//   - A reversed row could be counted as painted ground. Reverse is checked
//     first and the ground checks skip reversed cells, which are the chip and
//     the selected row.
//
// Negative controls (NEGATIVE_CONTROLS.md): on origin/main, and with
// theme.SetColorProfile not setting the depth, the 256 runs fail on a ground
// of index 17 and the 16 runs on one of index 4; with the palette row not
// finished by pal.Row, the 16 runs fail on the selected row. The truecolor
// runs pass on every build, which is their positive half.
func TestChromeAtEveryColourDepth(t *testing.T) {
	for _, look := range chromeLooks {
		for _, d := range chromeDepths {
			t.Run(look.name+"-"+d.name, func(t *testing.T) {
				base := t.TempDir()
				killDaemon(t, base)
				useShippedLooks(base)
				if look.theme != "" {
					writeConfig(t, base, "[appearance]\ntheme = \""+look.theme+"\"\n")
				}
				for _, name := range []string{"e2e-depth", "e2e-other"} {
					if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
						t.Fatalf("create session %s: %v\n%s", name, err, out)
					}
				}
				term := startIn(t, base, startOpts{args: []string{"attach", "e2e-depth"}, shippedLooks: true, env: d.env})
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return countWindows(s) == 1 && strings.Contains(s.Text(), "e2e-other")
				}, bootTimeout); err != nil {
					t.Fatalf("client never attached with the rail listing both sessions: %v\n%s", err, term.Snapshot())
				}
				windowManagementMode(t, term)
				dir := artifactDir(t)
				host := hostPalette(t, look.theme)
				saveArtifact(t, term, dir, "rail")
				savePNG(t, term.Screen(), host, dir, "rail")

				if err := term.SendKeys(legacyCtrlP); err != nil {
					t.Fatalf("open the palette: %v", err)
				}
				waitPaletteOpen(t, term, "for the depth check")
				p := findPalette(t, term)
				s := term.Screen()
				saveArtifact(t, term, dir, "palette")
				savePNG(t, s, host, dir, "palette")

				checkRailInk(t, term, look.theme, d.name)
				switch d.name {
				case "16":
					check16(t, term, s, p)
				case "256":
					check256(t, term, s, p)
				default:
					checkTrueColor(t, term, s, p)
				}

				// The rail with the keyboard: its cursor row is the same rule
				// as the palette's selected row, a ground or, at 16 colours,
				// reverse video.
				closePalette(t, term, "before focusing the rail")
				if err := term.SendKeys("s"); err != nil {
					t.Fatalf("focus the rail: %v", err)
				}
				if err := term.WaitForText(railPill, uiTimeout); err != nil {
					t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
				}
				s = term.Screen()
				saveArtifact(t, term, dir, "rail-focused")
				savePNG(t, s, host, dir, "rail-focused")
				row, col, ok := textAt(s, "e2e-depth", railHeaderColumn(s))
				if !ok {
					t.Fatalf("the attached session is not on the rail\n%s", term.Snapshot())
				}
				cur, below := s.Cell(col, row), s.Cell(col, row+1)
				if d.name == "16" {
					if !cur.Reverse {
						t.Fatalf("the focused rail's cursor row is %+v, want reverse video\n%s", cur, term.SnapshotStyled())
					}
				} else if cur.Bg == below.Bg {
					t.Fatalf("the focused rail's cursor row has the resting ground %+v\n%s", cur.Bg, term.SnapshotStyled())
				}
			})
		}
	}
}

// checkNoNavy holds the whole screen to the finding that started this work:
// no ground of index 17 or 4.
func checkNoNavy(t *testing.T, term *tuitest.Terminal, s tuitest.Screen) {
	t.Helper()
	cols, rows := s.Size()
	for y := range rows {
		for x := range cols {
			if bg := s.Cell(x, y).Bg; bg.Kind == tuitest.ColorIndexed && (bg.Index == 17 || bg.Index == 4) {
				t.Fatalf("cell (%d,%d) has ground index %d\n%s", x, y, bg.Index, term.SnapshotStyled())
			}
		}
	}
}

func check256(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, p palettePanel) {
	t.Helper()
	checkNoNavy(t, term, s)
	for y := p.titleRow - 1; y <= p.bottomRow; y++ {
		if y == p.titleRow {
			continue // the title chip is the accent
		}
		for x := p.left; x < p.right; x++ {
			bg := s.Cell(x, y).Bg
			if bg.Kind != tuitest.ColorIndexed || bg.Index < 232 {
				t.Fatalf("panel cell (%d,%d) has ground %+v, not a grey ramp entry\n%s", x, y, bg, term.SnapshotStyled())
			}
		}
	}
	if sel, ground := s.Cell(p.ruleStart, p.selRow).Bg, s.Cell(p.ruleStart, p.searchRow).Bg; sel == ground {
		t.Fatalf("the selected row's ground %+v is the panel's own\n%s", sel, term.SnapshotStyled())
	}
}

func check16(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, p palettePanel) {
	t.Helper()
	checkNoNavy(t, term, s)
	corners := map[[2]int]string{
		{p.left, p.titleRow - 1}:      "╭",
		{p.right - 1, p.titleRow - 1}: "╮",
		{p.left, p.selRow}:            "│",
		{p.right - 1, p.selRow}:       "│",
	}
	for at, want := range corners {
		if got := s.Cell(at[0], at[1]).Content; got != want {
			t.Fatalf("the panel's frame at (%d,%d) is %q, want %q\n%s", at[0], at[1], got, want, term.Snapshot())
		}
	}
	for x := p.ruleStart; x < p.ruleEnd; x++ {
		if !s.Cell(x, p.selRow).Reverse {
			t.Fatalf("selected row cell (%d,%d) is not reverse video\n%s", x, p.selRow, term.SnapshotStyled())
		}
	}
	for y := p.titleRow - 1; y <= p.bottomRow; y++ {
		for x := p.left; x < p.right; x++ {
			c := s.Cell(x, y)
			if c.Reverse {
				continue
			}
			if c.Bg.Kind != tuitest.ColorDefault {
				t.Fatalf("panel cell (%d,%d) paints ground %+v at 16 colours\n%s", x, y, c.Bg, term.SnapshotStyled())
			}
		}
	}
}

func checkTrueColor(t *testing.T, term *tuitest.Terminal, s tuitest.Screen, p palettePanel) {
	t.Helper()
	sel, ground := s.Cell(p.ruleStart, p.selRow).Bg, s.Cell(p.ruleStart, p.searchRow).Bg
	if sel.Kind != tuitest.ColorRGB || ground.Kind != tuitest.ColorRGB {
		t.Fatalf("truecolor panel grounds are not RGB: row %+v, surface %+v\n%s", sel, ground, term.SnapshotStyled())
	}
	if sel == ground {
		t.Fatalf("the selected row's ground %+v is the panel's own\n%s", sel, term.SnapshotStyled())
	}
}

// checkRailInk measures the attached session's name on the rail against the
// ground it is written on. Under the light theme the ground is the theme's
// background as the depth shows it; at 16 colours the name is the terminal's
// own foreground, which is readable by construction, and any other colour
// there fails.
func checkRailInk(t *testing.T, term *tuitest.Terminal, themeName, depth string) {
	t.Helper()
	s := term.Screen()
	railX := railHeaderColumn(s)
	if railX < 0 {
		t.Fatalf("no rail on screen\n%s", term.Snapshot())
	}
	row, col, ok := textAt(s, "e2e-other", railX)
	if !ok {
		t.Fatalf("the rail's other session is not on screen\n%s", term.Snapshot())
	}
	c := s.Cell(col, row)
	if depth == "16" {
		if c.Fg.Kind == tuitest.ColorRGB || (c.Fg.Kind == tuitest.ColorIndexed && c.Fg.Index >= 16) {
			t.Fatalf("a rail name at 16 colours is drawn in %+v, not a slot or the default\n%s", c.Fg, term.SnapshotStyled())
		}
		return
	}
	if themeName == "" {
		return
	}
	ground := color.Color(latteGround)
	if depth == "256" {
		ground = overlay.To256(latteGround)
	}
	if c.Bg.Kind != tuitest.ColorDefault {
		ground = tuiColor(c.Bg)
	}
	ink := tuiColor(c.Fg)
	if ratio := overlay.ContrastRatio(ink, ground); ratio < overlay.ContrastFloor {
		t.Fatalf("a rail name is %v on %v, %.2f:1, under the %.1f:1 floor\n%s", ink, ground, ratio, overlay.ContrastFloor, term.SnapshotStyled())
	}
}

// tuiColor is a tuitest colour as RGB, through the xterm table for an index.
func tuiColor(c tuitest.Color) color.Color {
	s := resolveCell(c, shot.XTermPalette(), shot.XTermFg)
	return color.RGBA{R: s.R, G: s.G, B: s.B, A: 0xff}
}

// TestUnfocusedListKeepsItsCursor holds the review overlay's two lists to the
// focus rule: the list without the keyboard still shows its cursor, on a
// quieter ground than the list with it. The review opens with the diff
// focused, so the file list's cursor is the unfocused one; tab gives the
// keyboard to the file list, and then the diff's cursor is.
//
// In truecolor the file row under the cursor has to stand out from the list's
// resting rows while the diff has the keyboard, stand out more strongly once
// the list has it, and the diff has to show its cursor then: the hunk header it rests on keeps a cursor tint that is gone once the cursor
// moves off it. At 16 colours, where there are no grounds, the quiet cursor is an
// underline and the focused one reverse video.
//
// How this could pass wrongly, written down first:
//   - The README row could be found in the diff rather than in the list. The
//     row is found by "M README", the list's status letter and path.
//   - A resting row could be missing, making any ground look distinct. The
//     resting ground is read from the list's own blank row under README, and
//     the list is required to have one.
//   - The two focus states could be read off the same frame. Each state is
//     waited for by its own mark: the list's sigil appears only when it has
//     the keyboard.
//
// Negative control: with the file list and the diff drawing their cursor only
// while focused (the code before this change), the truecolor run fails on the
// unfocused file row sharing the resting ground, and the 16-colour run on the
// missing underline.
func TestUnfocusedListKeepsItsCursor(t *testing.T) {
	for _, d := range []chromeDepth{chromeDepths[0], chromeDepths[2]} {
		t.Run(d.name, func(t *testing.T) {
			base, repo := fanFixture(t)
			session := reviewFan(t, base, repo, "fx")
			dir := artifactDir(t)
			host := shot.XTermPalette()

			term := attachIn(t, base, session, startOpts{env: d.env})
			sendKeys(t, term, tuitest.Ctrl('b'), "v")
			waitScreen(t, term, "the review never opened", "Review", "M README", "esc close")
			s := term.Screen()
			saveArtifact(t, term, dir, "diff-focused")
			savePNG(t, s, host, dir, "diff-focused")
			row, col, ok := textAt(s, "M README", 0)
			if !ok {
				t.Fatalf("no file row\n%s", term.Snapshot())
			}
			pathCol := col + 2
			quiet, rest := s.Cell(pathCol, row), s.Cell(pathCol, row+1)
			if rest.Content != " " && rest.Content != "" {
				t.Fatalf("the file list has no resting row under README to compare with\n%s", term.Snapshot())
			}

			sendKeys(t, term, "\t")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r, c, ok := textAt(s, "README", 0)
				return ok && r == row && c == pathCol && s.Cell(col-1, row).Content == overlay.SigilMark()
			}, uiTimeout); err != nil {
				t.Fatalf("tab did not give the keyboard to the file list: %v\n%s", err, term.Snapshot())
			}
			s = term.Screen()
			saveArtifact(t, term, dir, "list-focused")
			savePNG(t, s, host, dir, "list-focused")
			focused := s.Cell(pathCol, row)

			if d.name == "16" {
				if !quiet.Underline || quiet.Reverse {
					t.Fatalf("the unfocused file list's cursor is %+v, want underlined\n%s", quiet, term.SnapshotStyled())
				}
				if !focused.Reverse {
					t.Fatalf("the focused file list's cursor is %+v, want reverse video\n%s", focused, term.SnapshotStyled())
				}
				return
			}
			if quiet.Bg == rest.Bg {
				t.Fatalf("the unfocused file list's cursor sits on the resting ground %+v\n%s", rest.Bg, term.SnapshotStyled())
			}
			if focused.Bg == quiet.Bg || focused.Bg == rest.Bg {
				t.Fatalf("the focused cursor ground %+v is not its own (quiet %+v, rest %+v)\n%s",
					focused.Bg, quiet.Bg, rest.Bg, term.SnapshotStyled())
			}
			// The diff's cursor is on its first row, the hunk header. It has to
			// keep a cursor ground of its own there: different from the ground
			// the header rests on, which is what it shows once the cursor moves
			// off it.
			hunkRow, hunkCol, ok := textAt(s, "@@", col+10)
			if !ok {
				t.Fatalf("no hunk header in the diff\n%s", term.Snapshot())
			}
			quietHunk := s.Cell(hunkCol, hunkRow).Bg
			sendKeys(t, term, "\t", "j")
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return s.Cell(col-1, row).Content != overlay.SigilMark() && s.Cell(hunkCol, hunkRow).Bg != quietHunk
			}, uiTimeout); err != nil {
				t.Fatalf("the diff cursor never left the hunk header, or the header never showed a quiet cursor: %v\n%s",
					err, term.SnapshotStyled())
			}
			saveArtifact(t, term, dir, "diff-moved")
		})
	}
}
