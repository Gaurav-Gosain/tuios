package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCardPreviewDrawsTheSelectedPicture: the card preview draws a picture
// attachment it selects, as block glyphs on a host without kitty graphics
// and as a placed kitty image on a host with them (the docs worker's scene:
// 108x30, a side panel 56 wide).
//
// The bug this holds: the picture was prepared only when its file came in,
// and then the width it is drawn at was not known yet, so it was never
// prepared and its rows stayed blank on either host.
//
// How it could pass wrongly: the size line names the picture whatever its
// rows hold, so the rows under it are read: they must hold glyphs, or, on
// the kitty host, the host must have a picture placed on them.
//
// Artifact: a frame per host.
func TestCardPreviewDrawsTheSelectedPicture(t *testing.T) {
	for _, kitty := range []bool{false, true} {
		name := "blocks"
		if kitty {
			name = "kitty"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			writeConfig(t, base, "[agents]\ncard_preview = \"side\"\ncard_preview_width = \"56\"\n")
			mustCLI(t, base, "new", "S", "--detach")
			waitForSessionInfo(t, base, "S")
			mustCLI(t, base, "task", "add", "-s", "S", "-p", "Show the latency.", "chart the latency")
			pic := filepath.Join(base, "latency.png")
			if err := os.WriteFile(pic, gradientPNG(t, 1280, 720), 0o644); err != nil {
				t.Fatal(err)
			}
			mustCLI(t, base, "task", "attach", "-s", "S", "t1", pic)

			opts := startOpts{args: []string{"attach", "S"}, cols: 108, rows: 30, shippedLooks: true}
			host := &lockedBuffer{}
			opts.out = host
			opts.env = []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
			if !kitty {
				// A host that draws no graphics, as image_symbols_test.go makes
				// one: the picture is block glyphs.
				opts.env = append(opts.env, "TUIOS_CELL_SIZE=10x20", "TUIOS_SIXEL_GRAPHICS=0", "TUIOS_KITTY_GRAPHICS=0")
			}
			term := startIn(t, base, opts)
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
				t.Fatalf("open the agents view: %v", err)
			}
			waitText(t, term, "the agents view", "Board")
			if err := term.SendKeys("b"); err != nil {
				t.Fatalf("press b: %v", err)
			}
			waitText(t, term, "the board's columns", "Todo  1")
			if err := term.SendKeys(" "); err != nil {
				t.Fatalf("press space: %v", err)
			}
			waitText(t, term, "the picture's size line", "1280x720")
			if kitty {
				if err := term.WaitFor(func(tuitest.Screen) bool {
					return len(hostPictures(t, []byte(host.String()))) > 0
				}, uiTimeout); err != nil {
					t.Fatalf("ASSERTION: the kitty host was given no picture for the selected attachment: %v\n%s", err, term.Snapshot())
				}
				saveCockpitFrames(t, term, "card-preview-picture-kitty", hostPictures(t, []byte(host.String()))...)
				return
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				r := rowWith(s, "1280x720")
				if r < 0 {
					return false
				}
				line := []rune(s.Line(r + 1))
				panel := string(line[min(len(line), 56):])
				return strings.ContainsAny(panel, "▀▄█▌▐▖▗▘▝▚▞▙▛▜▟")
			}, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: the picture's rows hold no block glyphs: %v\n%s", err, term.Snapshot())
			}
			saveCockpitFrames(t, term, "card-preview-picture-blocks")
		})
	}
}
