package tuie2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A placement fault that shows up "sometimes" is not going to be caught by one
// assertion after one action. Three fixes have shipped for this area on the
// strength of exactly that and the reports kept coming, so this runs the
// scenario continuously instead and checks two things after every perturbation:
//
//   - Geometry. Every command that tells the host how to draw the image must
//     describe the whole bitmap in the whole of the pane's cells. Anything else
//     is a scale factor, which is the stretch.
//   - Liveness. New placements must keep arriving. A stream whose placement was
//     deleted and never restored goes quiet, which is the pane that "stops
//     working" after a switch away and back.
//
// The perturbations are the ones a live session applies to a pane that is doing
// nothing itself: its neighbour prints, focus moves, the workspace is hidden and
// shown, an overlay opens over it, and it is scrolled. None of them change the
// pane's rectangle, so none of them may change what the host is told about it.

// stressRounds is how many perturbation cycles to run. The default is a short
// run for CI; TUIOS_KITTY_STRESS_ROUNDS raises it for a soak.
func stressRounds(t *testing.T) int {
	if v := os.Getenv("TUIOS_KITTY_STRESS_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		t.Fatalf("TUIOS_KITTY_STRESS_ROUNDS=%q is not a positive number", v)
	}
	return 6
}

// phaseCmds returns the draw commands recorded in one phase.
func phaseCmds(stream []byte, phase string) []wireCmd {
	var out []wireCmd
	for _, c := range wireCmds(stream) {
		if c.phase != phase {
			continue
		}
		if c.action == "p" || c.action == "T" || (c.action == "t" && c.cols > 0) {
			out = append(out, c)
		}
	}
	return out
}

// settle marks a phase, lets it run, and returns what the host was told during
// it. Marking before rather than after is what attributes a command to the
// state it was emitted in.
func settlePhase(host *kittyHost, name string, d time.Duration) func([]byte) []wireCmd {
	host.mark(name)
	time.Sleep(d)
	return func(stream []byte) []wireCmd { return phaseCmds(stream, name) }
}

// checkRect says the pane's rectangle did not move. None of the perturbations
// resize the image pane, so every command in a phase must describe the same
// cells the guest was given. A rectangle that shrinks for a frame and comes
// back is the image jumping and re-cropping on screen.
func checkRect(t *testing.T, cmds []wireCmd, phase string, cols, rows int) {
	t.Helper()
	seen := map[string]int{}
	var order []string
	for _, c := range cmds {
		if c.cols == cols && c.rows == rows {
			continue
		}
		k := fmt.Sprintf("%dx%d cells, want %dx%d: %q", c.cols, c.rows, cols, rows, c.params)
		if seen[k] == 0 {
			order = append(order, k)
		}
		seen[k]++
	}
	if len(order) == 0 {
		return
	}
	var b strings.Builder
	for _, k := range order {
		fmt.Fprintf(&b, "  x%d  %s\n", seen[k], k)
	}
	t.Errorf("phase %q: the image pane was not resized, but the host was given a "+
		"different rectangle for it:\n%s", phase, b.String())
}

// checkLive says the stream is still reaching the host. A placement that was
// deleted and never restored leaves the pane frozen or blank, which is the
// third report: switch away, switch back, and the stream is dead.
func checkLive(t *testing.T, term *tuitest.Terminal, cmds []wireCmd, phase string) {
	t.Helper()
	if len(cmds) == 0 {
		t.Errorf("phase %q: the stream went quiet - no placement reached the host in "+
			"the whole phase, so whatever is in that pane is frozen or gone\n%s",
			phase, term.Snapshot())
	}
}

func TestKittyPlacementStressUnderPerturbation(t *testing.T) {
	host := newKittyHost()
	term, _ := start(t, startOpts{
		cols: 120, rows: 40,
		args: []string{"--shared-borders"},
		env:  []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:  host,
	})
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)
	waitWindowCount(t, term, 2, "two tiled panes")

	// Left pane: the graphics app.
	mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAGEPANE", "IMAGEPANE", shellTimeout)
	cols, rows, xpx, ypx := startFrameloop(t, term, 300)
	t.Logf("left pane: %dx%d cells, %dx%d px", cols, rows, xpx, ypx)
	leaveTerminalMode(t, term)

	// Right pane: the flood, running for the rest of the test.
	mouseClick(t, term, 95, 12, tuitest.MouseLeft, 0)
	time.Sleep(400 * time.Millisecond)
	enterTerminalMode(t, term)
	typeLine(t, term, "while :; do ls; done")
	leaveTerminalMode(t, term)
	time.Sleep(time.Second)

	const dwell = 1200 * time.Millisecond
	rounds := stressRounds(t)
	type check struct {
		name string
		get  func([]byte) []wireCmd
	}
	var checks []check
	record := func(name string, get func([]byte) []wireCmd) {
		checks = append(checks, check{name, get})
	}

	began := time.Now()
	for round := range rounds {
		tag := func(s string) string { return fmt.Sprintf("r%d-%s", round, s) }

		// Nothing at all but the neighbour printing. This is the report.
		record(tag("flood"), settlePhase(host, tag("flood"), dwell))

		// Focus moves to the neighbour and back. The image pane's rectangle is
		// the same either way, and its stream must not notice.
		mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
		record(tag("focus-image"), settlePhase(host, tag("focus-image"), dwell))
		mouseClick(t, term, 95, 12, tuitest.MouseLeft, 0)
		record(tag("focus-neighbour"), settlePhase(host, tag("focus-neighbour"), dwell))

		// Hidden with its whole workspace and brought back. A hidden pane's
		// image is deleted from the host; coming back must restore it.
		switchWorkspace(t, term, "2", 0)
		time.Sleep(600 * time.Millisecond)
		switchWorkspace(t, term, "1", 2)
		record(tag("workspace-return"), settlePhase(host, tag("workspace-return"), dwell))

		// An overlay covers the pane and then closes. Same story: the image is
		// hidden while it is up and must come back when it goes.
		if err := term.SendKeys(tuitest.Ctrl('b'), "?"); err != nil {
			t.Fatalf("open help: %v", err)
		}
		time.Sleep(600 * time.Millisecond)
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("close help: %v", err)
		}
		record(tag("overlay-return"), settlePhase(host, tag("overlay-return"), dwell))
	}
	t.Logf("%d rounds of perturbation in %s", rounds, time.Since(began).Round(time.Second))

	stream := host.bytes()
	if dump := os.Getenv("TUIOS_KITTY_CAPTURE"); dump != "" {
		if err := os.WriteFile(dump, stream, 0o644); err != nil {
			t.Fatalf("write capture: %v", err)
		}
	}
	cw, ch := xpx/cols, ypx/rows
	total := 0
	for _, c := range checks {
		got := c.get(stream)
		total += len(got)
		checkLive(t, term, got, c.name)
		checkRect(t, got, c.name, cols, rows)
	}
	reportScaleFaults(t, scaleFaults(stream, cw, ch), total)
}
