package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A picture placed past its pane's edge is cropped to the cells that show, and
// the crop is in the picture's own pixels. The guest in ./overflow scales a
// 2000x400 PNG into 100x10 cells, so a column holds 20 of its pixels and a row
// 40, and only the top left 41x4 cells are inside the pane. The host has to be
// told to draw 20 pixels a column and 40 a row of it into those cells.
//
// How this could pass wrongly, written down first:
//   - The crop could be measured in the host's cell, which the test pins at
//     10x20. That shows the picture at its own size, a slice half as wide and
//     half as tall as the cells it is given. The picture's scale is twice the
//     cell on both axes so that this cannot agree by chance.
//   - Only the bytes sent inline could carry the picture's size. Every way a
//     guest can send a PNG runs: inline, a file, a file compressed again, a
//     file with the PNG after other bytes, and a temporary file.
//   - The placement could escape the crop by not being clipped at all. It must
//     be fewer cells than the 100x10 asked for, on both axes.
//   - A guest can store the picture first (a=t) and place it after (a=p), as
//     snacks.nvim does. Storing shows nothing, so a placement made for the a=t
//     at the picture's own size is a second, unscaled copy on the screen.
//     So the check is on what the host is left showing: the last command for
//     each placement id, not only the last placement of all.
func TestKittyOverflowCropKeepsThePicturesScale(t *testing.T) {
	for _, tc := range []struct{ medium, how string }{
		{"direct", ""}, {"file", ""}, {"file-zlib", ""}, {"file-at", ""}, {"temp", ""},
		{"direct", "later"}, {"file", "later"}, {"temp", "later"},
	} {
		name := strings.TrimSuffix(tc.medium+"-"+tc.how, "-")
		t.Run(name, func(t *testing.T) {
			bin := buildOverflow(t)
			host := newKittyHost()
			term, _ := start(t, startOpts{
				cols: 120, rows: 40,
				env: []string{"TUIOS_SIXEL_GRAPHICS=0", "TUIOS_CELL_SIZE=10x20"},
				out: host,
			})
			host.answerProbe(t, term)
			waitBoot(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			waitWindowCount(t, term, 1, "one pane")
			enterTerminalMode(t, term)

			host.mark("launch")
			typeLine(t, term, strings.TrimSpace(bin+" "+tc.medium+" "+tc.how))
			if err := term.WaitForText("OVERFLOW-DONE", shellTimeout); err != nil {
				t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
			}

			placed, history := settledPlacement(t, host, "launch")
			saveText(t, "placements-"+name, strings.Join(history, "\n")+"\n")
			if placed.cols <= 0 || placed.cols >= 100 || placed.rows <= 0 || placed.rows >= 10 {
				t.Fatalf("ASSERTION: the placement is %dx%d cells, want it clipped to fewer than 100x10:\n  %s",
					placed.cols, placed.rows, strings.Join(history, "\n  "))
			}
			shown := map[string]wireCmd{}
			for _, c := range wireCmds(host.bytes()) {
				if c.phase != "launch" {
					continue
				}
				switch id := placementIDOf(c.params); {
				case c.action == "p":
					shown[id] = c
				case c.action == "d" && id != "":
					delete(shown, id)
				case c.action == "d":
					clear(shown)
				}
			}
			for _, c := range shown {
				wantW, wantH := c.cols*20, c.rows*40
				if c.srcX != 0 || c.srcY != 0 || c.srcW != wantW || c.srcH != wantH {
					t.Fatalf("ASSERTION: %dx%d cells show the source rectangle x=%d,y=%d,w=%d,h=%d, want x=0,y=0,w=%d,h=%d, 20 pixels a column and 40 a row:\n  %s",
						c.cols, c.rows, c.srcX, c.srcY, c.srcW, c.srcH, wantW, wantH, strings.Join(history, "\n  "))
				}
			}
		})
	}
}

// placementIDOf is the p= key of a kitty command's parameters, "" for none.
func placementIDOf(params string) string {
	for _, kv := range strings.Split(params, ",") {
		if v, ok := strings.CutPrefix(kv, "p="); ok {
			return v
		}
	}
	return ""
}

// saveText writes text to the test's artifact directory.
func saveText(t *testing.T, name, text string) {
	t.Helper()
	path := filepath.Join(artifactDir(t), name+".txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Logf("save %s: %v", path, err)
	}
}

// buildOverflow compiles the overflow stand-in once per test binary.
func buildOverflow(t *testing.T) string {
	t.Helper()
	overflowOnce.Do(func() {
		dir, err := os.MkdirTemp("", "overflow")
		if err != nil {
			overflowErr = err
			return
		}
		bin := filepath.Join(dir, "overflow")
		build := exec.Command("go", "build", "-o", bin, "./overflow")
		if out, err := build.CombinedOutput(); err != nil {
			overflowErr = fmt.Errorf("build overflow: %v\n%s", err, out)
			return
		}
		overflowBin = bin
	})
	if overflowErr != nil {
		t.Fatalf("%v", overflowErr)
	}
	return overflowBin
}

var (
	overflowOnce sync.Once
	overflowBin  string
	overflowErr  error
)
