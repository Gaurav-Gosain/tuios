package tuie2e

// End-to-end DOOM-fire throughput: a pane cats a full-screen truecolor
// repaint stream as fast as tuios will drain it, and the wall clock from
// start to the completion marker is the whole pipeline's consumption rate:
// PTY read, emulator parse, damage, render. Run against a pure-Go and a
// ghostty-tagged binary to compare backends end to end:
//
//	TUIOS_E2E=1 TUIOS_PERF=1 go test -run TestPerfDoomFireStream -v ./...

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	doomCols   = 158
	doomRows   = 40
	doomFrames = 600
)

func synthDoomFireE2E(cols, rows, frames int) []byte {
	var b strings.Builder
	rng := rand.New(rand.NewSource(42))
	pal := [][3]int{{7, 7, 7}, {31, 7, 7}, {103, 31, 7}, {175, 63, 7}, {223, 95, 7}, {255, 143, 7}, {255, 191, 7}, {255, 255, 255}}
	for f := 0; f < frames; f++ {
		b.WriteString("\x1b[H")
		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				t := pal[rng.Intn(len(pal))]
				bo := pal[rng.Intn(len(pal))]
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", t[0], t[1], t[2], bo[0], bo[1], bo[2])
			}
			if y < rows-1 {
				b.WriteString("\r\n")
			}
		}
	}
	return []byte(b.String())
}

func TestPerfDoomFireStream(t *testing.T) {
	perfGate(t)

	data := synthDoomFireE2E(doomCols, doomRows, doomFrames)
	path := filepath.Join(t.TempDir(), "doomfire.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	term, _ := start(t, startOpts{
		cols: doomCols + 4, rows: doomRows + 6,
		args: []string{"new", "doomfire"},
		env:  perfEnvVars(),
	})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	marker := "DOOMFIREDONE"
	cmd := fmt.Sprintf("cat %s; printf '\\n%s\\n'", path, marker)
	begin := time.Now()
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("start stream: %v", err)
	}
	if err := term.WaitForText(marker, 120*time.Second); err != nil {
		t.Fatalf("stream never finished: %v\n%s", err, term.Snapshot())
	}
	dur := time.Since(begin)

	mb := float64(len(data)) / (1024 * 1024)
	fps := float64(doomFrames) / dur.Seconds()
	t.Logf("doomfire %dx%d: %d frames (%.1f MiB) in %s = %.0f fps, %.1f MB/s end to end",
		doomCols, doomRows, doomFrames, mb, dur.Round(time.Millisecond), fps, mb/dur.Seconds())
}

// TestPerfDoomFireGame runs the real DOOM-fire binary in a pane and reads
// its own fps counter off the screen. Unlike the cat-flood variant, the game
// paces itself on the terminal's consumption, so the emulator's parse cost
// is on the frame path. Point TUIOS_PERF_DOOMFIRE_BIN at a DOOM-fire-zig
// binary to enable it.
func TestPerfDoomFireGame(t *testing.T) {
	perfGate(t)
	game := os.Getenv("TUIOS_PERF_DOOMFIRE_BIN")
	if game == "" {
		t.Skip("TUIOS_PERF_DOOMFIRE_BIN not set")
	}

	term, _ := start(t, startOpts{
		cols: doomCols + 4, rows: doomRows + 6,
		args: []string{"new", "doomgame"},
		env:  perfEnvVars(),
	})
	waitBoot(t, term)
	newWindow(t, term)
	// Fullscreen the pane: the game needs at least 120x22 and a floating
	// window is smaller than that.
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("fullscreen: %v", err)
	}
	enterTerminalMode(t, term)

	if err := term.SendKeys(game, tuitest.Enter); err != nil {
		t.Fatalf("start game: %v", err)
	}
	// The game opens on a capability screen and waits for return.
	if err := term.WaitForText("Press return to continue", shellTimeout); err != nil {
		t.Fatalf("game intro never appeared: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter game: %v", err)
	}
	// Let the flame run, then quit: the game prints its fps summary on
	// exit.
	time.Sleep(8 * time.Second)
	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("stop game: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	snap := term.Snapshot()

	re := regexp.MustCompile(`\[ ([0-9.]+) fps \]`)
	m := re.FindStringSubmatch(snap)
	if m == nil {
		t.Fatalf("no fps counter on screen:\n%s", snap)
	}
	t.Logf("doomfire game fps inside tuios: %s", m[1])
}
