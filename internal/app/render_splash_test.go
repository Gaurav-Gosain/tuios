package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// splashLayer renders the overlays on an empty desktop and returns the welcome
// layer, which is the only one drawn when no window exists.
func splashLayer(t *testing.T, m *OS) *lipgloss.Layer {
	t.Helper()
	for _, layer := range m.renderOverlays() {
		if layer.GetID() == "welcome" {
			return layer
		}
	}
	t.Fatal("the empty desktop drew no splash")
	return nil
}

// TestSplashCentersInContentRegion pins the splash to the region the panes get,
// not the whole screen. Centering on the screen drew the box across the rail's
// reserved columns and left it visibly off-centre in the part of the desktop
// the user can actually see.
func TestSplashCentersInContentRegion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		position string
	}{
		{"left rail", "left"},
		{"right rail", "right"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withSidebar(t, true, tc.position, 30)
			m := newNarrowOS(t, 120, 40)

			left, width := m.GetLeftMargin(), m.GetContentWidth()
			if width >= m.GetRenderWidth() {
				t.Fatalf("the rail reserved nothing: content width %d of %d", width, m.GetRenderWidth())
			}

			layer := splashLayer(t, m)
			if layer.GetX() != left {
				t.Errorf("splash starts at x=%d, want the content region's left edge %d", layer.GetX(), left)
			}
			if r := layer.GetX() + layer.Width(); r > left+width {
				t.Errorf("splash spans x=%d..%d, the content region ends at %d", layer.GetX(), r, left+width)
			}
			if layer.GetY() != m.GetTopMargin() {
				t.Errorf("splash starts at y=%d, want the content region's top %d", layer.GetY(), m.GetTopMargin())
			}
			if b := layer.GetY() + layer.Height(); b > m.GetTopMargin()+m.GetUsableHeight() {
				t.Errorf("splash spans y=%d..%d, the content region ends at %d",
					layer.GetY(), b, m.GetTopMargin()+m.GetUsableHeight())
			}
		})
	}
}

// TestSplashCentersOnTheBoxNotTheScreen checks the art is centred in the
// content region rather than merely confined to it: the columns of blank space
// on either side of the box have to match.
func TestSplashCentersOnTheBoxNotTheScreen(t *testing.T) {
	withSidebar(t, true, "left", 30)
	m := newNarrowOS(t, 120, 40)

	layer := splashLayer(t, m)
	var widest, boxLeft int
	for _, ln := range strings.Split(layer.GetContent(), "\n") {
		trimmed := strings.TrimRight(ln, " ")
		if trimmed == "" {
			continue
		}
		if w := lipgloss.Width(trimmed); w > widest {
			widest = w
			boxLeft = w - lipgloss.Width(strings.TrimLeft(trimmed, " "))
		}
	}
	if widest == 0 {
		t.Fatal("the splash rendered nothing")
	}
	rightGap := m.GetContentWidth() - widest
	if diff := boxLeft - rightGap; diff < -1 || diff > 1 {
		t.Errorf("splash sits %d columns from the left and %d from the right of a %d column content region",
			boxLeft, rightGap, m.GetContentWidth())
	}
}

// TestSplashDegradesInANarrowContentRegion checks a content region too small for
// the bordered box falls back to a hint that fits, rather than spilling the
// border over the rail or the dock. The sizes here are the ones that used to
// overflow, plus the whole narrow-screen set with a rail on both edges.
func TestSplashDegradesInANarrowContentRegion(t *testing.T) {
	sizes := append([]struct {
		name string
		w, h int
	}{
		{"box-too-tall", 34, 8},
		{"glyph-rail", 40, 12},
		{"squeezed", 46, 14},
	}, narrowScreens...)

	for _, pos := range []string{"left", "right"} {
		for _, sc := range sizes {
			t.Run(pos+"/"+sc.name, func(t *testing.T) {
				withSidebar(t, true, pos, 30)
				m := newNarrowOS(t, sc.w, sc.h)
				contentW, contentH := m.GetContentWidth(), m.GetUsableHeight()

				layer := splashLayer(t, m)
				if layer.Height() > contentH {
					t.Errorf("splash is %d rows tall, the content region is %d", layer.Height(), contentH)
				}
				if layer.GetY()+layer.Height() > m.GetTopMargin()+contentH {
					t.Errorf("splash runs into the dock: y=%d h=%d", layer.GetY(), layer.Height())
				}
				for i, ln := range strings.Split(layer.GetContent(), "\n") {
					if w := lipgloss.Width(ln); w > contentW {
						t.Errorf("splash line %d is %d cells wide, the content region is %d", i, w, contentW)
					}
				}
				if !strings.Contains(layer.GetContent(), "new window") {
					t.Error("the splash never says how to make a window")
				}
			})
		}
	}
}
