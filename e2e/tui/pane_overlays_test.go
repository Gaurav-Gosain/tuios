package tuie2e

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// stockLook is what a dialog and the palette show under a config that does
// not ask for pane-styled overlays, read off an upstream main build (b90ef7e71)
// under the same harness: the rename dialog's frame ink and ground and its
// interior ground, and the palette's search-line and selected-row grounds.
type stockLook struct {
	frameFg, frameBg, dialogBg tuitest.Color
	paletteBg, paletteSel      tuitest.Color
}

var (
	stockTrueColor = stockLook{
		frameFg:    tuitest.Color{Kind: tuitest.ColorRGB, R: 0x85, G: 0x83, B: 0x92},
		frameBg:    tuitest.Color{Kind: tuitest.ColorRGB, R: 0x20, G: 0x1f, B: 0x26},
		dialogBg:   tuitest.Color{Kind: tuitest.ColorRGB, R: 0x20, G: 0x1f, B: 0x26},
		paletteBg:  tuitest.Color{Kind: tuitest.ColorRGB, R: 0x3a, G: 0x39, B: 0x43},
		paletteSel: tuitest.Color{Kind: tuitest.ColorRGB, R: 0x2d, G: 0x2c, B: 0x36},
	}
	// At 16 colours the frame is bright black on the terminal's own ground.
	stockSixteen = stockLook{
		frameFg: tuitest.Color{Kind: tuitest.ColorIndexed, Index: 8},
	}
)

// TestStockOverlaysKeepTheirOwnLook holds a config that does not set
// appearance.pane_overlays to the look upstream main draws: the rename
// dialog's frame in its quiet ink on the dialog's shade, its interior on that
// shade, and the palette's search line and selected row on the chrome's own
// grounds. It runs with no appearance keys, with pane_background set (the
// config the selected row was seen as a greyish-brown bar on a navy pane
// under), and at 16 colours, where the frame is bright black.
//
// How this could pass wrongly, written down first:
//   - The expected values could be read off this branch, so they would agree
//     with whatever it draws. They were read off an upstream main build run
//     under this test, which passes there.
//   - The dialog could be read before it is drawn, and a frame cell left on
//     the pane would then be compared. The box is found from its title and
//     hints, and every probe is waited for.
//   - The palette's selected row could be the search line, which would pass
//     the search-line probe twice. findPalette finds it by its cursor mark.
func TestStockOverlaysKeepTheirOwnLook(t *testing.T) {
	art := artifactDir(t)
	for _, tc := range []struct {
		name, appearance string
		env              []string
		want             stockLook
	}{
		{"stock", "", truecolor, stockTrueColor},
		{"pane-background", "pane_background = \"#203040\"\n", truecolor, stockTrueColor},
		{"stock-16", "", sixteenColours, stockSixteen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := startOverlayBorders(t, tc.appearance, tc.env)
			enableTiling(t, term)
			box := openRenameFrame(t, term)
			saveArtifact(t, term, art, "stock-"+tc.name+"-rename")
			wantStockDialog(t, term, art, "stock-"+tc.name+"-rename", box, tc.want)
			closeRename(t, term)

			sendKeys(t, term, tuitest.Ctrl('p'))
			waitPaletteOpen(t, term, "to read its grounds")
			p := findPalette(t, term)
			saveArtifact(t, term, art, "stock-"+tc.name+"-palette")
			s := term.Screen()
			bg, sel := s.Cell(p.ruleStart, p.searchRow).Bg, s.Cell(p.ruleStart, p.selRow).Bg
			report := fmt.Sprintf("search line bg=%+v\nselected row bg=%+v\n", bg, sel)
			saveOverlayText(t, art, "stock-"+tc.name+"-palette-grounds", report)
			if bg != tc.want.paletteBg || sel != tc.want.paletteSel {
				t.Errorf("the palette's grounds moved under a config without pane_overlays (want search line %+v, selected row %+v):\n%s\n%s",
					tc.want.paletteBg, tc.want.paletteSel, report, term.SnapshotStyled())
			}
			closePalette(t, term, "after reading its grounds")
			alive(t, term, "after the stock look check")
		})
	}
}

// wantStockDialog fails unless the rename dialog at b has its frame in want's
// ink and ground and its padding cell on want's dialog ground.
func wantStockDialog(t *testing.T, term *tuitest.Terminal, art, name string, b overlayBox, want stockLook) {
	t.Helper()
	var report string
	err := term.WaitFor(func(s tuitest.Screen) bool {
		var r strings.Builder
		ok := true
		for _, p := range []cellProbe{{"top-left", b.x0, b.y0}, {"bottom-right", b.x1, b.y1}, {"top", b.x0 + 1, b.y0}, {"left", b.x0, b.y0 + 1}} {
			c := s.Cell(p.x, p.y)
			fmt.Fprintf(&r, "%-12s (%d,%d) %q fg=%+v bg=%+v\n", p.name, p.x, p.y, c.Content, c.Fg, c.Bg)
			if c.Fg != want.frameFg || c.Bg != want.frameBg {
				ok = false
			}
		}
		c := s.Cell(b.x0+1, b.y0+1)
		fmt.Fprintf(&r, "%-12s (%d,%d) bg=%+v\n", "padding", b.x0+1, b.y0+1, c.Bg)
		if c.Bg != want.dialogBg {
			ok = false
		}
		report = r.String()
		return ok
	}, uiTimeout)
	saveOverlayText(t, art, name+"-colours", report)
	if err != nil {
		t.Fatalf("the rename dialog at %+v does not keep upstream's look (want frame fg=%+v bg=%+v, interior bg=%+v):\n%s\n%s",
			b, want.frameFg, want.frameBg, want.dialogBg, report, term.SnapshotStyled())
	}
}

// TestPaneOverlaysStepRowsFromThePaneGround opens the command palette under
// pane_overlays with a navy pane_background and holds its selected row to a
// step of that navy: not the pane ground itself, which would hide the cursor;
// not the chrome's own cursor row, the greyish-brown bar the stock look draws;
// and on the navy's side of the colour wheel, its blue channel leading its red
// as #203040's does, at about one ramp step from the ground. The search line,
// the palette's ground, is the pane's.
//
// How this could pass wrongly, written down first:
//   - A row ground left on the chrome's ramp is neither the pane ground nor
//     navy, which the hue and stock checks catch apart.
//   - A cursor row the same colour as the ground passes every check but the
//     first, so it comes first.
func TestPaneOverlaysStepRowsFromThePaneGround(t *testing.T) {
	art := artifactDir(t)
	term := startOverlayBorders(t, "pane_overlays = true\npane_background = \"#203040\"\n", truecolor)
	enableTiling(t, term)
	sendKeys(t, term, tuitest.Ctrl('p'))
	waitPaletteOpen(t, term, "to read its selected row")
	var report string
	err := term.WaitFor(func(s tuitest.Screen) bool {
		p, ok := paletteRows(s)
		if !ok {
			report = "no palette yet"
			return false
		}
		ground, sel := s.Cell(p.ruleStart, p.searchRow).Bg, s.Cell(p.ruleStart, p.selRow).Bg
		var r strings.Builder
		fmt.Fprintf(&r, "search line bg=%+v\nselected row bg=%+v\n", ground, sel)
		good := ground == matchPaneBg && sel.Kind == tuitest.ColorRGB && sel != matchPaneBg && sel != stockTrueColor.paletteSel
		if sel.Kind == tuitest.ColorRGB {
			ratio := overlay.ContrastRatio(truecolorRGB(sel), truecolorRGB(matchPaneBg))
			fmt.Fprintf(&r, "selected row to ground %.2f:1\n", ratio)
			good = good && sel.B > sel.R && ratio > 1.05 && ratio < 1.6
		}
		report = r.String()
		return good
	}, uiTimeout)
	saveArtifact(t, term, art, "pane-overlays-palette")
	saveOverlayText(t, art, "pane-overlays-palette-rows", report)
	if err != nil {
		t.Fatalf("the palette's selected row is not a step of pane_background %+v (want a navy step off it, not the stock %+v):\n%s\n%s",
			matchPaneBg, stockTrueColor.paletteSel, report, term.SnapshotStyled())
	}
	closePalette(t, term, "after reading its selected row")
	alive(t, term, "after the pane overlay row check")
}

// paletteRows is findPalette for a WaitFor predicate: it reports false rather
// than failing while the palette is not yet drawn.
func paletteRows(s tuitest.Screen) (palettePanel, bool) {
	var p palettePanel
	p.titleRow = rowWith(s, paletteTitle)
	if p.titleRow < 0 {
		return p, false
	}
	p.searchRow = p.titleRow + 2
	col, w := longestRuleRun(s.Line(p.titleRow + 3))
	if w < 20 {
		return p, false
	}
	p.ruleStart, p.ruleEnd = col, col+w
	for _, y := range []int{p.titleRow + 4, p.titleRow + 5} {
		if strings.Contains(s.Line(y), "›") {
			p.selRow = y
			return p, true
		}
	}
	return p, false
}

// truecolorRGB is a 24-bit cell colour as a colour.
func truecolorRGB(c tuitest.Color) color.Color { return color.RGBA{R: c.R, G: c.G, B: c.B, A: 0xff} }

// mochaGround is catppuccin_mocha's background, the ground the rail is drawn
// on under that theme where it paints none of its own.
var mochaGround = color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff}

// railProbes are the rail texts TestRailReadsOnALightPaneGround measures, and
// the floor each is held to.
var railProbes = []struct {
	what, text string
	floor      float64
}{
	{"the rail's attached session", "e2e-lite", overlay.ContrastFloor},
	{"the rail's other session", "e2e-quill", overlay.ContrastFloor},
	{"the rail's section header", "sessions", overlay.MarkFloor},
}

// TestRailReadsOnALightPaneGround attaches under catppuccin_mocha, a dark
// theme, with the shipped looks, a near-white pane_background and
// pane_overlays on. It measures the rail's session names against the ground
// they are drawn on and its quiet section header against the mark floor, and
// holds each ink to the one the rail draws with neither option set. The
// pane-styled overlays are drawn from a palette of their own, so nothing about
// the pane's light ground may reach the rail.
//
// How this could pass wrongly, written down first:
//   - A name matched in a pane rather than the rail, so the names are looked
//     for right of the rail's divider only.
//   - A cell in the terminal's default colour cannot be measured; the client is
//     told it has truecolor and such a cell fails as not measured.
//   - An ink lifted against the pane's ground can still clear the floor on the
//     rail's dark one while it is no longer the rail's own colour, which is
//     why each ink is also compared with the plain run's.
func TestRailReadsOnALightPaneGround(t *testing.T) {
	art := artifactDir(t)
	plain := railInks(t, art, "rail-plain", "")
	light := railInks(t, art, "rail-on-light-pane-ground", "pane_overlays = true\npane_background = \"#f4f2ea\"\n")
	for _, probe := range railProbes {
		if !overlaySameColour(plain[probe.text], light[probe.text]) {
			t.Errorf("%s is %v with a light pane ground and pane_overlays, %v without: the pane's ground reached the rail",
				probe.what, light[probe.text], plain[probe.text])
		}
	}
}

// overlaySameColour reports whether a and b are the same colour.
func overlaySameColour(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return ar == br && ag == bg && ab == bb
}

// railInks attaches under catppuccin_mocha with the shipped looks and extra
// appearance keys, holds each rail probe to its floor, saves the frame and the
// measurements under name, and returns each probe's ink by its text.
func railInks(t *testing.T, art, name, extra string) map[string]color.Color {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "[appearance]\ntheme = \"catppuccin_mocha\"\n"+extra)
	for _, n := range []string{"e2e-lite", "e2e-quill"} {
		if out, err := tuiosCLI(t, base, "new", n, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", n, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-lite"}, shippedLooks: true,
		env: []string{"COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && strings.Contains(s.Text(), "e2e-quill")
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail listing both sessions: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	s := term.Screen()
	saveArtifact(t, term, art, name)
	savePNG(t, s, shot.XTermPalette(), art, name)

	railX := railHeaderColumn(s)
	if railX < 0 {
		t.Fatalf("no rail on screen\n%s", term.Snapshot())
	}
	inks := map[string]color.Color{}
	var report strings.Builder
	for _, probe := range railProbes {
		row, col, ok := textAt(s, probe.text, railX-2)
		if !ok {
			t.Fatalf("%s (%q) is not in the rail\n%s", probe.what, probe.text, term.Snapshot())
		}
		ink, measured := inkAt(s, col, row)
		if !measured {
			t.Fatalf("%s at (%d,%d) carries no colour to measure\n%s", probe.what, col, row, term.SnapshotStyled())
		}
		var ground color.Color = mochaGround
		if bg := s.Cell(col, row).Bg; bg.Kind == tuitest.ColorRGB {
			ground = truecolorRGB(bg)
		}
		ratio := overlay.ContrastRatio(ink, ground)
		fmt.Fprintf(&report, "%s (%d,%d) ink=%v ground=%v %.2f:1\n", probe.what, col, row, ink, ground, ratio)
		if ratio < probe.floor {
			t.Errorf("%s at (%d,%d) is %v on %v, %.2f:1, under the %.1f:1 floor\n%s",
				probe.what, col, row, ink, ground, ratio, probe.floor, term.SnapshotStyled())
		}
		inks[probe.text] = ink
	}
	saveOverlayText(t, art, name+"-contrast", report.String())
	return inks
}
