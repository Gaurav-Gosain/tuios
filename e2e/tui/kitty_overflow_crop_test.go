package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
//   - A guest can name no cell box, as kitty icat does. The picture is then
//     200x20 cells at its own size, and the crop is 10 of its pixels a column
//     and 20 a row, which a guess at the host's cell would also give: these
//     cases check the clip, not the scale.
//   - A guest can send other pixels under the same id with a=t after the
//     picture is placed. That replaces the stored image and places nothing,
//     so the placement keeps the cell box and the crop it had.
//   - The bytes sent again can be the ones the host already holds. Nothing is
//     forwarded then, and the placement must not follow the cursor either: an
//     a=t places nothing, wherever the cursor is when it comes. The guest moves
//     the cursor to the top left first, where the whole 100x10 cells fit, so a
//     placement moved there is not clipped.
func TestKittyOverflowCropKeepsThePicturesScale(t *testing.T) {
	for _, tc := range []struct{ medium, how string }{
		{"direct", ""}, {"file", ""}, {"file-zlib", ""}, {"file-at", ""}, {"temp", ""},
		{"direct", "later"}, {"file", "later"}, {"temp", "later"},
		{"direct", "natural"}, {"file", "natural"}, {"temp", "natural"},
		{"file", "resend"}, {"temp", "resend"}, {"direct", "resend"},
		{"file", "same"}, {"temp", "same"},
	} {
		name := strings.TrimSuffix(tc.medium+"-"+tc.how, "-")
		boxCols, boxRows, perCol, perRow := 100, 10, 20, 40
		if tc.how == "natural" {
			boxCols, boxRows, perCol, perRow = 200, 20, 10, 20
		}
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
			typeLine(t, term, strings.TrimSpace("OVERFLOW_DIR="+t.TempDir()+" "+bin+" "+tc.medium+" "+tc.how))
			if err := term.WaitForText("OVERFLOW-DONE", shellTimeout); err != nil {
				t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
			}

			placed, history := settledPlacement(t, host, "launch")
			saveText(t, "placements-"+name, strings.Join(history, "\n")+"\n")
			if placed.cols <= 0 || placed.cols >= boxCols || placed.rows <= 0 || placed.rows >= boxRows {
				t.Fatalf("ASSERTION: the placement is %dx%d cells, want it clipped to fewer than %dx%d:\n  %s",
					placed.cols, placed.rows, boxCols, boxRows, strings.Join(history, "\n  "))
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
				if c.cols >= boxCols || c.rows >= boxRows {
					t.Fatalf("ASSERTION: the host is left showing %dx%d cells, want them clipped to fewer than %dx%d:\n  %s",
						c.cols, c.rows, boxCols, boxRows, strings.Join(history, "\n  "))
				}
				wantW, wantH := c.cols*perCol, c.rows*perRow
				if c.srcX != 0 || c.srcY != 0 || c.srcW != wantW || c.srcH != wantH {
					t.Fatalf("ASSERTION: %dx%d cells show the source rectangle x=%d,y=%d,w=%d,h=%d, want x=0,y=0,w=%d,h=%d, %d pixels a column and %d a row:\n  %s",
						c.cols, c.rows, c.srcX, c.srcY, c.srcW, c.srcH, wantW, wantH, perCol, perRow, strings.Join(history, "\n  "))
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

// buildOverflow compiles the overflow stand-in into the test's temporary
// directory, which is removed with the test.
func buildOverflow(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "overflow")
	build := exec.Command("go", "build", "-o", bin, "./overflow")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build overflow: %v\n%s", err, out)
	}
	return bin
}
