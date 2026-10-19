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
func TestKittyOverflowCropKeepsThePicturesScale(t *testing.T) {
	for _, medium := range []string{"direct", "file", "file-zlib", "file-at", "temp"} {
		t.Run(medium, func(t *testing.T) {
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
			typeLine(t, term, bin+" "+medium)
			if err := term.WaitForText("OVERFLOW-DONE", shellTimeout); err != nil {
				t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
			}

			placed, history := settledPlacement(t, host, "launch")
			saveText(t, "placements-"+medium, strings.Join(history, "\n")+"\n")
			if placed.cols <= 0 || placed.cols >= 100 || placed.rows <= 0 || placed.rows >= 10 {
				t.Fatalf("ASSERTION: the placement is %dx%d cells, want it clipped to fewer than 100x10:\n  %s",
					placed.cols, placed.rows, strings.Join(history, "\n  "))
			}
			wantW, wantH := placed.cols*20, placed.rows*40
			if placed.srcX != 0 || placed.srcY != 0 || placed.srcW != wantW || placed.srcH != wantH {
				t.Fatalf("ASSERTION: %dx%d cells show the source rectangle x=%d,y=%d,w=%d,h=%d, want x=0,y=0,w=%d,h=%d, 20 pixels a column and 40 a row:\n  %s",
					placed.cols, placed.rows, placed.srcX, placed.srcY, placed.srcW, placed.srcH, wantW, wantH, strings.Join(history, "\n  "))
			}
		})
	}
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
