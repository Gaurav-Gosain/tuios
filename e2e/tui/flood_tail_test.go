package tuie2e

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

var floodTailEnv = strings.Fields(os.Getenv("TUIOS_TAIL_ENV"))

var firePalette = []int{0, 52, 88, 124, 160, 196, 202, 208, 214, 220, 226, 231}

var fireFileBytes = func() int {
	if mib, err := strconv.Atoi(os.Getenv("TUIOS_TAIL_MIB")); err == nil && mib > 0 {
		return mib << 20
	}
	return 48 << 20
}()

func writeFireFile(t *testing.T, path string, cols, rows int) int {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fire file: %v", err)
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriterSize(f, 1<<20)
	rng := rand.New(rand.NewSource(7))
	frames, written := 0, 0
	for written < fireFileBytes {
		frames++
		var b strings.Builder
		for y := 1; y <= rows-1; y++ {
			fmt.Fprintf(&b, "\x1b[%d;1H", y)
			last := -1
			for range cols {
				c := firePalette[rng.Intn(len(firePalette))]
				if c != last {
					fmt.Fprintf(&b, "\x1b[48;5;%dm", c)
					last = c
				}
				b.WriteByte(' ')
			}
			b.WriteString("\x1b[m")
		}
		fmt.Fprintf(&b, "\x1b[%d;1HFIREFRAME %07d", rows, frames)
		n, err := w.WriteString(b.String())
		if err != nil {
			t.Fatalf("write fire file: %v", err)
		}
		written += n
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush fire file: %v", err)
	}
	return frames
}

var fireFrameRe = regexp.MustCompile(`FIREFRAME (\d{7})`)

func shownFrame(s tuitest.Screen) int {
	best := 0
	for _, m := range fireFrameRe.FindAllStringSubmatch(s.Text(), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > best {
			best = n
		}
	}
	return best
}

func waitForStamp(t *testing.T, path string, timeout time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			return time.Now()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("flood never recorded its exit at %s", path)
	return time.Time{}
}

type floodTailResult struct {
	tail             time.Duration
	atDeath, settled int
	total            int
}

func measureFloodTail(t *testing.T, extraEnv ...string) floodTailResult {
	t.Helper()
	dir := t.TempDir()
	fire := filepath.Join(dir, "fire.bin")
	stamp := filepath.Join(dir, "exit_at")

	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-flood-tail", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	term := attachIn(t, base, "e2e-flood-tail", startOpts{cols: 160, rows: 45, env: extraEnv})

	if err := term.SendKeys(tuitest.Ctrl('b'), "z"); err != nil {
		t.Fatalf("zoom the pane: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	enterTerminalMode(t, term)

	rows, cols := reportedPaneSize(t, term, "A")
	if cols < 20 || rows < 6 {
		t.Fatalf("pane is %dx%d, too small to flood", cols, rows)
	}
	frames := writeFireFile(t, fire, cols, rows)
	defer func() { _ = os.Remove(fire) }()
	t.Logf("pane %dx%d, %d fire frames in %d MiB", cols, rows, frames, fireFileBytes>>20)

	cmd := fmt.Sprintf("cat %s; date +%%s.%%N > %s", fire, stamp)
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("start flood: %v", err)
	}

	died := waitForStamp(t, stamp, 180*time.Second)
	atDeath := shownFrame(term.Screen())

	lastChange, lastFrame := died, atDeath
	for {
		time.Sleep(20 * time.Millisecond)
		if n := shownFrame(term.Screen()); n != lastFrame {
			lastFrame, lastChange = n, time.Now()
			continue
		}
		if time.Since(lastChange) > 1500*time.Millisecond {
			break
		}
		if time.Since(died) > 180*time.Second {
			t.Fatalf("pane never stopped painting; at death frame %d, now %d", atDeath, lastFrame)
		}
	}
	return floodTailResult{
		tail:    lastChange.Sub(died),
		atDeath: atDeath,
		settled: lastFrame,
		total:   frames,
	}
}

// TestFloodPaintTail reports how long a pane keeps repainting after the program
// filling it has gone, and how many frames of what it paints were produced
// before that program died.
//
// The bound it asserts is loose, because the number is a property of the
// machine as much as of tuios. What it pins is what a user can see: the pane
// stops within a couple of seconds of the writer, and it settles on the end of
// the stream rather than in the middle of it.
func TestFloodPaintTail(t *testing.T) {
	got := measureFloodTail(t, floodTailEnv...)
	t.Logf("paint tail %v: at the writer's death the pane showed frame %d of %d, it settled on %d, %d frames later",
		got.tail.Round(time.Millisecond), got.atDeath, got.total, got.settled, got.settled-got.atDeath)

	if got.settled != got.total {
		t.Errorf("pane settled on frame %d, want the last frame %d: the screen is not showing the end of the stream",
			got.settled, got.total)
	}
	if got.tail > 2*time.Second {
		t.Errorf("pane kept painting %v after the writer died, want under 2s", got.tail.Round(time.Millisecond))
	}
}
