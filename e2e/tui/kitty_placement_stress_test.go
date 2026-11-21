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

func stressRounds(t *testing.T) int {
	if v := os.Getenv("TUIOS_KITTY_STRESS_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
		t.Fatalf("TUIOS_KITTY_STRESS_ROUNDS=%q is not a positive number", v)
	}
	return 6
}

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

func settlePhase(host *kittyHost, name string, d time.Duration) func([]byte) []wireCmd {
	host.mark(name)
	time.Sleep(d)
	return func(stream []byte) []wireCmd { return phaseCmds(stream, name) }
}

func checkAgrees(t *testing.T, cmds []wireCmd, phase string, told map[[2]int]bool) {
	t.Helper()
	seen := map[string]int{}
	var order []string
	for _, c := range cmds {
		if told[[2]int{c.cols, c.rows}] {
			continue
		}
		k := fmt.Sprintf("%dx%d cells: %q", c.cols, c.rows, c.params)
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
	t.Errorf("phase %q: the host was told to draw the image in a rectangle the "+
		"guest was never given, so no frame it sends can fill it:\n%s", phase, b.String())
}

func checkLive(t *testing.T, term *tuitest.Terminal, cmds []wireCmd, phase string, want int) {
	t.Helper()
	if len(cmds) >= want {
		return
	}
	if len(cmds) == 0 {
		t.Errorf("phase %q: the stream went quiet - no placement reached the host in "+
			"the whole phase, so whatever is in that pane is frozen or gone\n%s",
			phase, term.Snapshot())
		return
	}
	t.Errorf("phase %q: only %d placements reached the host where the guest painted "+
		"about %d; the stream is stalling rather than running", phase, len(cmds), want)
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

	clickToType(t, term, 20, 12)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)
	geom, cols, rows, xpx, ypx := startFrameloop(t, term, 0)
	t.Logf("left pane: %dx%d cells, %dx%d px", cols, rows, xpx, ypx)
	leaveTerminalMode(t, term)

	clickToType(t, term, 95, 12)
	typeLine(t, term, "while :; do ls; done")
	leaveTerminalMode(t, term)
	time.Sleep(time.Second)

	const dwell = 1200 * time.Millisecond
	minPlacements := int(2 * 20 * dwell.Seconds() / 2)
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

		record(tag("flood"), settlePhase(host, tag("flood"), dwell))

		mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
		record(tag("focus-image"), settlePhase(host, tag("focus-image"), dwell))
		mouseClick(t, term, 95, 12, tuitest.MouseLeft, 0)
		record(tag("focus-neighbour"), settlePhase(host, tag("focus-neighbour"), dwell))

		switchWorkspace(t, term, "2", 0)
		time.Sleep(600 * time.Millisecond)
		switchWorkspace(t, term, "1", 2)
		record(tag("workspace-return"), settlePhase(host, tag("workspace-return"), dwell))

		mouseClick(t, term, 20, 12, tuitest.MouseLeft, 0)
		time.Sleep(300 * time.Millisecond)
		if err := term.SendKeys("m"); err != nil {
			t.Fatalf("minimize: %v", err)
		}
		time.Sleep(600 * time.Millisecond)
		if err := term.SendKeys("M"); err != nil {
			t.Fatalf("restore: %v", err)
		}
		record(tag("minimize-return"), settlePhase(host, tag("minimize-return"), dwell))

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
	told := map[[2]int]bool{}
	for _, sz := range announcedSizes(geom) {
		told[[2]int{sz[0], sz[1]}] = true
	}
	t.Logf("sizes the guest was given: %v", told)

	cw, ch := xpx/cols, ypx/rows
	total := 0
	for _, c := range checks {
		got := c.get(stream)
		total += len(got)
		checkLive(t, term, got, c.name, minPlacements)
		checkAgrees(t, got, c.name, told)
	}
	reportScaleFaults(t, scaleFaults(stream, cw, ch), total)
}
