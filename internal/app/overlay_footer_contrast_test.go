package app

import (
	"image/color"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// inkBefore returns the truecolor foreground and background in force where
// needle is drawn, read off the escape sequence that precedes it. This is the
// ink on the frame rather than the token a renderer meant to use.
func inkBefore(t *testing.T, frame, needle string) (fg, bg color.Color) {
	t.Helper()
	idx := strings.Index(frame, needle)
	if idx < 0 {
		t.Fatalf("%q is not in the rendered output", needle)
	}
	head := frame[:idx]
	locs := sgrPattern.FindAllStringSubmatchIndex(head, -1)
	if locs == nil {
		t.Fatalf("%q is drawn with no styling at all", needle)
	}
	parse := func(params []string, start int) color.Color {
		if len(params) < start+5 || params[start+1] != "2" {
			return nil
		}
		v := make([]uint8, 3)
		for i := range v {
			n, err := strconv.Atoi(params[start+2+i])
			if err != nil {
				return nil
			}
			v[i] = uint8(n)
		}
		return color.RGBA{R: v[0], G: v[1], B: v[2], A: 0xFF}
	}
	// Walk the sequences in order so the last one to set each channel wins,
	// which is what the terminal does.
	for _, loc := range locs {
		params := strings.Split(head[loc[2]:loc[3]], ";")
		for i, p := range params {
			switch p {
			case "0":
				fg, bg = nil, nil
			case "38":
				if c := parse(params, i); c != nil {
					fg = c
				}
			case "48":
				if c := parse(params, i); c != nil {
					bg = c
				}
			}
		}
	}
	if fg == nil || bg == nil {
		t.Fatalf("%q is drawn without both a foreground and a background (fg=%v bg=%v)", needle, fg, bg)
	}
	return fg, bg
}

// TestOverlayFooterLabelsClearTheContrastFloor is the floor that keeps the
// footer readable.
//
// Every panel's footer said what its keys do in FgMute, the token for
// separators and disabled things, picked to disappear against the canvas. On
// the panel's lighter Surface it measured 1.81:1 - below every threshold there
// is - so "move / run / close" were barely on the screen at all.
//
// The measurement is taken off the composed frame, so a renderer that goes back
// to a furniture token, or a palette that darkens the label, fails here.
func TestOverlayFooterLabelsClearTheContrastFloor(t *testing.T) {
	pal := theme.UI()

	t.Run("panel-footer", func(t *testing.T) {
		p := overlay.Panel{
			Title: "contrast",
			Width: 40,
			Body:  overlay.Style(pal.Surface).Render("body"),
			Hints: []overlay.Hint{{Key: "enter", Label: "run"}, {Key: "esc", Label: "close"}},
		}
		out, _ := p.Render(pal)
		for _, label := range []string{"run", "close"} {
			fg, bg := inkBefore(t, out, label)
			if got := theme.ContrastRatio(fg, bg); got < theme.ContrastFloor {
				t.Errorf("footer label %q measures %.2f:1, floor is %.2f:1", label, got, theme.ContrastFloor)
			}
		}
	})

	// The same footer as a real overlay draws it, so the floor covers what
	// ships rather than a panel built in the test.
	t.Run("command-palette", func(t *testing.T) {
		m := paletteContrastOS()
		m.ShowCommandPalette = true
		out, _, _ := m.renderCommandPalette()
		fg, bg := inkBefore(t, out, "close")
		if got := theme.ContrastRatio(fg, bg); got < theme.ContrastFloor {
			t.Errorf("the palette footer's %q label measures %.2f:1, floor is %.2f:1", "close", got, theme.ContrastFloor)
		}
	})
}

// TestPanelSecondaryTextClearsTheFloor covers the rows that share the footer's
// ground: the "N of M" info row, and a context menu's unavailable rows, since
// an action you cannot take still has to say what it is.
func TestPanelSecondaryTextClearsTheFloor(t *testing.T) {
	m := paletteContrastOS()

	m.ShowCommandPalette = true
	out, _, _ := m.renderCommandPalette()
	fg, bg := inkBefore(t, out, "commands")
	if got := theme.ContrastRatio(fg, bg); got < theme.ContrastFloor {
		t.Errorf("the palette's info row measures %.2f:1, floor is %.2f:1", got, theme.ContrastFloor)
	}
	m.ShowCommandPalette = false

	m.ContextMenu = &ContextMenu{
		Title:       "pane",
		Items:       []ContextMenuItem{{Label: "close", Action: "close"}, {Label: "unzoom", Action: "unzoom", Dim: true}},
		Selected:    0,
		WindowIndex: -1,
		AnchorX:     10,
		AnchorY:     6,
	}
	menu, _ := m.renderContextMenu()
	fg, bg = inkBefore(t, menu, "unzoom")
	if got := theme.ContrastRatio(fg, bg); got < theme.ContrastFloor {
		t.Errorf("an unavailable menu row measures %.2f:1, floor is %.2f:1", got, theme.ContrastFloor)
	}
}

// paletteContrastOS is a model big enough for the overlays to lay out.
func paletteContrastOS() *OS {
	return &OS{
		Settings:         config.Global,
		WorkspaceFocus:   map[int]int{},
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           30,
	}
}
