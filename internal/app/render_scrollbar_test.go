package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// fillScrollback pushes enough lines through the emulator that the pane has a
// scrollback deep enough for a thumb shorter than the viewport, which is the
// last of windowNeedsScrollbar's arithmetic gates.
func fillScrollback(t *testing.T, win *terminal.Window, lines int) {
	t.Helper()
	win.LockIO()
	for i := range lines {
		_, _ = win.Terminal.Write([]byte("scrollback line " + itoa(i) + "\r\n"))
	}
	win.UnlockIO()
	win.MarkContentDirty()
	if n := win.ScrollbackLenSync(); n <= 0 {
		t.Fatalf("pane has no scrollback after %d lines: the test cannot exercise the scrollbar", lines)
	}
}

// withSharedBorders sets config.SharedBorders for the duration of fn.
func withSharedBorders(t *testing.T, shared bool, fn func()) {
	t.Helper()
	prev := config.SharedBorders
	config.SharedBorders = shared
	defer func() { config.SharedBorders = prev }()
	fn()
}

// The scrollbar's whole truth table, recorded rather than inferred.
//
// The thumb is drawn on the pane's right border cell. Whether a pane has one is
// decided by terminal.Window.ContentWidth/BorderOffset, which reserve border
// cells on !Tiled alone - so a Tiled pane's rectangle is guest output from edge
// to edge and there is no cell a thumb could take that is not the user's own
// terminal content. Tiling assigns Tiled from config.SharedBorders, which makes
// shared borders a deliberately scrollbar-free mode.
//
// Zoom is the case this pins hardest, because it is the one that reads like an
// exception and is not: a zoomed pane under shared borders is still borderless
// and still full-rect (renderSeparatorOverlay stands down for a zoomed pane
// rather than drawing it a frame), so it gets no thumb either. Without shared
// borders a tiled pane is Tiled=false and carries its own border box, zoomed or
// not, so the thumb has a real cell to sit on.
func TestWindowNeedsScrollbarTruthTable(t *testing.T) {
	cases := []struct {
		name   string
		tiled  bool
		zoomed bool
		shared bool
		want   bool
	}{
		{"floating pane", false, false, false, true},
		{"floating pane, shared borders on", false, false, true, true},
		{"bordered tiled pane", false, false, false, true},
		{"bordered tiled pane zoomed", false, true, false, true},
		{"borderless pane", true, false, true, false},
		// The reported case: zoom under shared borders stays borderless, so it
		// stays scrollbar-free. Not an oversight, and not a special case either.
		{"borderless pane zoomed", true, true, true, false},
		// Only reachable transiently, while a shared-borders toggle has landed
		// but the retile that resets Tiled has not. Borderless is borderless:
		// what decides is the flag the geometry uses, not the config setting.
		{"borderless pane zoomed, shared borders off", true, true, false, false},
		{"borderless pane, shared borders off", true, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			win := newTestWindow(t, "sbtt-"+strings.ReplaceAll(tc.name, " ", "-"), 60, 20)
			fillScrollback(t, win, 200)
			win.Tiled = tc.tiled
			win.Zoomed = tc.zoomed

			withSharedBorders(t, tc.shared, func() {
				if got := windowNeedsScrollbar(win); got != tc.want {
					t.Errorf("windowNeedsScrollbar = %v, want %v (tiled=%v zoomed=%v shared=%v)",
						got, tc.want, tc.tiled, tc.zoomed, tc.shared)
				}
			})
		})
	}
}

// windowNeedsScrollbar is consulted by four render paths (compositor cached,
// sync-hold, redraw, fullscreen fast path) to decide whether a thumb exists,
// while renderScrollbarLayer is what actually draws it. If the two ever
// disagree, the layout believes something the screen does not show, or the
// layer paints where the layout left no room. Pin that they agree on every row
// of the table above, plus the config gates.
func TestScrollbarLayerAgreesWithWindowNeedsScrollbar(t *testing.T) {
	type variant struct {
		name           string
		tiled, zoomed  bool
		shared, hide   bool
		borderStyle    string
		altScreen      bool
		emptyScrollbck bool
	}
	variants := []variant{
		{name: "bordered pane"},
		{name: "borderless pane", tiled: true, shared: true},
		{name: "borderless pane zoomed", tiled: true, zoomed: true, shared: true},
		{name: "bordered pane zoomed", zoomed: true},
		{name: "scrollbar disabled", hide: true},
		{name: "borders hidden", borderStyle: "hidden"},
		{name: "no scrollback", emptyScrollbck: true},
	}

	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			win := newTestWindow(t, "sbagree-"+strings.ReplaceAll(v.name, " ", "-"), 60, 20)
			if !v.emptyScrollbck {
				fillScrollback(t, win, 200)
			}
			win.Tiled = v.tiled
			win.Zoomed = v.zoomed

			prevHide, prevStyle := config.HideScrollbar, config.BorderStyle
			config.HideScrollbar = v.hide
			if v.borderStyle != "" {
				config.BorderStyle = v.borderStyle
			}
			t.Cleanup(func() {
				config.HideScrollbar, config.BorderStyle = prevHide, prevStyle
			})

			withSharedBorders(t, v.shared, func() {
				need := windowNeedsScrollbar(win)
				layer := renderScrollbarLayer(win, theme.BorderUnfocused(), 1)
				if need != (layer != nil) {
					t.Fatalf("windowNeedsScrollbar = %v but renderScrollbarLayer returned %v: "+
						"the layout and the layer disagree about whether a thumb is present",
						need, layer != nil)
				}
			})
		})
	}
}

// The layer renderer must refuse a borderless pane on its own, not only because
// its callers happen to check first. It is the code that writes the cells, and
// on a borderless pane those cells are the guest's output.
func TestScrollbarLayerRefusesBorderlessPaneDirectly(t *testing.T) {
	win := newTestWindow(t, "sbdirect-0001", 60, 20)
	fillScrollback(t, win, 200)
	win.Tiled = true

	withSharedBorders(t, true, func() {
		for _, zoomed := range []bool{false, true} {
			win.Zoomed = zoomed
			if layer := renderScrollbarLayer(win, theme.BorderUnfocused(), 1); layer != nil {
				t.Errorf("renderScrollbarLayer drew a thumb on a borderless pane (zoomed=%v); "+
					"it would land on the rightmost column of terminal output", zoomed)
			}
		}
	})
}

// The pairing that matters on screen: a pane gets a thumb exactly when it gets
// a border box to hang it on. renderWindowBox and windowNeedsScrollbar read the
// same predicate, and this asserts against the rendered box rather than against
// the predicate, so a future divergence between them is caught by what the user
// would see.
func TestThumbAppearsExactlyWhenTheBoxHasABorder(t *testing.T) {
	cases := []struct {
		name          string
		tiled, zoomed bool
		shared        bool
	}{
		{"bordered pane", false, false, false},
		{"bordered pane zoomed", false, true, false},
		{"borderless pane", true, false, true},
		{"borderless pane zoomed", true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			win := newTestWindow(t, "sbbox-"+strings.ReplaceAll(tc.name, " ", "-"), 60, 20)
			fillScrollback(t, win, 200)
			// SetTiled rather than the bare flag: it re-syncs the emulator to
			// the borderless size, which is what makes the rendered box the
			// pane's full rectangle. Setting the flag alone leaves the emulator
			// two cells short in each axis and the box would not be what the
			// compositor actually composites.
			win.SetTiled(tc.tiled)
			win.Zoomed = tc.zoomed
			m := newTestOS(win)

			withSharedBorders(t, tc.shared, func() {
				box := m.renderWindowBox(win, 0, true, theme.BorderUnfocused())
				lines := strings.Split(box, "\n")
				hasBorder := strings.Contains(lines[len(lines)-1], config.GetWindowBorderBottomLeft())

				if got := windowNeedsScrollbar(win); got != hasBorder {
					t.Fatalf("box has a border = %v but windowNeedsScrollbar = %v: "+
						"a thumb without a border cell lands on terminal content, and a border "+
						"with no thumb loses the scroll position the pane could show",
						hasBorder, got)
				}

				// A borderless pane must also be exactly its rectangle, which is
				// what leaves no cell to spare in the first place.
				if !hasBorder {
					w, h := lipgloss.Size(box)
					if w != win.ContentWidth() || h != win.ContentHeight() {
						t.Errorf("borderless box is %dx%d, want the pane's full rect %dx%d",
							w, h, win.ContentWidth(), win.ContentHeight())
					}
				}
			})
		})
	}
}
