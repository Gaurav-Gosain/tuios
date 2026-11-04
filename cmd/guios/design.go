package main

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// The design system's tokens, as ~/dev/tuios-notes/guios/DESIGN.md sets
// them: the type scale, the spacing grid, the radii and the motion. Views
// take their sizes from here, so that one change reaches every surface and
// no two surfaces drift apart.

// The type scale. Line heights follow the size (DESIGN.md section 2).
const (
	fsCaption = 11 // key caps, counts in pills
	fsSmall   = 12 // detail lines, folders, ages, chips, tooltips, section labels
	fsBody    = 13 // rows, buttons, menu items, settings labels, pane titles
	fsProse   = 14 // the agent thread, the reply box, Markdown
	fsTitle   = 15 // view and dialog titles
	fsDisplay = 20 // a full-window empty state's title
	fsCode    = 12 // commands, code blocks, diffs in the thread
)

// Weights.
const (
	wRegular  = 400
	wMedium   = 500
	wSemibold = 600
)

// The 4 px grid.
const (
	sp1 = 4
	sp2 = 8
	sp3 = 12
	sp4 = 16
	sp5 = 20
	sp6 = 24
	sp8 = 32
)

// Sizes of controls and rows.
const (
	hControl   = 28 // buttons, fields, segmented controls, tabs
	hControlSm = 24 // buttons in a card header or a toast
	hRow       = 32 // a one-line row
	hRow2      = 48 // a two-line row
	hChip      = 20 // chips and badges
	hKbd       = 18 // key caps
	dialogW    = 420
	paletteW   = 640
	measure    = 440 // the widest a paragraph gets in an empty state or a dialog
	threadW    = 760 // the widest the agent thread gets
	// titleKeep is the least a title keeps of its width before the chips
	// beside it drop.
	titleKeep = 96
)

// Radii.
const (
	rChip    = 4  // chips, badges, key caps, inline code, menu items
	rRow     = 6  // rows, icon buttons, tooltips, tabs
	rControl = 8  // buttons, fields, segmented controls, code blocks, menus
	rPane    = 10 // pane cards
	rCard    = 12 // cards in a view, the reply box, approval cards
	rSheet   = 14 // dialogs, the palette, the agents view, settings
)

// Motion lengths for state changes in place: fills, glows, a card's
// ground. What comes and goes (dialogs, toasts, panes) moves by
// motion.go's motionQuick, motionBase and motionLayout. MyGo's animations
// go to their target at once when the desktop asks for less motion, so
// these need no reduced variant.
const (
	dHoverOut = 120 * time.Millisecond
	dPress    = 90 * time.Millisecond
	dFade     = 120 * time.Millisecond
	dArrive   = 180 * time.Millisecond
	dToggle   = 150 * time.Millisecond
)

// flat is text from a program or an agent made one line: its newlines and
// tabs joined into single spaces, and an ellipsis its source cut it with
// taken off, so that a cut of ours does not end in two.
func flat(s string) string {
	if strings.ContainsAny(s, "\n\r\t") {
		s = strings.Join(strings.Fields(s), " ")
	}
	return strings.TrimSuffix(strings.TrimSpace(s), "…")
}

// natural is the width text takes on one line, uncut. The view asks it
// of every label each frame, so it keeps what it measured.
func natural(c *ui.Context, s string, size float32, weight int) float32 {
	k := measureKey{s, size, weight}
	if w, ok := measured[k]; ok {
		return w
	}
	if len(measured) > 4096 {
		clear(measured)
	}
	w, _ := c.MeasureText(0, ui.Span{Text: s, Size: size, Weight: weight})
	measured[k] = w
	return w
}

type measureKey struct {
	s      string
	size   float32
	weight int
}

var measured = map[measureKey]float32{}

// oneLine is a label (DESIGN.md section 7): one line, cut at its end with
// an ellipsis where it does not fit, giving way to what is beside it, and
// with its full text in a tooltip while it is cut.
func oneLine(c *ui.Context, s string, size float32, weight int) ui.Element {
	s = flat(s)
	t := ui.Text(c, s).FontSize(size).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
	if weight != 0 && weight != wRegular {
		t.FontWeight(weight)
	}
	if b := t.Bounds(); b.W > 0 && b.W+0.5 < natural(c, s, size, weight) {
		t.Tooltip(s)
	}
	return t
}

// startCut is a path cut at its start, so that the file's name stays:
// "…/internal/auth/token.go". width is the room it has, in DIPs.
func startCut(c *ui.Context, s string, size float32, width float32) string {
	if width <= 0 || natural(c, s, size, 0) <= width {
		return s
	}
	// The cut is the same from frame to frame while the room is: keep it,
	// rather than measure every tail again in each frame.
	k := cutKey{s, size, int(width)}
	if t, ok := cuts[k]; ok {
		return t
	}
	if len(cuts) > 1024 {
		clear(cuts)
	}
	r := []rune(s)
	t := ""
	// A whole folder at a time where one fits ("…/demo", not "…rc/demo"),
	// else character by character.
	for i := 1; i < len(r); i++ {
		if r[i] != '/' && r[i] != '\\' {
			continue
		}
		if tail := "…" + string(r[i:]); natural(c, tail, size, 0) <= width {
			t = tail
			break
		}
	}
	for i := 1; t == "" && i < len(r); i++ {
		if tail := "…" + string(r[i:]); natural(c, tail, size, 0) <= width {
			t = tail
		}
	}
	if t == "" {
		t = "…"
	}
	// The tails measured on the way would crowd out the labels.
	for i := 1; i < len(r); i++ {
		if tail := "…" + string(r[i:]); tail != t {
			delete(measured, measureKey{tail, size, 0})
		}
	}
	cuts[k] = t
	return t
}

type cutKey struct {
	s     string
	size  float32
	width int
}

var cuts = map[cutKey]string{}

// keyHint is a key cap and what the key does, for the hint bars: the cap in
// a ring, the label in the third text color.
func keyHint(c *ui.Context, pal *palette, key, what string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		kbd(c, pal, key)
		ui.Text(c, what).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine()
	})
}

// countBadge is a count in a pill of its role's color: 18 high, at least
// as wide, with tabular figures.
func countBadge(c *ui.Context, n int, bg, fg ui.Color) {
	ui.Row(c).Height(hKbd).MinWidth(hKbd).Padding(0, 6).Radius(hKbd / 2).Background(bg).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, itoa(n)).FontSize(fsCaption).FontWeight(wSemibold).FontFeatures("tnum").TextColor(fg).SingleLine()
	})
}

// onAccent is the color of text on the accent: white on a deep accent,
// the card's color on a light one, as a light accent in the dark is.
func onAccent(pal *palette) ui.Color {
	if l, _, _ := oklch(pal.accent); l > 0.68 {
		return pal.card.Mix(ui.Hex("#000000"), 0.35)
	}
	return ui.Hex("#ffffff")
}

// pathLine is a path on one line, cut at its start so that its file's
// name stays, with the whole path in a tooltip while it is cut. key
// names its box, whose width the last frame measured.
func pathLine(c *ui.Context, key, path string, size float32, col ui.Color) ui.Element {
	box := ui.Box(c.Key(key)).Grow(1).MinWidth(0)
	w := box.Bounds().W
	box.Children(func() {
		s := startCut(c, path, size, w)
		t := ui.Text(c, s).FontSize(size).TextColor(col).SingleLine()
		if s != path {
			t.Tooltip(path)
		}
	})
	return box
}

// emptyBlock is an empty state (DESIGN.md section 6): an icon in a tile,
// a title, at most three lines of detail, and its actions. display sizes
// the title for a whole window; else it is a view's.
func emptyBlock(c *ui.Context, pal *palette, icon *ui.SVG, title, detail string, display bool, actions func()) {
	ui.Column(c).Fill().Center().Gap(0).Padding(sp6).Children(func() {
		if icon != nil {
			ui.Box(c).Size(40, 40).Radius(rCard).Center().Background(pal.chip).TextColor(pal.textFaint).Margin(0, 0, sp4, 0).Children(func() {
				ui.Icon(c, icon).Size(20, 20)
			})
		}
		size := float32(fsTitle)
		if display {
			size = fsDisplay
		}
		ui.Text(c, title).FontSize(size).FontWeight(wSemibold).TextColor(pal.text).TextAlign(ui.Center).MaxWidth(measure).MaxLines(2)
		if detail != "" {
			ui.Text(c, detail).FontSize(fsBody).TextColor(pal.textMuted).MaxWidth(measure).TextAlign(ui.Center).LineHeight(1.45).MaxLines(3).Margin(sp2, 0, 0, 0)
		}
		if actions != nil {
			ui.Row(c).Gap(sp2).Margin(sp5, 0, 0, 0).Wrap().Justify(ui.Center).Children(actions)
		}
	})
}
