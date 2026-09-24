package capture

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestWithPaneBackground is the palette a single-pane capture is drawn in for
// each value of appearance.pane_background.
func TestWithPaneBackground(t *testing.T) {
	themed, warn := Palette("catppuccin_mocha")
	if warn != "" {
		t.Fatalf("the test theme did not resolve: %s", warn)
	}
	plain := shot.XTermPalette()
	custom := shot.RGB(0x12, 0x34, 0x56)

	cases := []struct {
		name    string
		in      *shot.Palette
		setting string
		themeID string
		wantBG  shot.Color
		same    bool // the palette comes back untouched
	}{
		{name: "off", in: themed, setting: "off", themeID: "catppuccin_mocha", same: true},
		{name: "empty", in: themed, setting: "", themeID: "catppuccin_mocha", same: true},
		{name: "theme", in: themed, setting: "theme", themeID: "catppuccin_mocha", same: true},
		{name: "theme with no theme", in: plain, setting: "theme", same: true},
		{name: "not a colour", in: themed, setting: "#fff", themeID: "catppuccin_mocha", same: true},
		{name: "colour with a theme", in: themed, setting: "#123456", themeID: "catppuccin_mocha", wantBG: custom},
		{name: "colour with no theme", in: plain, setting: "#123456", wantBG: custom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeBG, beforeFG := tc.in.BG, tc.in.FG
			got := WithPaneBackground(tc.in, tc.setting, tc.themeID)
			if tc.in.BG != beforeBG || tc.in.FG != beforeFG {
				t.Fatal("the palette passed in was modified")
			}
			if tc.same {
				if got != tc.in {
					t.Errorf("got a new palette %+v, want the one passed in", got)
				}
				return
			}
			if got.BG != tc.wantBG {
				t.Errorf("BG %v, want %v", got.BG, tc.wantBG)
			}
			if got.ANSI != tc.in.ANSI {
				t.Error("the sixteen changed")
			}
			if tc.themeID == "" {
				if got.FG != tc.in.FG {
					t.Errorf("with no theme FG moved to %v", got.FG)
				}
				return
			}
			if r := theme.ContrastRatio(got.FG, got.BG); r < theme.ContrastFloor {
				t.Errorf("FG %v on BG %v is %.2f:1, under the text floor", got.FG, got.BG, r)
			}
		})
	}
}
