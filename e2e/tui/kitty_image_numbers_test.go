package tuie2e

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Issue #644: cbirds --render kitty drew nothing inside tuios. It names every
// image by number (I=) and never by id (i=). tuios forwarded the transmission
// with I= as the guest wrote it, so the host filed the image under an id of
// its own choosing, and then sent every a=p under one host id it had
// allocated for "image 0", which nothing was ever transmitted under.
//
// The stand-in in ./imagenumbers transmits two PNGs by number and one by id,
// places all three in two synchronized frames the way cbirds does, and then
// frees one of the numbered images with d=N. On the host, every image must go
// out under its own host id with no I= left on it, every placement must name
// an id that was transmitted, and the cell counts must follow from each PNG's
// own size. The image sent by id is the positive half: that path worked
// before and must still work.
func TestKittyImageNumbers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		daemon bool
	}{
		{"standalone", false},
		{"daemon", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := buildImageNumbers(t)
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
			typeLine(t, term, bin)
			if err := term.WaitForText("IMAGENUMBERS-DONE", shellTimeout); err != nil {
				t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
			}
			// Several render passes, each of which could take a placement
			// down or put a deleted one back.
			time.Sleep(2 * time.Second)

			stream := host.bytes()
			if i := bytes.Index(stream, []byte(phaseMark+"launch")); i >= 0 {
				stream = stream[i:]
			}
			dir := artifactDir(t)
			if err := os.WriteFile(filepath.Join(dir, "host-stream.bin"), stream, 0o644); err != nil {
				t.Errorf("save the host stream: %v", err)
			}
			saveArtifact(t, term, dir, "screen")
			t.Logf("host stream and screen saved in %s", dir)

			assertImageNumbers(t, stream)
		})
	}
}

// numberedCmd is one graphics command on the host wire, with its keys and,
// for a transmission, the size of the image it carries.
type numberedCmd struct {
	keys     map[string]string
	w, h     int
	rawParam string
}

func (c numberedCmd) num(key string) int {
	n, _ := strconv.Atoi(c.keys[key])
	return n
}

// parseNumberedStream reads every graphics command from the host stream. A
// transmission split over chunks is joined, and its pixel size is taken from
// s= and v= or, for a PNG, from the PNG header.
func parseNumberedStream(stream []byte) []numberedCmd {
	var out []numberedCmd
	var payload strings.Builder
	open := -1
	for _, m := range graphicsRE.FindAllSubmatch(stream, -1) {
		c := numberedCmd{keys: map[string]string{}, rawParam: string(m[1])}
		for _, kv := range strings.Split(string(m[1]), ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				c.keys[k] = v
			}
		}
		if open >= 0 && c.keys["a"] == "" {
			// A continuation chunk of the transmission still open.
			payload.Write(m[2])
			if c.keys["m"] != "1" {
				out[open].w, out[open].h = transmittedSize(out[open], payload.String())
				open = -1
			}
			continue
		}
		out = append(out, c)
		if a := c.keys["a"]; a == "t" || a == "T" {
			payload.Reset()
			payload.Write(m[2])
			if c.keys["m"] == "1" {
				open = len(out) - 1
			} else {
				out[len(out)-1].w, out[len(out)-1].h = transmittedSize(c, payload.String())
			}
		}
	}
	return out
}

func transmittedSize(c numberedCmd, payload string) (int, int) {
	if w, h := c.num("s"), c.num("v"); w > 0 && h > 0 {
		return w, h
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func assertImageNumbers(t *testing.T, stream []byte) {
	t.Helper()
	cmds := parseNumberedStream(stream)

	// Which host id each of the three images went out under, found by its
	// size, which only the guest's own PNG carries.
	type want struct {
		label   string
		w, h    int
		deleted bool
	}
	images := []want{
		{"I=7", 40, 40, false},
		{"I=9", 30, 60, true},
		{"i=5", 20, 20, false},
	}
	hostOf := map[string]int{}
	transmitted := map[int]bool{}
	var log []string
	for _, c := range cmds {
		log = append(log, c.rawParam)
		a := c.keys["a"]
		if _, ok := c.keys["I"]; ok {
			t.Errorf("an image number reached the host, which assigns its own ids for it: %q", c.rawParam)
		}
		if a != "t" && a != "T" {
			continue
		}
		id := c.num("i")
		if id == 0 {
			t.Errorf("an image went to the host with no id: %q (%dx%d)", c.rawParam, c.w, c.h)
			continue
		}
		transmitted[id] = true
		for _, img := range images {
			if c.w == img.w && c.h == img.h {
				hostOf[img.label] = id
			}
		}
	}
	t.Logf("graphics commands on the host:\n  %s", strings.Join(log, "\n  "))
	seen := map[int]string{}
	for _, img := range images {
		id := hostOf[img.label]
		if id == 0 {
			t.Fatalf("image %s (%dx%d) never reached the host", img.label, img.w, img.h)
		}
		if other, dup := seen[id]; dup {
			t.Fatalf("images %s and %s went to the host under the same id %d", other, img.label, id)
		}
		seen[id] = img.label
	}

	// Every placement must name an image the host holds.
	for _, c := range cmds {
		if c.keys["a"] == "p" && !transmitted[c.num("i")] {
			t.Errorf("a placement names host image %d, which was never transmitted: %q", c.num("i"), c.rawParam)
		}
	}

	// The cell counts of every placement, by image, to be checked against
	// one cell size below.
	cells := map[string]map[[2]int]bool{}
	for _, img := range images {
		id := hostOf[img.label]
		cells[img.label] = map[[2]int]bool{}
		var history []string
		var last numberedCmd
		placed := 0
		for _, c := range cmds {
			a := c.keys["a"]
			if c.num("i") != id || (a != "p" && a != "d") {
				continue
			}
			history = append(history, c.rawParam)
			last = c
			if a == "p" {
				placed++
				cells[img.label][[2]int{c.num("c"), c.num("r")}] = true
			}
		}
		t.Logf("image %s is host image %d:\n  %s", img.label, id, strings.Join(history, "\n  "))
		if placed == 0 {
			t.Errorf("image %s was transmitted as host image %d and never placed", img.label, id)
			continue
		}
		switch {
		case img.deleted && (last.keys["a"] != "d" || last.keys["d"] != "I"):
			t.Errorf("image %s was freed by number and the host was last told %q", img.label, last.rawParam)
		case !img.deleted && last.keys["a"] != "p":
			t.Errorf("image %s was placed and then taken down by %q", img.label, last.rawParam)
		}
	}
	assertOneCellSize(t, cells, func(label string) (int, int) {
		for _, img := range images {
			if img.label == label {
				return img.w, img.h
			}
		}
		return 0, 0
	})
}

// assertOneCellSize checks that every image was placed at its own pixel size:
// one cell size must turn each image's pixels into the cells it was placed
// in. A placement sized from a guess, or from no size at all, fits no cell
// size the others fit.
func assertOneCellSize(t *testing.T, cells map[string]map[[2]int]bool, size func(string) (int, int)) {
	t.Helper()
	ceil := func(a, b int) int { return (a + b - 1) / b }
	for cw := 1; cw <= 64; cw++ {
		for ch := 1; ch <= 64; ch++ {
			fits := true
			for label, seen := range cells {
				w, h := size(label)
				for cr := range seen {
					if cr[0] != ceil(w, cw) || cr[1] != ceil(h, ch) {
						fits = false
					}
				}
			}
			if fits {
				t.Logf("every placement fits a %dx%d px cell: %v", cw, ch, cells)
				return
			}
		}
	}
	t.Errorf("no one cell size gives the cell counts the images were placed in: %v", cells)
}

// buildImageNumbers compiles the image-number stand-in once per test binary.
func buildImageNumbers(t *testing.T) string {
	t.Helper()
	imageNumbersOnce.Do(func() {
		dir, err := os.MkdirTemp("", "imagenumbers")
		if err != nil {
			imageNumbersErr = err
			return
		}
		bin := filepath.Join(dir, "imagenumbers")
		build := exec.Command("go", "build", "-o", bin, "./imagenumbers")
		if out, err := build.CombinedOutput(); err != nil {
			imageNumbersErr = fmt.Errorf("build imagenumbers: %v\n%s", err, out)
			return
		}
		imageNumbersBin = bin
	})
	if imageNumbersErr != nil {
		t.Fatalf("%v", imageNumbersErr)
	}
	return imageNumbersBin
}

var (
	imageNumbersOnce sync.Once
	imageNumbersBin  string
	imageNumbersErr  error
)
