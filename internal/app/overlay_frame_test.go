package app

import (
	"image/color"
	"sync"
	"testing"

	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestPaneOverlayPalettesStayWithTheirClient holds what a pane-styled overlay
// is drawn with to the client that draws it. The SSH and web servers run one
// model per client in one process, and the frame ink (the session's tint) and
// the ground (the pane background) are each client's own.
//
// The ways it could fail, written down before the code:
//   - The ink or the ground kept in a package variable that each client
//     writes before it renders: two clients rendering at once draw with
//     whichever wrote last. The clients here render interleaved from several
//     goroutines, and each palette is compared with the one that client drew
//     alone; under -race the shared write is also reported.
//   - Two clients that happen to agree would pass all of that, so their
//     tints and grounds are picked far apart and the first check is that the
//     two palettes differ in ink, ground and cursor row.
func TestPaneOverlayPalettesStayWithTheirClient(t *testing.T) {
	was := theme.ColorProfile()
	theme.SetColorProfile(colorprofile.TrueColor)
	t.Cleanup(func() { theme.SetColorProfile(was) })

	client := func(accent, pane string) *OS {
		s := config.DefaultSettings()
		s.PaneOverlays, s.SessionBorder, s.SessionColors = true, true, true
		s.PaneBackground = pane
		a, ok := ParseAccent(accent)
		if !ok {
			t.Fatalf("ParseAccent(%q) failed", accent)
		}
		return &OS{
			Settings:       s,
			FocusedWindow:  -1,
			WorkspaceFocus: map[int]int{},
			NumWorkspaces:  9,
			SessionName:    "work",
			sessionColors:  map[string]Accent{"work": a},
		}
	}
	red, green := client("#ff3030", "#203040"), client("#30d030", "#f4f2ea")
	wantRed, wantGreen := red.overlayUI(), green.overlayUI()

	type field struct {
		name string
		get  func(overlay.Palette) color.Color
	}
	fields := []field{
		{"Frame", func(p overlay.Palette) color.Color { return p.Frame }},
		{"Surface", func(p overlay.Palette) color.Color { return p.Surface }},
		{"RowSel", func(p overlay.Palette) color.Color { return p.RowSel }},
		{"Fg", func(p overlay.Palette) color.Color { return p.Fg }},
	}
	same := func(a, b overlay.Palette, f field) bool {
		return safeColorEquals(f.get(a), f.get(b))
	}
	for _, f := range fields[:3] {
		if same(wantRed, wantGreen, f) {
			t.Fatalf("the two clients share %s %v: the test cannot tell them apart", f.name, f.get(wantRed))
		}
	}
	if !safeColorEquals(wantRed.Surface, mustColor(t, "#203040")) {
		t.Errorf("the red client's overlay ground is %v, want its pane background #203040", wantRed.Surface)
	}
	if !safeColorEquals(wantGreen.Surface, mustColor(t, "#f4f2ea")) {
		t.Errorf("the green client's overlay ground is %v, want its pane background #f4f2ea", wantGreen.Surface)
	}

	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				m, want, who := red, wantRed, "red"
				if (g+i)%2 == 1 {
					m, want, who = green, wantGreen, "green"
				}
				got := m.overlayUI()
				for _, f := range fields {
					if !same(got, want, f) {
						select {
						case errs <- who + " client drew " + f.name + " in another client's colour":
						default:
						}
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// mustColor parses a hex colour the way the config does.
func mustColor(t *testing.T, hex string) color.Color {
	t.Helper()
	c := theme.ConfigColor(hex)
	if isNilColor(c) {
		t.Fatalf("ConfigColor(%q) is nil", hex)
	}
	return c
}
