package tuie2e

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The daemon paces a pane that streams kitty graphics to its fastest client. A
// client that is behind skips frames: a frame still waiting for it when the
// next frame of the same image arrives is dropped from its queue, whole. The
// pane is held only while every client is behind, and one hold ends after
// 250 ms. These tests run a real guest that streams frames, two real clients
// attached to one daemon, and stop a client with SIGSTOP to make it slow or
// stuck.

// frameHost plays a kitty terminal like kittyHost, but counts frames as they
// arrive instead of keeping every byte, so a long stream does not fill memory.
type frameHost struct {
	mu      sync.Mutex
	tail    []byte
	frames  int
	deletes int
	probe   chan struct{}
}

func newFrameHost() *frameHost { return &frameHost{probe: make(chan struct{}, 1)} }

var hostKeyRE = regexp.MustCompile(`(?:^|,)([a-zA-Z])=([^,]*)`)

func (h *frameHost) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(da1Query)) {
		select {
		case h.probe <- struct{}{}:
		default:
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	data := append(h.tail, p...)
	h.tail = nil
	pos := 0
	for {
		k := bytes.Index(data[pos:], []byte("\x1b_G"))
		if k < 0 {
			h.tail = append([]byte(nil), data[max(pos, len(data)-2):]...)
			break
		}
		start := pos + k
		e := bytes.IndexAny(data[start+3:], ";\x1b")
		if e < 0 {
			if len(data)-start < 4096 {
				h.tail = append([]byte(nil), data[start:]...)
			}
			break
		}
		keys := map[string]string{}
		for _, m := range hostKeyRE.FindAllStringSubmatch(string(data[start+3:start+3+e]), -1) {
			keys[m[1]] = m[2]
		}
		if id, _ := strconv.Atoi(keys["i"]); id != 0 {
			switch keys["a"] {
			case "t":
				if s, _ := strconv.Atoi(keys["s"]); s > 0 {
					h.frames++
				}
			case "d":
				h.deletes++
			}
		}
		pos = start + 3 + e
	}
	return len(p), nil
}

func (h *frameHost) counts() (frames, deletes int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.frames, h.deletes
}

// pacingClient is one tuios client attached to the session under test.
type pacingClient struct {
	term *tuitest.Terminal
	host *frameHost
}

const pacingCols, pacingRows = 100, 30

func attachPacingClient(t *testing.T, base, session string) *pacingClient {
	t.Helper()
	host := newFrameHost()
	term := startIn(t, base, startOpts{
		cols: pacingCols, rows: pacingRows,
		args: []string{"attach", session},
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
	})
	answerKittyProbe(t, term, host.probe, pacingCols, pacingRows)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	return &pacingClient{term: term, host: host}
}

// stop and resume make a client stop reading, the way a client on a stalled
// link or a suspended laptop does.
func (c *pacingClient) stop(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(c.term.Pid(), syscall.SIGSTOP); err != nil {
		t.Fatalf("stop the client: %v", err)
	}
}

func (c *pacingClient) resume() { _ = syscall.Kill(c.term.Pid(), syscall.SIGCONT) }

// startPacedGuest starts a session with one pane, attaches a client, and runs
// the frameloop guest in the pane with the given transport. It returns the
// client and the file the guest counts its frames in.
func startPacedGuest(t *testing.T, transport string) (base string, first *pacingClient, count string) {
	t.Helper()
	base = t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "pacing", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	first = attachPacingClient(t, base, "pacing")
	time.Sleep(insertGuard + 150*time.Millisecond)
	runInShell(t, first.term, "echo PACE\"\"PANE", "PACEPANE", shellTimeout)

	bin := buildFrameloop(t)
	dir := t.TempDir()
	geom, count := filepath.Join(dir, "geom"), filepath.Join(dir, "count")
	typeLine(t, first.term, fmt.Sprintf("%s %s 60 0 %s %s", bin, geom, transport, count))
	deadline := time.Now().Add(shellTimeout)
	for guestFrames(count) < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("the guest never wrote a frame\n%s", first.term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s := announcedSizes(geom); len(s) > 0 && transport != "text" {
		t.Logf("pane %dx%d cells, %dx%d px, about %d KB a frame as base64",
			s[0][0], s[0][1], s[0][2], s[0][3], s[0][2]*s[0][3]*4*4/3/1024)
	}
	return base, first, count
}

// guestFrames is how many frames the guest has written. The guest rewrites
// the file for every frame through a rename, so a read never finds it empty.
// A file that cannot be parsed is tried again, and a count of 0 means only that
// no frame was written yet.
func guestFrames(count string) int {
	for range 100 {
		b, err := os.ReadFile(count)
		if err != nil {
			return 0
		}
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			return n
		}
		time.Sleep(time.Millisecond)
	}
	return 0
}

// rate counts, over d, the frames the guest wrote and the frames each client's
// host received.
func rate(count string, d time.Duration, clients ...*pacingClient) (guest int, got []int) {
	g0 := guestFrames(count)
	before := make([]int, len(clients))
	for i, c := range clients {
		before[i], _ = c.host.counts()
	}
	time.Sleep(d)
	got = make([]int, len(clients))
	for i, c := range clients {
		f, _ := c.host.counts()
		got[i] = f - before[i]
	}
	return guestFrames(count) - g0, got
}

// pacingTotals adds up the daemon's pacing lines: how many times it held the
// guest and for how long, how many frames it skipped for a client, and how
// many client streams it cut because their queue filled.
type pacingTotals struct {
	held, skipped, cut int
	heldFor            time.Duration
}

var pacingLineRE = regexp.MustCompile(`held (\d+) times for (\S+), .* (\d+) frames skipped, (\d+) streams cut`)

func sumPacing(lines []string) pacingTotals {
	var p pacingTotals
	for _, l := range lines {
		if m := pacingLineRE.FindStringSubmatch(l); m != nil {
			h, _ := strconv.Atoi(m[1])
			d, _ := time.ParseDuration(m[2])
			s, _ := strconv.Atoi(m[3])
			c, _ := strconv.Atoi(m[4])
			p.held, p.skipped, p.cut = p.held+h, p.skipped+s, p.cut+c
			p.heldFor += d
		}
	}
	return p
}

// pacingLog returns the daemon's output pacing lines.
func pacingLog(t *testing.T, base string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "logs", "--all")
	if err != nil {
		t.Fatalf("tuios logs: %v: %s", err, out)
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "output pacing") {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

// TestKittyStuckClientDoesNotSlowTheOthers: two clients on one graphics pane,
// and one of them stops reading. The guest and the other client keep their
// rate. The stopped client skips the frames it missed, and when it reads again
// it gets whole frames: no image data printed as text, and no image deleted.
func TestKittyStuckClientDoesNotSlowTheOthers(t *testing.T) {
	base, fast, count := startPacedGuest(t, "b64")
	stuck := attachPacingClient(t, base, "pacing")
	t.Cleanup(stuck.resume)
	deadline := time.Now().Add(shellTimeout)
	for f, _ := stuck.host.counts(); f < 5; f, _ = stuck.host.counts() {
		if time.Now().After(deadline) {
			t.Fatalf("the second client never got a frame\n%s", stuck.term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}

	const window = 3 * time.Second
	guestBoth, gotBoth := rate(count, window, fast, stuck)
	t.Logf("both reading: guest wrote %d frames, the clients got %v in %s", guestBoth, gotBoth, window)

	_, deletesBefore := stuck.host.counts()
	if garbage := base64Rows(stuck.term.Screen().Text()); len(garbage) > 0 {
		t.Errorf("image data was printed into the second client's pane as text before any stop:\n%s", strings.Join(garbage, "\n"))
	}
	stuck.stop(t)
	guestStuck, gotStuck := rate(count, window, fast)
	stuck.resume()
	t.Logf("one stopped:  guest wrote %d frames, the reading client got %d in %s", guestStuck, gotStuck[0], window)

	_, gotAfter := rate(count, window, stuck)
	_, deletes := stuck.host.counts()
	garbage := base64Rows(stuck.term.Screen().Text())
	t.Logf("resumed:      the stopped client got %d frames in %s, %d deletes, %d rows of base64",
		gotAfter[0], window, deletes-deletesBefore, len(garbage))
	log := pacingLog(t, base)
	t.Logf("daemon: %s", strings.Join(log, "\n  "))
	totals := sumPacing(log)

	if gotBoth[0] < 6 {
		t.Fatalf("the reading client got only %d frames in %s before the stop; the stream is not running", gotBoth[0], window)
	}
	if gotStuck[0]*10 < gotBoth[0]*6 {
		t.Errorf("the reading client got %d frames while the other was stopped, %d before: the stopped client set the pace",
			gotStuck[0], gotBoth[0])
	}
	if guestStuck*10 < guestBoth*6 {
		t.Errorf("the guest wrote %d frames while a client was stopped, %d before: the stopped client held the guest",
			guestStuck, guestBoth)
	}
	if gotAfter[0] < 3 {
		t.Errorf("the stopped client got only %d frames after it read again", gotAfter[0])
	}
	if totals.cut > 0 {
		t.Errorf("the daemon cut %d client streams: the stopped client fell behind instead of skipping frames", totals.cut)
	}
	if totals.skipped == 0 {
		t.Errorf("the stopped client skipped no frames")
	}
	if deletes > deletesBefore {
		t.Errorf("the stopped client's image was deleted %d times: its stream was cut", deletes-deletesBefore)
	}
	if len(garbage) > 0 {
		t.Errorf("image data was printed into the stopped client's pane as text:\n%s", strings.Join(garbage, "\n"))
	}
}

// TestKittySlowClientHoldsTheGuest: the only client reads in bursts, stopped
// for 150 ms out of every 200. Every client is behind, so the daemon holds the
// guest in write() instead of reading frames nobody can take.
//
// The test reads the time the daemon held the pane, not only the guest's
// rate. The guest's rate with the client reading is bound by the CPU: on two
// cores it fell to 68 frames in 3 s, and the burst rate came to 60.3% of it
// with the pane held. The time held does not depend on how fast the machine
// is: the client is stopped for 2.25 s of the 3 s, and a pane that is not
// held is held for none of it.
func TestKittySlowClientHoldsTheGuest(t *testing.T) {
	base, client, count := startPacedGuest(t, "b64")

	const window = 3 * time.Second
	guestFree, gotFree := rate(count, window, client)
	t.Logf("reading:       guest wrote %d frames, the client got %d in %s", guestFree, gotFree[0], window)
	before := sumPacing(pacingLog(t, base))

	done := make(chan struct{})
	go dutyCycle(client, 150*time.Millisecond, 50*time.Millisecond, done)
	guestSlow, gotSlow := rate(count, window, client)
	close(done)
	t.Logf("in bursts:     guest wrote %d frames, the client got %d in %s", guestSlow, gotSlow[0], window)
	// The daemon writes its pacing line at most every 2 s: wait for the one
	// that covers the end of the bursts.
	time.Sleep(pacingReportWait)
	log := pacingLog(t, base)
	t.Logf("daemon: %s", strings.Join(log, "\n  "))
	after := sumPacing(log)
	heldFor := after.heldFor - before.heldFor
	t.Logf("held for %s of the %s in bursts", heldFor, window)

	if guestFree < 6 {
		t.Fatalf("the guest wrote only %d frames in %s with the client reading", guestFree, window)
	}
	if heldFor < window/3 {
		t.Errorf("the daemon held the guest for %s of %s while its only client read in bursts: the pane was not held",
			heldFor, window)
	}
	if guestSlow >= guestFree {
		t.Errorf("the guest wrote %d frames while its only client read in bursts, %d while it read: the hold cost it nothing",
			guestSlow, guestFree)
	}
	if garbage := base64Rows(client.term.Screen().Text()); len(garbage) > 0 {
		t.Errorf("image data was printed into the pane as text:\n%s", strings.Join(garbage, "\n"))
	}
}

// pacingReportWait is how long a test waits for the daemon to write the pacing
// line for what it just did: one report interval and a margin.
const pacingReportWait = 2500 * time.Millisecond

// TestTextFloodPaneIsNeverHeld: the same client in bursts, on a pane that
// floods text and draws no graphics. A text pane is read as before: the guest
// is never held, whatever its clients do.
func TestTextFloodPaneIsNeverHeld(t *testing.T) {
	base, client, count := startPacedGuest(t, "text")

	const window = 3 * time.Second
	guestFree, _ := rate(count, window)
	done := make(chan struct{})
	go dutyCycle(client, 900*time.Millisecond, 100*time.Millisecond, done)
	guestSlow, _ := rate(count, window)
	close(done)
	log := pacingLog(t, base)
	t.Logf("text guest wrote %d frames in %s with the client reading, %d with it in bursts", guestFree, window, guestSlow)
	t.Logf("daemon: %s", strings.Join(log, "\n  "))

	if guestFree < 100 {
		t.Fatalf("the text guest wrote only %d frames in %s", guestFree, window)
	}
	if held := sumPacing(log).held; held > 0 {
		t.Errorf("the daemon held a pane that draws no graphics %d times:\n  %s", held, strings.Join(log, "\n  "))
	}
}

// dutyCycle stops and resumes a client until done closes, and leaves it
// running.
func dutyCycle(c *pacingClient, stopped, running time.Duration, done <-chan struct{}) {
	defer c.resume()
	for {
		_ = syscall.Kill(c.term.Pid(), syscall.SIGSTOP)
		select {
		case <-done:
			return
		case <-time.After(stopped):
		}
		c.resume()
		select {
		case <-done:
			return
		case <-time.After(running):
		}
	}
}

// TestKittyAttachDuringAStreamPrintsNoImageData: a client that attaches while
// a pane streams frames starts its stream wherever the pane has got to, which
// is usually inside a frame. The daemon starts it after that frame. Starting
// inside one handed the client the rest of an image with no start, and the
// client printed the image data into the pane as text. A gapped stream resumes
// the same way. Each attach lands at a new point in the stream.
func TestKittyAttachDuringAStreamPrintsNoImageData(t *testing.T) {
	base, _, count := startPacedGuest(t, "b64")
	const attaches = 16
	for i := range attaches {
		c := attachPacingClient(t, base, "pacing")
		deadline := time.Now().Add(shellTimeout)
		for f, _ := c.host.counts(); f < 3; f, _ = c.host.counts() {
			if time.Now().After(deadline) {
				t.Fatalf("attach %d never got a frame; the guest has written %d\n%s\ndaemon:\n  %s",
					i+1, guestFrames(count), c.term.Snapshot(), strings.Join(pacingLog(t, base), "\n  "))
			}
			time.Sleep(50 * time.Millisecond)
		}
		if garbage := base64Rows(c.term.Screen().Text()); len(garbage) > 0 {
			t.Fatalf("attach %d of %d printed image data into the pane as text:\n%s",
				i+1, attaches, strings.Join(garbage, "\n"))
		}
		_ = c.term.Close()
	}
}

// base64Run matches a long run of base64 letters, which a pane that draws only
// an image never shows.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/]{48,}`)

// base64Rows returns the screen rows that carry image data as text, at most
// five of them.
func base64Rows(screen string) []string {
	var out []string
	for _, line := range strings.Split(screen, "\n") {
		if base64Run.MatchString(line) {
			out = append(out, fmt.Sprintf("  %.100s", line))
			if len(out) == 5 {
				break
			}
		}
	}
	return out
}
