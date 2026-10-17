package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A guest that states one dimension of the cell box asks the host to fit the
// other one; one that states neither gets the picture's pixel size in cells, and
// one that states both is giving its own numbers.
//
// The stand-in in ./oneaxis sends one a=T per shape, or a=t and then a=p for the
// shape a guest places later, which is the other path into the sizing. TUIOS_CELL_SIZE pins the
// cells at 10x20px, as the other image tests do, so the aspect gives 6 rows for
// 12 columns of a 512px-wide picture where its pixel height would give 26. A
// square cannot tell the two axes apart, so the wide cases do.
func TestKittyPlacementSizingFromOneAxis(t *testing.T) {
	for _, tc := range []struct {
		name               string
		mode               string
		size               string
		wantCols, wantRows int
	}{
		{"width only keeps the aspect", "width", "512x512", 12, 6},
		{"width only on a wide picture", "width", "512x256", 12, 3},
		{"height only keeps the aspect", "height", "512x256", 16, 4},
		{"width only rounds up", "width", "512x300", 12, 4},
		{"both given are the guest's own", "both", "512x256", 12, 4},
		{"width only keeps the crop's aspect", "crop", "512x512", 12, 12},
		{"placed later keeps the aspect", "place", "512x512", 12, 6},
		{"neither given uses the pixel size", "none", "512x512", 52, 26},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildOneAxis(t)
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
			typeLine(t, term, bin+" "+tc.mode+" "+tc.size)
			if err := term.WaitForText("ONE-AXIS-DONE", shellTimeout); err != nil {
				t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
			}

			placed, history := settledPlacement(t, host, "launch")
			if placed.cols != tc.wantCols || placed.rows != tc.wantRows {
				t.Fatalf("placement box = %dx%d cells, want %dx%d:\n  %s",
					placed.cols, placed.rows, tc.wantCols, tc.wantRows, strings.Join(history, "\n  "))
			}
		})
	}
}

// settledPlacement waits until the render passes stop re-placing the image and
// returns the last placement they left. Two reads have to agree, as
// settledWindowCount does in the harness: the first read after the guest has
// finished can land between passes, and each pass after it re-places the image.
func settledPlacement(t *testing.T, host *kittyHost, phase string) (wireCmd, []string) {
	t.Helper()
	seen := -2
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		last, history := lastPlacementIn(wireCmds(host.bytes()), phase)
		if last.action == "p" && len(history) == seen {
			return last, history
		}
		seen = len(history)
		time.Sleep(60 * time.Millisecond)
	}
	last, history := lastPlacementIn(wireCmds(host.bytes()), phase)
	if last.action != "p" {
		t.Fatalf("the image was never left placed:\n  %s", strings.Join(history, "\n  "))
	}
	t.Fatalf("the placement never settled:\n  %s", strings.Join(history, "\n  "))
	return wireCmd{}, nil
}

// lastPlacementIn is the last placement or delete the host received in a phase,
// with every one it saw there.
func lastPlacementIn(cmds []wireCmd, phase string) (wireCmd, []string) {
	var last wireCmd
	var history []string
	for _, c := range cmds {
		if c.phase != phase || (c.action != "p" && c.action != "d") {
			continue
		}
		history = append(history, fmt.Sprintf("a=%s %s", c.action, c.params))
		last = c
	}
	return last, history
}

// buildOneAxis compiles the one-axis stand-in once per test binary.
func buildOneAxis(t *testing.T) string {
	t.Helper()
	oneAxisOnce.Do(func() {
		dir, err := os.MkdirTemp("", "oneaxis")
		if err != nil {
			oneAxisErr = err
			return
		}
		bin := filepath.Join(dir, "oneaxis")
		build := exec.Command("go", "build", "-o", bin, "./oneaxis")
		if out, err := build.CombinedOutput(); err != nil {
			oneAxisErr = fmt.Errorf("build oneaxis: %v\n%s", err, out)
			return
		}
		oneAxisBin = bin
	})
	if oneAxisErr != nil {
		t.Fatalf("%v", oneAxisErr)
	}
	return oneAxisBin
}

var (
	oneAxisOnce sync.Once
	oneAxisBin  string
	oneAxisErr  error
)
