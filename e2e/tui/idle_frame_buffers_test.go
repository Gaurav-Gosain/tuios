package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
)

// TestIdleClientFreesItsFrameBuffers: once the client has been idle for the
// cleanup interval, it frees the compositor's canvas. It is a cell buffer the
// size of the screen, 1.35 MB at 207x55, and an idle client held it for as
// long as nothing moved. The next key builds it again and draws the pane as
// before.
//
// The client records every allocation in its heap profile
// (GODEBUG=memprofilerate=1), so a held buffer always shows. The test reads
// the profile in its binary form: the text form of that profile is about
// 60 MB and took over 10 s on a CI runner.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md,
// TestIdleClientFreesItsFrameBuffers): with the releaseFrameBuffers call cut
// from View, the test fails at "the idle client still holds". With the old
// frame of a theme transition drawn from the frame text alone, it fails at
// "started from a blank frame".
func TestIdleClientFreesItsFrameBuffers(t *testing.T) {
	requireReal(t, "reads the heap of the real client")
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	base := t.TempDir()
	// A long circle, so its first frames still show nearly all of the old
	// frame.
	writeConfig(t, base, "[appearance]\ntheme = \"tokyonight\"\ntheme_transition = \"circle\"\ntheme_transition_duration = 2000\n")
	term := startIn(t, base, startOpts{
		cols: perfCols, rows: perfRows,
		// The theme transition is an animation.
		animations: true,
		args:       []string{"new", "frames", "--pprof", addr},
		env:        append(perfEnvVars(), "TUIOS_CLEANUP_SECONDS=2", "GODEBUG=memprofilerate=1"),
	})
	waitBoot(t, term)
	for range 3 {
		newWindow(t, term)
	}
	enterTerminalMode(t, term)
	typeLine(t, term, "echo BEFORE$((1+1))IDLE")
	if err := term.WaitForText("BEFORE2IDLE", uiTimeout); err != nil {
		t.Fatalf("the first line never showed: %v\n%s", err, term.Snapshot())
	}

	// The buffers are held while the client draws: a reading now that finds
	// none says the profile does not see them.
	heap, err := readHeapProfile(addr)
	if err != nil {
		t.Fatalf("read the client heap profile: %v", err)
	}
	busy := heapBytesFrom(heap, frameBufferAllocs...)
	if busy < 1<<20 {
		t.Fatalf("the busy client holds %d KiB of canvas, want at least 1 MiB: the profile does not see them", busy/1024)
	}
	t.Logf("the busy client holds %d KiB of canvas", busy/1024)

	var held uint64
	waitUntil(t, "the idle client never freed its canvas", 30*time.Second, func() (bool, string) {
		heap, err := readHeapProfile(addr)
		if err != nil {
			return false, err.Error()
		}
		held = heapBytesFrom(heap, frameBufferAllocs...)
		return held < 512<<10, fmt.Sprintf("the idle client still holds %d KiB of canvas", held/1024)
	})
	t.Logf("the idle client holds %d KiB of canvas", held/1024)

	// A theme transition starts from the frame the terminal shows. The
	// canvas that held it is gone, so the frame comes from the copy the
	// terminal writer keeps. Taken from nothing, the circle would start on a
	// blank screen, and the first line would be gone outside it.
	shown := term.Snapshot()
	if out, err := tuiosCLI(t, base, "set-config", "--no-save", "-s", "frames", "appearance.theme", "catppuccin_mocha"); err != nil {
		t.Fatalf("set the theme: %v: %s", err, out)
	}
	var first string
	waitUntil(t, "the theme change drew no frame", uiTimeout, func() (bool, string) {
		first = term.Snapshot()
		return first != shown, first
	})
	if !strings.Contains(first, "BEFORE2IDLE") {
		t.Fatalf("ASSERTION: the theme transition after the release started from a blank frame:\n%s", first)
	}

	typeLine(t, term, "echo AFTER$((1+1))IDLE")
	if err := term.WaitForText("AFTER2IDLE", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the first key after the release drew nothing: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Snapshot(), "BEFORE2IDLE") {
		t.Fatalf("ASSERTION: the frame after the release lost the text drawn before it:\n%s", term.Snapshot())
	}
}

// readHeapProfile reads the heap profile of the process that serves pprof at
// addr, after a collection (gc=1), in its binary form. The text form
// (debug=1) writes every frame of every record out again: with
// memprofilerate=1 it is about 150 times the size and takes about 15 times
// as long, about 5 s on a fast machine.
func readHeapProfile(addr string) (*profile.Profile, error) {
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?gc=1", addr))
	if err != nil {
		return nil, err
	}
	return profile.Parse(strings.NewReader(body))
}

// heapBytesFrom sums the in-use bytes of the samples in a heap profile
// allocated directly by one of the given call pairs: the frame that allocates
// and the function that called it, the runtime's own frames skipped. A buffer
// some other code allocates on the way through (the frame check's scratch
// below GetCanvas) is not counted.
func heapBytesFrom(p *profile.Profile, pairs ...[2]string) uint64 {
	inuse := -1
	for i, st := range p.SampleType {
		if st.Type == "inuse_space" {
			inuse = i
		}
	}
	if inuse < 0 {
		return 0
	}
	var total uint64
	for _, s := range p.Sample {
		frames := callers(s, 2)
		if len(frames) < 2 {
			continue
		}
		for _, pair := range pairs {
			if strings.Contains(frames[0], pair[0]) && strings.Contains(frames[1], pair[1]) {
				total += uint64(s.Value[inuse])
				break
			}
		}
	}
	return total
}

// callers returns the first n function names of a sample's stack, from the
// leaf out, with inlined calls as their own frames and the runtime's frames
// skipped.
func callers(s *profile.Sample, n int) []string {
	var names []string
	for _, loc := range s.Location {
		for _, line := range loc.Line {
			if line.Function == nil || strings.HasPrefix(line.Function.Name, "runtime.") {
				continue
			}
			names = append(names, line.Function.Name)
			if len(names) == n {
				return names
			}
		}
	}
	return names
}

// frameBufferAllocs are where the canvas is allocated.
var frameBufferAllocs = [][2]string{
	{"ultraviolet.NewBuffer", "(*OS).GetCanvas"},
}
