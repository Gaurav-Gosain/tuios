package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A guest that transmits an image with a=t and then places it with one a=p
// that carries no cell count saw nothing until it transmitted the same id
// again. The a=p reached the host, and the refresh pass deleted it in the
// same flush: the placement record had no size, so the image looked as if no
// part of it was on screen.
//
// The stand-in in ./placeonce sends exactly one a=t and one a=p and then
// nothing. The png cases send f=100 with no s= or v=, so the size has to come
// from the PNG's header. The image must end up placed on the host, at the size its pixels
// give it, and must stay placed.
func TestKittyPlaceAfterTransmitShows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport string
		daemon    bool
	}{
		{"b64", "b64", false},
		{"shm", "shm", false},
		{"daemon-b64", "b64", true},
		{"png", "png", false},
		{"pngz", "pngz", false},
		{"daemon-png", "png", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.transport == "shm" {
				requireDevShm(t)
			}
			bin := buildPlaceOnce(t)
			host := newKittyHost()
			term, base := start(t, startOpts{
				cols: 120, rows: 40,
				env:           []string{"TUIOS_SIXEL_GRAPHICS=0"},
				out:           host,
				daemonDefault: tc.daemon,
			})
			if tc.daemon {
				killDaemon(t, base)
			}
			host.answerProbe(t, term)
			waitBoot(t, term)
			newWindow(t, term)
			enableTiling(t, term)
			waitWindowCount(t, term, 1, "one pane")
			enterTerminalMode(t, term)
			runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)

			host.mark("launch")
			line := bin + " " + tc.transport
			if tc.transport == "shm" {
				line = shmPrefixEnv + "=" + standInShmPrefix(t) + " " + line
			}
			typeLine(t, term, line)
			if err := term.WaitForText("PLACEONCE-DONE", shellTimeout); err != nil {
				t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
			}
			// Long enough for several render passes, each of which could take
			// the placement down again.
			time.Sleep(2 * time.Second)

			stream := host.bytes()
			if i := bytes.Index(stream, []byte(phaseMark+"launch")); i >= 0 {
				stream = stream[i:]
			}
			assertPlacedAndKept(t, stream)
		})
	}
}

// assertPlacedAndKept finds the host id the 40x40 image (or the PNG) was
// transmitted under and checks that the last command naming it is a placement
// with a cell count.
func assertPlacedAndKept(t *testing.T, stream []byte) {
	t.Helper()
	cmds := wireCmds(stream)
	hostID := 0
	for _, c := range cmds {
		// A PNG goes out without s= and v=: its size is in its own header.
		png := strings.Contains(","+c.params+",", ",f=100,")
		if c.action == "t" && (png || (c.pixW == 40 && c.pixH == 40)) {
			hostID = c.image
		}
	}
	if hostID == 0 {
		t.Fatalf("the image never reached the host:\n%s", summarise(cmds))
	}
	var history []string
	var last wireCmd
	placed := 0
	for _, c := range cmds {
		if c.image != hostID || (c.action != "p" && c.action != "d") {
			continue
		}
		history = append(history, fmt.Sprintf("a=%s %s", c.action, c.params))
		last = c
		if c.action == "p" {
			placed++
		}
	}
	t.Logf("commands naming host image %d:\n  %s", hostID, strings.Join(history, "\n  "))
	if placed == 0 {
		t.Fatalf("the image was transmitted as %d and never placed", hostID)
	}
	if last.action != "p" {
		t.Fatalf("the image was placed %d times, then taken down by %q and never placed again",
			placed, last.params)
	}
	// The cell count follows from the image's pixels and the host's cell
	// size. A placement with none is the bug: the host is left to guess, and
	// the refresh pass reads the record as empty.
	if last.cols <= 0 || last.rows <= 0 {
		t.Fatalf("the image was placed with no cell count: %q", last.params)
	}
}

// buildPlaceOnce compiles the transmit-then-place stand-in once per test binary.
func buildPlaceOnce(t *testing.T) string {
	t.Helper()
	placeOnceOnce.Do(func() {
		dir, err := os.MkdirTemp("", "placeonce")
		if err != nil {
			placeOnceErr = err
			return
		}
		bin := filepath.Join(dir, "placeonce")
		build := exec.Command("go", "build", "-o", bin, "./placeonce")
		if out, err := build.CombinedOutput(); err != nil {
			placeOnceErr = fmt.Errorf("build placeonce: %v\n%s", err, out)
			return
		}
		placeOnceBin = bin
	})
	if placeOnceErr != nil {
		t.Fatalf("%v", placeOnceErr)
	}
	return placeOnceBin
}

var (
	placeOnceOnce sync.Once
	placeOnceBin  string
	placeOnceErr  error
)
