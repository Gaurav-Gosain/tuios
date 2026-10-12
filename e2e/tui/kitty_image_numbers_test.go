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
// images at once, under two ids. d=a frees no image, and kitty keeps the
// older images under a number until they are deleted, so none of the three
// may be freed.
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

			for _, c := range cmds {
				if id := c.num("i"); (id == first || id == second || id == third) &&
					c.keys["a"] == "d" && c.keys["d"] == "I" {
					t.Errorf("an image under I=1 was freed, and nothing deleted it: %q", c.rawParam)
				}
			}
		})
	}
}

// d=N with p= deletes one placement of the image a number names. The image
// and its number stay while other placements remain. The first fix dropped
// the number on any d=N, so the guest could no longer place its image. At the
// end the host must show p=2 and p=3, 20 columns apart, and not p=1.
func TestKittyImageNumberDeletePlacement(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
			stream, _ := runImageNumbers(t, "delplace", daemon, false)
			cmds := parseNumberedStream(stream)
			logNumbered(t, cmds)
			id := hostIDBySize(t, cmds, 40, 40)

			// p=1 at column 2, p=2 at column 20, then p=1 deleted by
			// number and p=3 placed at column 40. Each guest placement
			// has host placement ids of its own, so the host's state is
			// replayed rather than read off one command.
			live := hostPlacements(stream, id)
			t.Logf("placements of host image %d at the end: %v", id, live)
			cols := map[int]bool{}
			row := -1
			for _, at := range live {
				cols[at[1]] = true
				if row >= 0 && at[0] != row {
					t.Errorf("the placements are on different rows: %v", live)
				}
				row = at[0]
			}
			if len(live) != 2 {
				t.Fatalf("host image %d has %d placements up, want p=2 and p=3", id, len(live))
			}
			minCol := 1 << 30
			for c := range cols {
				minCol = min(minCol, c)
			}
			if !cols[minCol+20] {
				t.Errorf("after d=N with p=1, I=2 no longer named its image, or p=1 stayed up: placements at %v", live)
			}
		})
	}
}

// Kitty answers an upload that names its image, and a placement, unless q=
// says not to: OK with the image's id, and its number when it has one, or an
// error. A guest that sends by number learns its image's id only from that
// answer. tuios answered no upload, by number or by id.
//
// The stand-in sends, and kitty answers as listed:
//   - I=3 and i=4 with no q: OK. I=5 with q=1 and i=8 with q=2: nothing.
//   - I=6 in two chunks: OK once, after the last.
//   - files: i=10 exists, OK; i=11 does not, ENOENT; i=12 a temporary file, OK.
//   - i=14 with I=13: EINVAL.
//   - a=p of i=19, which it has: OK. Of i=99 and I=77, which it has not: ENOENT.
//   - I=15 in two chunks, q=2 on the last only: nothing.
//   - I=16 in two chunks, the last naming I=16 again: OK, and I=17 right after
//     gets the next id, so the second chunk took no id of its own.
//   - i=18 whose payload is not a PNG: EBADPNG.
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
	got := uploadReplies(t, term.Snapshot())
	want := map[string]string{
		"I=3": "OK", "i=4": "OK", "I=6": "OK",
		"i=10": "OK", "i=11": "ENOENT:", "i=12": "OK",
		"I=13": "EINVAL:", "i=19": "OK", "i=99": "ENOENT:", "I=77": "ENOENT:",
		"I=16": "OK", "I=17": "OK", "i=18": "EBADPNG:",
	}
	for key, msg := range want {
		a, ok := got[key]
		switch {
		case !ok:
			t.Errorf("%s got no answer", key)
		case !strings.HasPrefix(a.msg, msg):
			t.Errorf("%s was answered %q, want %s", key, a.msg, msg)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("%s was answered, and kitty does not answer it", key)
		}
	}
	if got["I=3"].id != "" && got["I=3"].id == got["I=6"].id {
		t.Errorf("two uploads by number were given one id: %s", got["I=3"].id)
	}
	if a, b := got["I=16"].id, got["I=17"].id; a != "" && b != "" {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		if y != x+1 {
			t.Errorf("I=16 is %s and I=17 is %s: the second chunk of I=16 took an id of its own", a, b)
		}
	}
}

type uploadAnswer struct{ id, msg string }

// uploadReplies reads the REPLY lines the stand-in printed, keyed by the
// image number when the answer names one and by the id when not.
func uploadReplies(t *testing.T, screen string) map[string]uploadAnswer {
	t.Helper()
	got := map[string]uploadAnswer{}
	for _, m := range regexp.MustCompile(`REPLY (\S+)`).FindAllStringSubmatch(screen, -1) {
		r := m[1]
		p := regexp.MustCompile(`^(?:i=([1-9][0-9]*))?,?(?:I=([0-9]+))?(?:,p=[0-9]+)?;(OK|E[A-Z]+:\S*)$`).FindStringSubmatch(r)
		if p == nil || (p[1] == "" && p[2] == "") {
			t.Errorf("an answer is not one kitty gives: %q", r)
			continue
		}
		key := "i=" + p[1]
		if p[2] != "" {
			key = "I=" + p[2]
		}
		if _, dup := got[key]; dup {
			t.Errorf("%s was answered twice", key)
		}
		got[key] = uploadAnswer{p[1], p[3]}
	}
	t.Logf("answers the guest got: %v", got)
	return got
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
		_ = attachSecondKittyClient(t, base)
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
func attachSecondKittyClient(t *testing.T, base string) *kittyHost {
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
	return host
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

// A client that attaches after the guest sent images by number has not seen
// those uploads. It used to number the next upload from the start, while the
// daemon, which answers the guest, counted on. The guest places the image by
// the id the daemon told it, and on the late client that id named nothing, or
// another image. The client now takes the daemon's ids with the snapshot.
//
// The guest uploads I=1, a second client attaches, and the guest uploads I=2
// and places it by the id in the answer. On the late client's host, that
// placement must name the image the I=2 upload put there. The first client
// is the positive half: it saw every upload, and gets the same.
func TestKittyImageIDsForALateClient(t *testing.T) {
	bin := buildImageNumbers(t)
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		env:           []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:           host,
		daemonDefault: true,
	})
	killDaemon(t, base)
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 1, "one pane")
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)

	typeLine(t, term, bin+" late")
	if err := term.WaitForText("LATE-WAIT", shellTimeout); err != nil {
		t.Fatalf("the guest never uploaded I=1: %v\n%s", err, term.Snapshot())
	}
	lateHost := attachSecondKittyClient(t, base)
	lateHost.mark("attached")
	if err := term.Type("x"); err != nil {
		t.Fatalf("send the key: %v", err)
	}
	if err := term.WaitForText("IMAGENUMBERS-DONE", shellTimeout); err != nil {
		t.Fatalf("the guest never placed I=2: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(2 * time.Second)

	dir := artifactDir(t)
	for name, h := range map[string]*kittyHost{"first": host, "late": lateHost} {
		stream := h.bytes()
		if err := os.WriteFile(filepath.Join(dir, name+"-host-stream.bin"), stream, 0o644); err != nil {
			t.Errorf("save the host stream: %v", err)
		}
		cmds := parseNumberedStream(stream)
		logNumbered(t, cmds)
		id := hostIDBySize(t, cmds, 30, 60)
		placed := false
		for _, c := range cmds {
			if c.keys["a"] == "p" && c.num("i") == id && c.num("c") > 0 {
				placed = true
			}
		}
		if !placed {
			t.Errorf("%s client: the guest placed I=2 by the id it was told, and the host never placed image %d", name, id)
		}
	}
	saveArtifact(t, term, dir, "screen")
}

// cbirds frees its images with d=A as it quits. tuios forgot the images'
// ids without deleting them on the host, so every run left its sprites in
// the outer terminal, and closing the pane freed nothing either, because
// nothing named them any more.
//
// Two cbirds-like runs in one pane: the first quits with d=A, the second
// ends with d=c, which takes placements down and frees nothing. Then the pane
// closes. The first run's images must be deleted from the host before the
// second run uploads, and every image either run put there by the end. The
// images are on the host during each run, which is the positive half.
func TestKittyImagesFreedOnExitAndClose(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
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

			host.mark("launch")
			typeLine(t, term, bin+" cbirdsrun exit; "+bin+" cbirdsrun & sleep 2; echo RUNS\"\"-DONE")
			if err := term.WaitForText("RUNS-DONE", shellTimeout); err != nil {
				t.Fatalf("the runs never finished: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(time.Second)
			leaveTerminalMode(t, term)
			closeFocusedWindow(t, term, 0)
			time.Sleep(2 * time.Second)

			stream := host.bytes()
			if i := bytes.Index(stream, []byte(phaseMark+"launch")); i >= 0 {
				stream = stream[i:]
			}
			dir := artifactDir(t)
			if err := os.WriteFile(filepath.Join(dir, "host-stream.bin"), stream, 0o644); err != nil {
				t.Errorf("save the host stream: %v", err)
			}
			cmds := parseNumberedStream(stream)
			logNumbered(t, cmds)
			live := map[int]bool{}
			most, uploads := 0, 0
			for _, c := range cmds {
				id := c.num("i")
				switch {
				case (c.keys["a"] == "t" || c.keys["a"] == "T") && id != 0:
					uploads++
					if uploads == 6 && len(live) != 0 {
						t.Errorf("the second run started with %d images of the first still on the host: d=A freed nothing", len(live))
					}
					live[id] = true
				case c.keys["a"] == "d" && c.keys["d"] == "I" && c.keys["p"] == "":
					delete(live, id)
				}
				most = max(most, len(live))
			}
			if most < 5 {
				t.Fatalf("the runs never had their 5 images on the host: at most %d", most)
			}
			if len(live) != 0 {
				t.Errorf("%d images are left on the host after both runs and the close: %v", len(live), live)
			}
		})
	}
}

// A pane on another machine names a file for an upload. The path is a path
// over there, and this machine's daemon, which answers for the pane, used to
// open it here: OK, ENOENT and EBADF told the far pane which files exist on
// this machine. The daemon now refuses every file upload from such a pane
// without looking at the file.
//
// The stand-in runs in a pane on alpha and in a pane on this machine, in one
// session. On alpha every file upload is refused. On this machine the file
// that exists is OK and the one that does not is ENOENT, which is the
// positive half: the refusal is about where the pane runs.
func TestKittyUploadFileFromAnotherMachine(t *testing.T) {
	bin := buildImageNumbers(t)
	f := startGlobalFleet(t)

	run := func(host string) map[string]uploadAnswer {
		before := sessionWindowHosts(t, f.here, f.env, "home")
		args := []string{"new-window", "-s", "home"}
		if host != "" {
			args = append(args, "--host", host)
		}
		if out, err := tuiosCLIEnv(t, f.here, f.env, args...); err != nil {
			t.Fatalf("open a pane on %q: %v\n%s", host, err, out)
		}
		id, _, ok := waitNewWindow(t, f.here, f.env, "home", before, 10*time.Second)
		if !ok {
			t.Fatalf("the pane on %q never appeared", host)
		}
		time.Sleep(time.Second)
		if out, err := tuiosCLIEnv(t, f.here, f.env, "send-text", "-s", "home", "-w", id, bin+" reply"); err != nil {
			t.Fatalf("type the stand-in in %s: %v\n%s", id, err, out)
		}
		if out, err := tuiosCLIEnv(t, f.here, f.env, "send-keys", "-s", "home", "-w", id, "Enter"); err != nil {
			t.Fatalf("run the stand-in in %s: %v\n%s", id, err, out)
		}
		deadline := time.Now().Add(shellTimeout)
		var screen string
		for time.Now().Before(deadline) {
			out, _ := tuiosCLIEnv(t, f.here, f.env, "capture-pane", "-s", "home", "-w", id)
			screen = out
			if strings.Contains(out, "IMAGENUMBERS-DONE") {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !strings.Contains(screen, "IMAGENUMBERS-DONE") {
			t.Fatalf("the stand-in never finished on %q:\n%s", host, screen)
		}
		_ = os.WriteFile(filepath.Join(artifactDir(t), "pane-"+map[bool]string{true: "far", false: "here"}[host != ""]+".txt"), []byte(screen), 0o644)
		return uploadReplies(t, screen)
	}

	far := run("alpha")
	for _, key := range []string{"i=10", "i=11", "i=12"} {
		if a, ok := far[key]; !ok || !strings.HasPrefix(a.msg, "ENOTSUPPORTED:") {
			t.Errorf("a pane on alpha uploaded the file %s and was answered %q, want ENOTSUPPORTED", key, a.msg)
		}
	}
	here := run("")
	if a := here["i=10"]; a.msg != "OK" {
		t.Errorf("a pane on this machine uploaded a file that exists and was answered %q, want OK", a.msg)
	}
	if a := here["i=11"]; !strings.HasPrefix(a.msg, "ENOENT:") {
		t.Errorf("a pane on this machine uploaded a missing file and was answered %q, want ENOENT", a.msg)
	}
}

// One image can be placed at several points, as cbirds places one sprite
// for every bird that shows it. Each a=p without p= is a placement of its
// own, and an a=p with a p= the guest used before moves that placement. tuios
// kept one placement per image, so every a=p of an image replaced the last.
//
// The stand-in places one image three times in each of two frames, each frame
// starting with d=a, then twice with p=7. On the host, four placements of the
// image must be up at the end, at the points the guest gave: three in a row,
// and the p=7 one where the second a=p moved it.
func TestKittyMultiplePlacements(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(modeName(daemon), func(t *testing.T) {
			stream, _ := runImageNumbers(t, "multi", daemon, false)
			cmds := parseNumberedStream(stream)
			logNumbered(t, cmds)
			id := hostIDBySize(t, cmds, 40, 40)

			live := hostPlacements(stream, id)
			t.Logf("placements of host image %d at the end: %v", id, live)
			for p := range live {
				if strings.HasPrefix(p, "anonymous") {
					t.Errorf("a placement reached the host with no p=, so tuios cannot move or remove it alone")
				}
			}
			if len(live) != 4 {
				t.Fatalf("host image %d has %d placements up, want 4", id, len(live))
			}
			minRow, minCol := 1<<30, 1<<30
			for _, at := range live {
				minRow, minCol = min(minRow, at[0]), min(minCol, at[1])
			}
			want := map[[2]int]bool{{0, 0}: true, {0, 18}: true, {0, 38}: true, {10, 28}: true}
			for _, at := range live {
				rel := [2]int{at[0] - minRow, at[1] - minCol}
				if !want[rel] {
					t.Errorf("a placement is at %v from the first, which the guest never placed at; want %v", rel, want)
				}
				delete(want, rel)
			}
		})
	}
}

// movedPlacementRE is a cursor move followed by a graphics command, the way the
// passthrough positions every a=p.
var movedPlacementRE = regexp.MustCompile(`(?:\x1b\[(\d+);(\d+)H)?\x1b_G([^;\x1b]*)(?:;[^\x1b]*)?\x1b\\`)

// hostPlacements replays the host stream's placements and deletes of one host
// image and returns where each placement still up is, by host placement id.
// A placement with no p= gets an id of its own, as the host gives it one.
func hostPlacements(stream []byte, id int) map[string][2]int {
	live := map[string][2]int{}
	anon := 0
	row, col := 0, 0
	for _, m := range movedPlacementRE.FindAllSubmatch(stream, -1) {
		if len(m[1]) > 0 {
			row, _ = strconv.Atoi(string(m[1]))
			col, _ = strconv.Atoi(string(m[2]))
		}
		keys := map[string]string{}
		for _, kv := range strings.Split(string(m[3]), ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				keys[k] = v
			}
		}
		switch keys["a"] {
		case "p", "T":
			if keys["i"] != strconv.Itoa(id) || (keys["a"] == "T" && keys["p"] == "") {
				continue
			}
			p := keys["p"]
			if p == "" {
				anon++
				p = fmt.Sprintf("anonymous-%d", anon)
			}
			live[p] = [2]int{row, col}
		case "d":
			switch keys["d"] {
			case "a", "A", "":
				clear(live)
			case "i", "I":
				if keys["i"] != strconv.Itoa(id) {
					continue
				}
				if p := keys["p"]; p != "" {
					delete(live, p)
				} else {
					clear(live)
				}
			}
		}
	}
	return live
}
