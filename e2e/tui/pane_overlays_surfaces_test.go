package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
)

// The three surfaces the first pass of pane-styled overlays left on the
// chrome's own palette: the plugin approval dialog, the scrollback browser and
// the copy-mode search bar. Each is opened end to end here, with
// appearance.pane_overlays on and with it off.

// paneOverlaysLook is the config the pane-styled checks run under: the
// focused border and the pane ground in colours no palette lands on by chance.
const paneOverlaysLook = "pane_overlays = true\n" +
	"border_focused_color = \"#ff8800\"\n" +
	"pane_background = \"#203040\"\n"

// TestPaneOverlaysReachTheRemainingSurfaces checks that with
// appearance.pane_overlays on, the plugin approval dialog, the scrollback
// browser and the copy-mode search bar are drawn like the other overlays:
// framed in the focused pane border's ink, on the pane's ground, with rows
// stepped from that ground.
//
//   - plugin-approval: a native plugin linked while a client is attached opens
//     the dialog. Its frame and interior are held as the rename dialog's are by
//     TestOverlayFramesMatchPaneBorders, title and hints included.
//   - scrollback-browser: the browser over a pane that printed two commands
//     with OSC 133 marks. Its frame takes the focused border's ink, its
//     selected command row is a step of the pane ground, and its footer's key
//     pills are steps of it too.
//   - copy-mode-search: the search bar copy mode draws at the foot of the pane
//     is on the pane's ground.
//
// How this could pass wrongly, written down first:
//   - Under pane_background every cell the browser leaves unstyled is painted
//     the pane's ground by the compositor whatever palette the browser uses, so
//     its interior ground proves nothing by itself. The browser is held by what
//     it draws itself: its frame ink, its selected row and its key pills, each
//     compared with the stock look's colour as well as with the pane ground.
//   - The browser covers the whole screen, so the focused border's ink is read
//     off a pane border cell before it opens, not after.
//   - A selected row on the pane ground itself would hide the cursor and still
//     not be the stock row, so it is checked against the ground first.
//   - The copy-mode bar is found by its query and match count, so a bar that is
//     not drawn cannot pass as one whose ground is the pane's. The cell left of
//     it is pane content and must carry the ground too, so the ground compared
//     is the one the pane really shows.
//   - The plugin dialog is found from its title and its corners, not by
//     searching for colours.
func TestPaneOverlaysReachTheRemainingSurfaces(t *testing.T) {
	art := artifactDir(t)

	t.Run("plugin-approval", func(t *testing.T) {
		term, base := startOverlaySession(t, paneOverlaysLook, truecolor)
		enableTiling(t, term)
		box := openPluginApproval(t, term, base)
		saveArtifact(t, term, art, "pane-overlays-plugin-approval")
		wantFrameColours(t, term, art, "pane-overlays-plugin-approval", "the plugin approval dialog", box, matchBorder, pluginApprovalInterior)
		wantFrameRowsOnPane(t, term, art, "pane-overlays-plugin-approval", box)
		closePluginApproval(t, term)
		alive(t, term, "after the plugin approval check")
	})

	t.Run("scrollback-browser", func(t *testing.T) {
		term, _ := startOverlaySession(t, paneOverlaysLook, truecolor)
		if _, _, found := paneBorderCell(term.Screen(), overlayBox{-1, -1, -1, -1}); !found {
			t.Fatalf("no pane border in the focused border's ink %+v before the browser opened:\n%s", matchBorder, term.SnapshotStyled())
		}
		b := openScrollbackBrowser(t, term)
		saveArtifact(t, term, art, "pane-overlays-scrollback-browser")
		var report string
		err := term.WaitFor(func(s tuitest.Screen) bool {
			l, ok := readBrowserLook(s, b)
			report = l.report
			if !ok {
				return false
			}
			good := l.frameFg == matchBorder && l.frameBg == matchPaneBg && l.padding == matchPaneBg
			good = good && paneStep(l.selected, stockBrowserTrueColor.selected) && paneStep(l.pill, stockBrowserTrueColor.pill)
			return good
		}, uiTimeout)
		saveOverlayText(t, art, "pane-overlays-scrollback-browser-colours", report)
		if err != nil {
			t.Fatalf("the scrollback browser does not read as a pane window (want frame fg=%+v on %+v, the selected row and key pills steps of %+v, not the stock %+v and %+v):\n%s\n%s",
				matchBorder, matchPaneBg, matchPaneBg, stockBrowserTrueColor.selected, stockBrowserTrueColor.pill, report, term.SnapshotStyled())
		}
		closeScrollbackBrowser(t, term)
		alive(t, term, "after the scrollback browser check")
	})

	t.Run("copy-mode-search", func(t *testing.T) {
		term, _ := startOverlaySession(t, paneOverlaysLook, truecolor)
		openCopyModeSearch(t, term)
		saveArtifact(t, term, art, "pane-overlays-copy-search")
		var report string
		err := term.WaitFor(func(s tuitest.Screen) bool {
			l, ok := readCopySearchLook(s)
			report = l.report
			return ok && l.pane == matchPaneBg && l.pad == matchPaneBg && l.prompt == matchPaneBg && l.count == matchPaneBg
		}, uiTimeout)
		saveOverlayText(t, art, "pane-overlays-copy-search-colours", report)
		if err != nil {
			t.Fatalf("the copy-mode search bar is not on the pane's ground %+v (the stock bar is %+v):\n%s\n%s",
				matchPaneBg, stockCopySearchTrueColor, report, term.SnapshotStyled())
		}
		alive(t, term, "after the copy-mode search check")
	})
}

// paneStep reports whether c is a step of the navy pane ground: a 24-bit
// colour that is neither the ground nor stock, on the navy's side of the
// wheel, at about one ramp step from it. TestPaneOverlaysStepRowsFromThePaneGround
// holds the palette's cursor row to the same.
func paneStep(c, stock tuitest.Color) bool {
	if c.Kind != tuitest.ColorRGB || c == matchPaneBg || c == stock || c.B <= c.R {
		return false
	}
	ratio := overlay.ContrastRatio(cellRGB(c), cellRGB(matchPaneBg))
	return ratio > 1.05 && ratio < 1.6
}

// browserStock is what the scrollback browser draws itself with under a config
// without pane_overlays: its frame ink, the selected command row's ground and
// a footer key pill's ground. It was read off a build of this branch before
// the browser followed the overlay palette (4a9267a44), which draws it as
// upstream main does.
type browserStock struct {
	frameFg, selected, pill tuitest.Color
}

var (
	stockBrowserTrueColor = browserStock{
		frameFg:  tuitest.Color{Kind: tuitest.ColorRGB, R: 0x8b, G: 0x75, B: 0xff},
		selected: tuitest.Color{Kind: tuitest.ColorRGB, R: 0x2d, G: 0x2c, B: 0x36},
		pill:     tuitest.Color{Kind: tuitest.ColorRGB, R: 0x4d, G: 0x4c, B: 0x57},
	}
	// At 16 colours the frame is bright cyan, and the selected row and the
	// pills are on the terminal's own ground: the row says it is selected in
	// reverse video.
	stockBrowserSixteen = browserStock{
		frameFg: tuitest.Color{Kind: tuitest.ColorIndexed, Index: 14},
	}

	// stockCopySearchTrueColor is the copy-mode search bar's ground under the
	// same stock config, read off the same build. At 16 colours it is the
	// terminal's own.
	stockCopySearchTrueColor = tuitest.Color{Kind: tuitest.ColorRGB, R: 0x3a, G: 0x39, B: 0x43}
	stockCopySearchSixteen   = tuitest.Color{}
)

// TestStockRemainingSurfacesKeepTheirOwnLook is
// TestStockOverlaysKeepTheirOwnLook for the plugin approval dialog, the
// scrollback browser and the copy-mode search bar: under a config that does not
// set appearance.pane_overlays each draws what it drew before it followed the
// overlay palette. It runs with no appearance keys, with pane_background set,
// and at 16 colours.
//
// How this could pass wrongly, written down first:
//   - The expected values could be read off the build under test, so they
//     would agree with whatever it draws. They were read off the build before
//     the change, which passes this test.
//   - A surface read before it is drawn would be compared as pane content.
//     Each is found by its own text, and every probe is waited for.
//   - The browser's interior ground is the compositor's, not the browser's,
//     so it is not what is compared; its frame, selected row and key pills are.
func TestStockRemainingSurfacesKeepTheirOwnLook(t *testing.T) {
	art := artifactDir(t)
	for _, tc := range []struct {
		name, appearance string
		env              []string
		dialog           stockLook
		browser          browserStock
		search           tuitest.Color
	}{
		{"stock", "", truecolor, stockTrueColor, stockBrowserTrueColor, stockCopySearchTrueColor},
		{"pane-background", "pane_background = \"#203040\"\n", truecolor, stockTrueColor, stockBrowserTrueColor, stockCopySearchTrueColor},
		{"stock-16", "", sixteenColours, stockSixteen, stockBrowserSixteen, stockCopySearchSixteen},
	} {
		t.Run(tc.name+"/plugin-approval", func(t *testing.T) {
			term, base := startOverlaySession(t, tc.appearance, tc.env)
			box := openPluginApproval(t, term, base)
			name := "stock-" + tc.name + "-plugin-approval"
			saveArtifact(t, term, art, name)
			wantStockDialog(t, term, art, name, box, tc.dialog)
			closePluginApproval(t, term)
			alive(t, term, "after the stock plugin approval check")
		})

		t.Run(tc.name+"/scrollback-browser", func(t *testing.T) {
			term, _ := startOverlaySession(t, tc.appearance, tc.env)
			b := openScrollbackBrowser(t, term)
			name := "stock-" + tc.name + "-scrollback-browser"
			saveArtifact(t, term, art, name)
			var report string
			err := term.WaitFor(func(s tuitest.Screen) bool {
				l, ok := readBrowserLook(s, b)
				report = l.report
				return ok && l.frameFg == tc.browser.frameFg && l.selected == tc.browser.selected && l.pill == tc.browser.pill
			}, uiTimeout)
			saveOverlayText(t, art, name+"-colours", report)
			if err != nil {
				t.Fatalf("the scrollback browser moved under a config without pane_overlays (want frame fg=%+v, selected row %+v, key pill %+v):\n%s\n%s",
					tc.browser.frameFg, tc.browser.selected, tc.browser.pill, report, term.SnapshotStyled())
			}
			closeScrollbackBrowser(t, term)
			alive(t, term, "after the stock scrollback browser check")
		})

		t.Run(tc.name+"/copy-mode-search", func(t *testing.T) {
			term, _ := startOverlaySession(t, tc.appearance, tc.env)
			openCopyModeSearch(t, term)
			name := "stock-" + tc.name + "-copy-search"
			saveArtifact(t, term, art, name)
			var report string
			err := term.WaitFor(func(s tuitest.Screen) bool {
				l, ok := readCopySearchLook(s)
				report = l.report
				return ok && l.pad == tc.search && l.prompt == tc.search && l.count == tc.search
			}, uiTimeout)
			saveOverlayText(t, art, name+"-colours", report)
			if err != nil {
				t.Fatalf("the copy-mode search bar moved under a config without pane_overlays (want its ground %+v):\n%s\n%s",
					tc.search, report, term.SnapshotStyled())
			}
			alive(t, term, "after the stock copy-mode search check")
		})
	}
}

// pluginApprovalTitle is the plugin the dialog asks about.
const pluginApprovalTitle = "Allow the plugin Look probe?"

// openPluginApproval links a native plugin while the client is attached, waits
// for the dialog it opens, and finds its frame: the title sits two cells right
// of the top-left corner, and the other corners are read along the top row and
// down the left edge.
func openPluginApproval(t *testing.T, term *tuitest.Terminal, base string) overlayBox {
	t.Helper()
	approvalPlugin(t, base, "e2e.look", "Look probe", `"read"`, "sleep 1\n")
	var box overlayBox
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		var ok bool
		box, ok = pluginApprovalBox(s)
		return ok
	}, bootTimeout); err != nil {
		t.Fatalf("the plugin approval dialog never opened: %v\n%s", err, term.Snapshot())
	}
	return box
}

func pluginApprovalBox(s tuitest.Screen) (overlayBox, bool) {
	cols, rows := s.Size()
	y := rowWith(s, " plugin approval ")
	if y < 0 || rowWith(s, pluginApprovalTitle) != y+1 {
		return overlayBox{}, false
	}
	b := overlayBox{x0: rowCol(s, y, " plugin approval ") - 2, y0: y}
	if b.x0 < 0 || s.Cell(b.x0, y).Content != "╭" {
		return overlayBox{}, false
	}
	for x := b.x0 + 1; x < cols && b.x1 == 0; x++ {
		if s.Cell(x, y).Content == "╮" {
			b.x1 = x
		}
	}
	for yy := y + 1; yy < rows && b.y1 == 0; yy++ {
		if s.Cell(b.x0, yy).Content == "╰" {
			b.y1 = yy
		}
	}
	return b, b.x1 > 0 && b.y1 > 0 && strings.Contains(s.Line(b.y1), "not now")
}

// pluginApprovalInterior is the dialog's interior probes: the padding cell
// right of the left edge on the question's row, and the question's first
// letter.
func pluginApprovalInterior(s tuitest.Screen, b overlayBox) []cellProbe {
	return []cellProbe{{"padding", b.x0 + 1, b.y0 + 1}, {"body text", rowCol(s, b.y0+1, pluginApprovalTitle), b.y0 + 1}}
}

// closePluginApproval answers Not now with esc, past the half second the
// dialog ignores keys for.
func closePluginApproval(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Allow the plugin") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the plugin approval dialog: %v\n%s", err, term.Snapshot())
	}
}

// browserTitle is the scrollback browser's title.
const browserTitle = "Scrollback browser"

// openScrollbackBrowser prints two commands with OSC 133 marks in the pane,
// opens the scrollback browser on them and finds its frame: the title is one
// row of padding below the top edge and two cells of padding right of the
// left one.
func openScrollbackBrowser(t *testing.T, term *tuitest.Terminal) overlayBox {
	t.Helper()
	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033]133;A\007$ \033]133;B\007echo one\n\033]133;C\007first-%s\n\033]133;D;0\007\033]133;A\007$ \033]133;B\007echo two\n\033]133;C\007second-out\n\033]133;D;0\007' out`, "first-out", shellTimeout)
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, term, tuitest.Ctrl('b'), "s")
	var b overlayBox
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		y := rowWith(s, browserTitle)
		if y < 2 || rowWith(s, "> echo two") < 0 {
			return false
		}
		cols, rows := s.Size()
		b = overlayBox{x0: rowCol(s, y, browserTitle) - 3, y0: y - 2}
		for x := b.x0 + 1; x < cols && b.x1 == 0; x++ {
			if s.Cell(x, b.y0).Content == "╮" {
				b.x1 = x
			}
		}
		for yy := y; yy < rows && b.y1 == 0; yy++ {
			if s.Cell(b.x0, yy).Content == "╰" {
				b.y1 = yy
			}
		}
		return b.x0 >= 0 && s.Cell(b.x0, b.y0).Content == "╭" && b.x1 > 0 && b.y1 > 0
	}, uiTimeout); err != nil {
		t.Fatalf("the scrollback browser never opened on the two commands: %v\n%s", err, term.Snapshot())
	}
	return b
}

func closeScrollbackBrowser(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	sendKeys(t, term, "q")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), browserTitle) }, uiTimeout); err != nil {
		t.Fatalf("q did not close the scrollback browser: %v\n%s", err, term.Snapshot())
	}
}

// browserLook is what readBrowserLook read off the scrollback browser.
type browserLook struct {
	frameFg, frameBg, padding, selected, pill tuitest.Color
	report                                    string
}

// readBrowserLook reads the browser at b: the frame's ink and ground, which
// must be one colour round all four corners and the edges; the padding cell
// inside the top-left corner; the selected command's first letter; and the key
// pill of the footer's first hint. ok is false until the rows are drawn.
func readBrowserLook(s tuitest.Screen, b overlayBox) (browserLook, bool) {
	var l browserLook
	var r strings.Builder
	ok := true
	frame := []cellProbe{
		{"top-left", b.x0, b.y0}, {"top-right", b.x1, b.y0},
		{"bottom-left", b.x0, b.y1}, {"bottom-right", b.x1, b.y1},
		{"top", b.x0 + 1, b.y0}, {"left", b.x0, b.y0 + 1},
	}
	for i, p := range frame {
		c := s.Cell(p.x, p.y)
		fmt.Fprintf(&r, "%-12s (%d,%d) %q fg=%+v bg=%+v\n", p.name, p.x, p.y, c.Content, c.Fg, c.Bg)
		if i == 0 {
			l.frameFg, l.frameBg = c.Fg, c.Bg
		} else if c.Fg != l.frameFg || c.Bg != l.frameBg {
			ok = false
		}
	}
	l.padding = s.Cell(b.x0+1, b.y0+1).Bg
	fmt.Fprintf(&r, "%-12s (%d,%d) bg=%+v\n", "padding", b.x0+1, b.y0+1, l.padding)

	sx, sy := -1, rowWith(s, "> echo two")
	if sy >= 0 {
		sx = rowCol(s, sy, "echo two")
	}
	hx, hy := -1, rowWith(s, "j/k")
	if hy >= 0 {
		hx = rowCol(s, hy, "j/k")
	}
	if sx < 0 || hx < 0 {
		fmt.Fprintf(&r, "selected row at (%d,%d), first key pill at (%d,%d): not drawn yet\n", sx, sy, hx, hy)
		l.report = r.String()
		return l, false
	}
	l.selected, l.pill = s.Cell(sx, sy).Bg, s.Cell(hx, hy).Bg
	fmt.Fprintf(&r, "%-12s (%d,%d) %q bg=%+v\n", "selected", sx, sy, s.Cell(sx, sy).Content, l.selected)
	fmt.Fprintf(&r, "%-12s (%d,%d) %q bg=%+v\n", "key pill", hx, hy, s.Cell(hx, hy).Content, l.pill)
	if l.selected.Kind == tuitest.ColorRGB {
		fmt.Fprintf(&r, "selected row to pane ground %.2f:1\n", overlay.ContrastRatio(cellRGB(l.selected), cellRGB(matchPaneBg)))
	}
	if l.pill.Kind == tuitest.ColorRGB {
		fmt.Fprintf(&r, "key pill to pane ground %.2f:1\n", overlay.ContrastRatio(cellRGB(l.pill), cellRGB(matchPaneBg)))
	}
	l.report = r.String()
	return l, ok
}

// openCopyModeSearch prints a line to find, enters copy mode and types a
// search for it, and waits for the bar to show the query and its one match.
func openCopyModeSearch(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	enterTerminalMode(t, term)
	runInShell(t, term, `printf 'nee''dle-%s\n' one`, "dle-one", shellTimeout)
	sendKeys(t, term, tuitest.Ctrl('b'), "[")
	time.Sleep(300 * time.Millisecond)
	sendKeys(t, term, "/", "n", "e", "e", "d")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, ok := readCopySearchLook(s)
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("copy mode never showed the search bar with /need and its match: %v\n%s", err, term.Snapshot())
	}
}

// copySearchLook is what readCopySearchLook read off the copy-mode search bar.
type copySearchLook struct {
	pane, pad, prompt, count tuitest.Color
	report                   string
}

// readCopySearchLook reads the search bar: the pad cell left of the prompt,
// the prompt, and the match count's bracket, and the pane content cell left of
// the bar. ok is false until the bar shows the query and its count.
func readCopySearchLook(s tuitest.Screen) (copySearchLook, bool) {
	var l copySearchLook
	y := lastRowWith(s, "/need")
	if y < 0 || !strings.Contains(s.Line(y), "[1/1]") {
		l.report = "no search bar with /need [1/1]"
		return l, false
	}
	x, cx := rowCol(s, y, "/need"), rowCol(s, y, "[1/1]")
	if x < 2 || cx < 0 {
		l.report = "the search bar's cells were not found"
		return l, false
	}
	var r strings.Builder
	for _, p := range []struct {
		name string
		x    int
		dst  *tuitest.Color
	}{{"pane", x - 2, &l.pane}, {"pad", x - 1, &l.pad}, {"prompt", x, &l.prompt}, {"count", cx, &l.count}} {
		c := s.Cell(p.x, y)
		*p.dst = c.Bg
		fmt.Fprintf(&r, "%-8s (%d,%d) %q fg=%+v bg=%+v\n", p.name, p.x, y, c.Content, c.Fg, c.Bg)
	}
	l.report = r.String()
	return l, true
}
