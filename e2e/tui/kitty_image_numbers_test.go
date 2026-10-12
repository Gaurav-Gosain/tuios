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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
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
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
			stream, _ := runImageNumbers(t, "frames", daemon, false)
			assertImageNumbers(t, stream)
		})
	}
}

// Kitty makes a new image for every upload under a number, and the number
// names the newest. The images uploaded before keep their placements. The
// first fix reused one id per number, so the second a=T under I=1 replaced
// the first image and its placement.
//
// Three uploads go out under I=1: two placed as they go, one not placed.
// Then d=a takes every placement down. The host must have held both placed
// images at once, under two ids. Once their placements are gone, the two
// older images must be freed, as nothing but their id names them, and the
// newest must not.
func TestKittyImageNumberReupload(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
			stream, _ := runImageNumbers(t, "reupload", daemon, false)
			cmds := parseNumberedStream(stream)
			logNumbered(t, cmds)
			first := hostIDBySize(t, cmds, 40, 40)
			second := hostIDBySize(t, cmds, 30, 60)
			third := hostIDBySize(t, cmds, 20, 20)
			if first == second || second == third || first == third {
				t.Fatalf("uploads under one number share a host id: %d, %d, %d", first, second, third)
			}

			placed := map[int]bool{}
			most := 0
			for _, c := range cmds {
				id := c.num("i")
				switch c.keys["a"] {
				case "T", "p":
					placed[id] = true
				case "d":
					switch c.keys["d"] {
					case "a", "A", "":
						clear(placed)
					case "i", "I":
						delete(placed, id)
					}
				}
				if n := btoi(placed[first]) + btoi(placed[second]); n > most {
					most = n
				}
			}
			if most < 2 {
				t.Errorf("the two placed uploads under I=1 were never on the host at once")
			}

			for _, id := range []int{first, second} {
				if last := lastNaming(cmds, id); last.keys["a"] != "d" || last.keys["d"] != "I" {
					t.Errorf("host image %d lost its placements and was not freed; the last command naming it is %q",
						id, last.rawParam)
				}
			}
			for _, c := range cmds {
				if c.num("i") == third && c.keys["a"] == "d" && c.keys["d"] == "I" {
					t.Errorf("the newest image under I=1 was freed: %q", c.rawParam)
				}
			}
		})
	}
}

// d=N with p= deletes one placement of the image a number names. The image
// and its number stay while other placements remain. The first fix dropped
// the number on any d=N, so the guest could no longer place its image.
func TestKittyImageNumberDeletePlacement(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
			stream, _ := runImageNumbers(t, "delplace", daemon, false)
			cmds := parseNumberedStream(stream)
			logNumbered(t, cmds)
			id := hostIDBySize(t, cmds, 40, 40)

			deleted, placedAfter := -1, false
			placements := map[string]bool{}
			for i, c := range cmds {
				if c.num("i") != id {
					continue
				}
				switch {
				case c.keys["a"] == "p" && deleted < 0:
					placements[c.keys["p"]] = true
				case c.keys["a"] == "d" && c.keys["d"] == "I" && c.keys["p"] == "1":
					deleted = i
				case c.keys["a"] == "p" && c.keys["p"] == "3" && deleted >= 0:
					// With no cell count the refresh pass takes the
					// placement down again at once.
					placedAfter = c.num("c") > 0 && c.num("r") > 0
				}
			}
			if !placements["1"] || !placements["2"] {
				t.Fatalf("the image was not placed as p=1 and p=2 first: %v", placements)
			}
			if deleted < 0 {
				t.Fatalf("d=N,I=2,p=1 never reached the host as a=d,d=I,i=%d,p=1", id)
			}
			if !placedAfter {
				t.Errorf("after d=N with p=1, I=2 no longer named its image, or lost its size: no p=3 placement with a cell count reached the host")
			}
		})
	}
}

// Kitty answers an upload that names its image, unless q= says not to: OK
// with the image's id, and its number when it has one. A guest that sends by
// number learns its image's id only from that answer. tuios answered no
// upload, by number or by id. An upload sent as a file is answered OK only
// when the file can be opened, and with an error when it cannot.
//
// The stand-in sends I=3 and i=4 with no q, I=5 with q=1, i=8 with q=2, I=6
// in two chunks, and three files: i=10 exists, i=11 does not, and i=12 is a
// temporary file. Exactly six answers must come back, each once.
func TestKittyUploadReply(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
			_, term := runImageNumbers(t, "reply", daemon, false)
			assertUploadReplies(t, term)
		})
	}
}

// With two clients attached to one daemon pane, each client's emulator sees
// the upload. Answered by every client, the guest got one answer per client.
// The daemon answers instead, so the guest gets exactly one, and still gets
// it.
func TestKittyUploadReplyTwoClients(t *testing.T) {
	_, term := runImageNumbers(t, "reply", true, true)
	assertUploadReplies(t, term)
}

func assertUploadReplies(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	var replies []string
	for _, m := range regexp.MustCompile(`REPLY (\S+)`).FindAllStringSubmatch(term.Snapshot(), -1) {
		replies = append(replies, m[1])
	}
	t.Logf("answers the guest got: %q", replies)

	type answer struct{ id, msg string }
	got := map[string]answer{}
	for _, r := range replies {
		m := regexp.MustCompile(`^i=([1-9][0-9]*)(?:,I=([0-9]+))?;(OK|E[A-Z]+:\S*)$`).FindStringSubmatch(r)
		if m == nil {
			t.Errorf("an answer is not one kitty gives an upload: %q", r)
			continue
		}
		key := "i=" + m[1]
		if m[2] != "" {
			key = "I=" + m[2]
		}
		if _, dup := got[key]; dup {
			t.Errorf("the upload %s was answered twice", key)
		}
		got[key] = answer{m[1], m[3]}
	}
	for _, key := range []string{"I=3", "i=4", "I=6", "i=10", "i=12"} {
		if a, ok := got[key]; !ok {
			t.Errorf("the upload %s got no answer", key)
		} else if a.msg != "OK" {
			t.Errorf("the upload %s was answered %q, want OK", key, a.msg)
		}
	}
	if a, ok := got["i=11"]; !ok {
		t.Errorf("the upload of a missing file got no answer")
	} else if !strings.HasPrefix(a.msg, "ENOENT:") {
		t.Errorf("the upload of a missing file was answered %q, want ENOENT", a.msg)
	}
	for _, key := range []string{"I=5", "i=8"} {
		if _, ok := got[key]; ok {
			t.Errorf("the upload %s was answered through its q=", key)
		}
	}
	if len(replies) != 6 {
		t.Errorf("got %d answers, want 6", len(replies))
	}
	if got["I=3"].id != "" && got["I=3"].id == got["I=6"].id {
		t.Errorf("two uploads by number were given one id: %s", got["I=3"].id)
	}
}

func modeName(daemon bool) string {
	if daemon {
		return "daemon"
	}
	return "standalone"
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// runImageNumbers runs the stand-in in one mode in a fresh tuios and returns
// what the host was sent from the launch on. With second, a second client
// attaches to the daemon session before the launch, with a kitty host of its
// own. It saves the stream and the screen as the test's artifacts.
func runImageNumbers(t *testing.T, mode string, daemon, second bool) ([]byte, *tuitest.Terminal) {
	t.Helper()
	bin := buildImageNumbers(t)
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		env:           []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:           host,
		daemonDefault: daemon,
	})
	if daemon {
		killDaemon(t, base)
	}
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 1, "one pane")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)

	if second {
		attachSecondKittyClient(t, base)
	}

	host.mark("launch")
	typeLine(t, term, bin+" "+mode)
	if err := term.WaitForText("IMAGENUMBERS-DONE", shellTimeout); err != nil {
		t.Fatalf("the guest never finished: %v\n%s", err, term.Snapshot())
	}
	// Several render passes, each of which could take a placement down or
	// put a deleted one back.
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
	return stream, term
}

// attachSecondKittyClient attaches one more client to the only session of
// the daemon under base, on a host that draws kitty graphics, and waits until
// it shows the pane.
func attachSecondKittyClient(t *testing.T, base string) {
	t.Helper()
	var session string
	deadline := time.Now().Add(uiTimeout)
	for session == "" && time.Now().Before(deadline) {
		if names := attachedSessions(base); len(names) == 1 {
			session = names[0]
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if session == "" {
		t.Fatalf("no attached session to join under %s", base)
	}
	host := newKittyHost()
	other := startIn(t, base, startOpts{
		cols: 120, rows: 40,
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
		args: []string{"attach", session},
	})
	host.answerProbe(t, other)
	if err := other.WaitForText("IMAGEPANE", bootTimeout); err != nil {
		t.Fatalf("the second client never showed the pane: %v\n%s", err, other.Snapshot())
	}
	if n := len(attachedSessions(base)); n != 1 {
		t.Fatalf("the second client attached elsewhere: %d attached sessions", n)
	}
}

func logNumbered(t *testing.T, cmds []numberedCmd) {
	t.Helper()
	var lines []string
	for _, c := range cmds {
		lines = append(lines, c.rawParam)
	}
	t.Logf("graphics commands on the host:\n  %s", strings.Join(lines, "\n  "))
}

// hostIDBySize is the host id of the last upload of a w by h image.
func hostIDBySize(t *testing.T, cmds []numberedCmd, w, h int) int {
	t.Helper()
	id := 0
	for _, c := range cmds {
		if a := c.keys["a"]; (a == "t" || a == "T") && c.w == w && c.h == h {
			id = c.num("i")
		}
	}
	if id == 0 {
		t.Fatalf("no %dx%d image reached the host under an id", w, h)
	}
	return id
}

// lastNaming is the last placement or delete that names a host image.
func lastNaming(cmds []numberedCmd, id int) numberedCmd {
	var last numberedCmd
	for _, c := range cmds {
		if a := c.keys["a"]; c.num("i") == id && (a == "p" || a == "d" || a == "T") {
			last = c
		}
	}
	return last
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
