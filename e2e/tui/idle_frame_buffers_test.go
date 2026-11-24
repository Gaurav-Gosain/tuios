package tuie2e

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestIdleClientFreesItsFrameBuffers: once the client has been idle for the
// cleanup interval, it frees the compositor's canvas and the layers it parsed
// to cells. Each is a cell buffer the size of the screen, 1.35 MB at 207x55,
// and an idle client held them for as long as nothing moved. The next key
// builds them again and draws the pane as before.
//
// The client records every allocation in its heap profile
// (GODEBUG=memprofilerate=1), so a held buffer always shows.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md,
// TestIdleClientFreesItsFrameBuffers): with the releaseFrameBuffers call cut
// from View, the test fails at "the idle client still holds".
func TestIdleClientFreesItsFrameBuffers(t *testing.T) {
	requireReal(t, "reads the heap of the real client")
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	term, _ := start(t, startOpts{
		cols: perfCols, rows: perfRows,
		args: []string{"new", "frames", "--pprof", addr},
		env:  append(perfEnvVars(), "TUIOS_CLEANUP_SECONDS=2", "GODEBUG=memprofilerate=1"),
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
	body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?gc=1&debug=1", addr))
	if err != nil {
		t.Fatalf("read the client heap profile: %v", err)
	}
	if busy := heapBytesFrom(body, frameBufferAllocs...); busy < 1<<20 {
		t.Fatalf("the busy client holds %d KiB of canvas and parsed layers, want at least 1 MiB: the profile does not see them", busy/1024)
	}

	var held uint64
	waitUntil(t, "the idle client never freed its canvas and its parsed layers", 30*time.Second, func() (bool, string) {
		body, err := httpGet(fmt.Sprintf("http://%s/debug/pprof/heap?gc=1&debug=1", addr))
		if err != nil {
			return false, err.Error()
		}
		held = heapBytesFrom(body, frameBufferAllocs...)
		return held < 512<<10, fmt.Sprintf("the idle client still holds %d KiB of canvas and parsed layers", held/1024)
	})
	t.Logf("the idle client holds %d KiB of canvas and parsed layers", held/1024)

	typeLine(t, term, "echo AFTER$((1+1))IDLE")
	if err := term.WaitForText("AFTER2IDLE", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the first key after the release drew nothing: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Snapshot(), "BEFORE2IDLE") {
		t.Fatalf("ASSERTION: the frame after the release lost the text drawn before it:\n%s", term.Snapshot())
	}
}

// heapBytesFrom sums the in-use bytes of the records in a debug=1 heap
// profile allocated directly by one of the given call pairs: the frame that
// allocates and the function that called it, the runtime's own frames
// skipped. A buffer some other code allocates on the way through (the frame
// check's scratch below GetCanvas) is not counted.
func heapBytesFrom(profile string, pairs ...[2]string) uint64 {
	var total, bytes uint64
	var frames []string
	flush := func() {
		if len(frames) >= 2 {
			for _, p := range pairs {
				if strings.Contains(frames[0], p[0]) && strings.Contains(frames[1], p[1]) {
					total += bytes
					break
				}
			}
		}
		frames = frames[:0]
	}
	for line := range strings.SplitSeq(profile, "\n") {
		if m := heapRecordLine.FindStringSubmatch(line); m != nil {
			flush()
			bytes, _ = strconv.ParseUint(m[1], 10, 64)
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || f[0] != "#" || strings.HasPrefix(f[2], "runtime.") {
			continue
		}
		frames = append(frames, f[2])
	}
	flush()
	return total
}

// frameBufferAllocs are where the canvas and the parsed layers are allocated.
var frameBufferAllocs = [][2]string{
	{"ultraviolet.NewBuffer", "(*OS).GetCanvas"},
	{"ultraviolet.(*Buffer).Resize", "(*cellLayer).update"},
}

var heapRecordLine = regexp.MustCompile(`^\d+: (\d+) \[\d+: \d+\] @`)
