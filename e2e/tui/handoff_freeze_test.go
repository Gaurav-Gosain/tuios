package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A pane is frozen from the moment a handoff stops it until the exec, or
// until the handoff is put back: a resize, a cell size and a push of the
// report colours in that span reach neither the emulator, nor the kernel's
// window size, nor the program (docs/design/live-handoff.md, section 2.4).
// The new image builds the pane at the size it was written down at, and every
// client attaches again and announces its own.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// freezeProbeScript runs in the pane under test. It turns on in-band resize
// reports (mode 2048) and colour scheme reports (mode 2031), and prints what
// the program hears: every reply on its input through cat -v, and on SIGWINCH
// or SIGUSR1 the size the kernel gives it.
const freezeProbeScript = `stty -icanon -echo min 1; ` +
	`trap 'echo; echo "size $(stty size)"' WINCH USR1; ` +
	`printf '\033[?2048h\033[?2031h'; echo ready; ` +
	`cat -v </dev/tty & while :; do sleep 0.1; done`

var (
	inBandReport = regexp.MustCompile(`\^\[\[48;(\d+);(\d+);`)
	schemeReport = regexp.MustCompile(`\^\[\[\?997;(\d)n`)
	kernelSize   = regexp.MustCompile(`^size (\d+) (\d+)$`)
)

// paneHeard is what the probe printed: each in-band size report as
// "rows x cols", each scheme report, and each kernel size it read.
type paneHeard struct {
	Reports []string `json:"reports"`
	Schemes []string `json:"schemes"`
	Kernel  []string `json:"kernel"`
}

func readPaneHeard(t *testing.T, base, sess, window string) paneHeard {
	t.Helper()
	lines, err := daemonScrollback(base, sess, window, 2000)
	if err != nil {
		t.Fatalf("capture the probe pane: %v", err)
	}
	var h paneHeard
	for _, l := range lines {
		for _, m := range inBandReport.FindAllStringSubmatch(l, -1) {
			h.Reports = append(h.Reports, m[1]+"x"+m[2])
		}
		for _, m := range schemeReport.FindAllStringSubmatch(l, -1) {
			h.Schemes = append(h.Schemes, m[1])
		}
		if m := kernelSize.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			h.Kernel = append(h.Kernel, m[1]+"x"+m[2])
		}
	}
	return h
}

// askKernelSize has the probe print the size the kernel gives it, and returns
// it as "rows x cols".
func askKernelSize(t *testing.T, base, sess, window string, pid int) string {
	t.Helper()
	before := len(readPaneHeard(t, base, sess, window).Kernel)
	if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
		t.Fatalf("signal the probe: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h := readPaneHeard(t, base, sess, window); len(h.Kernel) > before {
			return h.Kernel[len(h.Kernel)-1]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the probe never printed its size")
	return ""
}

// streamedSize is the size the daemon gives the pane, as "rows x cols".
func streamedSize(t *testing.T, base, sess, window string) string {
	t.Helper()
	reply, _ := paneSnapshot(t, base, sess, window, nil)
	return fmt.Sprintf("%dx%d", reply.Rows, reply.Cols)
}

// waitPaneHeard waits until what the probe heard satisfies ok.
func waitPaneHeard(t *testing.T, base, sess, window, what string, ok func(paneHeard) bool) paneHeard {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var h paneHeard
	for time.Now().Before(deadline) {
		if h = readPaneHeard(t, base, sess, window); ok(h) {
			return h
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the probe never heard %s; it heard %+v", what, h)
	return h
}

// dialEarly opens a connection to the daemon now, which a handoff's paused
// listener does not hold back: the old image serves it until it closes its
// clients at the exec.
func dialEarly(t *testing.T, base string) *session.VerbClient {
	t.Helper()
	c, err := session.DialVerbClientAt(frameSocket(base), "e2e")
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// answeredBy is the instance of the image that serves c.
func answeredBy(t *testing.T, c *session.VerbClient) string {
	t.Helper()
	raw, err := c.Call("hello", map[string]any{})
	if err != nil {
		t.Fatalf("hello: %v", err)
	}
	var h struct {
		Instance string `json:"instance"`
	}
	_ = json.Unmarshal(raw, &h)
	return h.Instance
}

// TestSelfExecFreezesPaneDuringHandoff holds a handoff for 2.5 s between
// writing the manifest and the exec, and in that span resizes the probe pane
// and pushes light report colours for its session, on connections opened
// before the handoff, as an attached client and a pane-view would. After the
// exec, before anything announces again:
//
//   - the pane is the size it was written down at, and the kernel gives the
//     program the same size: a resize that reached the kernel in the span
//     would leave the program at a size the new image does not know;
//   - the program heard no in-band size report, no SIGWINCH and no scheme
//     report from the span, each of which the old image would have sent for
//     a pane the new image rebuilds without them.
//
// Then a resize and a push to the new image reach the program, as a client
// that attaches again makes them.
//
// How it could pass wrongly: the calls in the span could land after the
// exec, on the new image, which would apply them. Each call is checked to
// have been answered by the old image's instance. The handoff could have
// failed: the image must change and the daemon keep its pid.
func TestSelfExecFreezesPaneDuringHandoff(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-freeze"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	win := newCommandWindow(t, base, sess, "probe", false, "sh", "-c", freezeProbeScript)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^ready`), 10*time.Second)
	pid := panePIDs(t, base, sess)[win]
	sizeBefore := streamedSize(t, base, sess, win)
	if k := askKernelSize(t, base, sess, win, pid); k != sizeBefore {
		t.Fatalf("before the handoff the pane is %s and the kernel says %s", sizeBefore, k)
	}
	time.Sleep(300 * time.Millisecond)
	heardBefore := readPaneHeard(t, base, sess, win)

	resizer, viewer := dialEarly(t, base), dialEarly(t, base)
	img := helloImage(t, base)
	res := mustCall(t, base, "test-handoff", map[string]any{"hold_ms": 2500})
	time.Sleep(300 * time.Millisecond)
	if _, err := resizer.Call("resize", map[string]any{"session": sess, "window": win, "width": 117, "height": 33}); err != nil {
		t.Fatalf("resize in the hold: %v", err)
	}
	stream, _, err := viewer.OpenPaneStream(map[string]any{"session": sess, "window": win, "viewer": map[string]any{"bg": "#ffffff", "fg": "#101010"}})
	if err != nil {
		t.Fatalf("push light colours in the hold: %v", err)
	}
	inHold := answeredBy(t, resizer) == img.Instance
	after := awaitNewImage(t, base, img, 20*time.Second)
	_ = stream.Close()
	time.Sleep(500 * time.Millisecond)

	sizeAfter := streamedSize(t, base, sess, win)
	kernelAfter := askKernelSize(t, base, sess, win, pid)
	heardAfter := readPaneHeard(t, base, sess, win)

	_ = mustCall(t, base, "resize", map[string]any{"session": sess, "window": win, "width": 91, "height": 27})
	again := waitPaneHeard(t, base, sess, win, "a size report of 27x91 after the handoff", func(h paneHeard) bool {
		return len(h.Reports) > 0 && h.Reports[len(h.Reports)-1] == "27x91"
	})
	view, _, err := dialEarly(t, base).OpenPaneStream(map[string]any{"session": sess, "window": win, "viewer": map[string]any{"bg": "#ffffff", "fg": "#101010"}})
	if err != nil {
		t.Fatalf("push light colours after the handoff: %v", err)
	}
	defer view.Close()
	again = waitPaneHeard(t, base, sess, win, "a light scheme report after the handoff", func(h paneHeard) bool {
		return len(h.Schemes) > len(heardBefore.Schemes) && h.Schemes[len(h.Schemes)-1] == "2"
	})

	writeHandoffArtifact(t, map[string]any{
		"pid_before": img.PID, "pid_after": after.PID, "manifest_bytes": res["manifest_bytes"],
		"calls_answered_by_old_image": inHold,
		"size_before":                 sizeBefore, "size_after": sizeAfter, "kernel_after": kernelAfter,
		"heard_before": heardBefore, "heard_after": heardAfter, "heard_after_announce": again,
	})
	if after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	if !inHold {
		t.Fatalf("the calls in the hold were answered by the new image, so the test exercised nothing")
	}
	if sizeAfter != sizeBefore {
		t.Errorf("ASSERTION: after the handoff the pane is %s, want %s, the size it was written down at", sizeAfter, sizeBefore)
	}
	if kernelAfter != sizeAfter {
		t.Errorf("ASSERTION: after the handoff the pane is %s and the kernel gives its program %s", sizeAfter, kernelAfter)
	}
	if got, want := len(heardAfter.Reports), len(heardBefore.Reports); got != want {
		t.Errorf("ASSERTION: the program heard %d in-band size reports by the handoff's end, %d before it: %v", got, want, heardAfter.Reports)
	}
	if got, want := len(heardAfter.Schemes), len(heardBefore.Schemes); got != want {
		t.Errorf("ASSERTION: the program heard %d scheme reports by the handoff's end, %d before it: %v", got, want, heardAfter.Schemes)
	}
	if got, want := len(heardAfter.Kernel), len(heardBefore.Kernel)+1; got != want {
		t.Errorf("ASSERTION: the program read its size %d times by the handoff's end, want %d (one SIGUSR1 after it): %v", got, want, heardAfter.Kernel)
	}
}

// replaceWithNonTuios puts a script that is not a tuios at path, by a rename
// over it as an installer replaces a binary, so path names another file.
func replaceWithNonTuios(t *testing.T, path string) {
	t.Helper()
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// TestHandoffPutBackAppliesWhatWasHeld holds a handoff to a copy of the
// binary, resizes the probe pane and pushes light report colours in the hold,
// and then replaces the copy with a script that is not a tuios. The exec finds
// a file the preflight did not run, runs the preflight again, which fails, and
// puts the daemon back with its clients still attached. Those clients will
// not ask again, so what they asked during the freeze must be applied: the
// pane at the size asked, the kernel giving the program that size, and the
// program told of the size (mode 2048) and of the light scheme (mode 2031).
//
// How it could pass wrongly: the handoff could have gone through, to an image
// that applied the calls itself. The daemon must answer from the same image.
func TestHandoffPutBackAppliesWhatWasHeld(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-putback"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	win := newCommandWindow(t, base, sess, "probe", false, "sh", "-c", freezeProbeScript)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^ready`), 10*time.Second)
	pid := panePIDs(t, base, sess)[win]
	sizeBefore := streamedSize(t, base, sess, win)
	second := copyTuios(t)

	resizer, viewer := dialEarly(t, base), dialEarly(t, base)
	img := helloImage(t, base)
	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second, "hold_ms": 2500})
	time.Sleep(300 * time.Millisecond)
	if _, err := resizer.Call("resize", map[string]any{"session": sess, "window": win, "width": 117, "height": 33}); err != nil {
		t.Fatalf("resize in the hold: %v", err)
	}
	stream, _, err := viewer.OpenPaneStream(map[string]any{"session": sess, "window": win, "viewer": map[string]any{"bg": "#ffffff", "fg": "#101010"}})
	if err != nil {
		t.Fatalf("push light colours in the hold: %v", err)
	}
	defer stream.Close()
	replaceWithNonTuios(t, second)
	time.Sleep(3500 * time.Millisecond)

	after := helloImage(t, base)
	sizeAfter := streamedSize(t, base, sess, win)
	kernelAfter := askKernelSize(t, base, sess, win, pid)
	heard := readPaneHeard(t, base, sess, win)

	writeHandoffArtifact(t, map[string]any{
		"instance_before": img.Instance, "instance_after": after.Instance,
		"size_before": sizeBefore, "size_after": sizeAfter, "kernel_after": kernelAfter, "heard": heard,
	})
	if after.Instance != img.Instance || after.PID != img.PID {
		t.Fatalf("the handoff went through (%+v then %+v), so nothing was put back", img, after)
	}
	if sizeAfter != "33x117" {
		t.Errorf("ASSERTION: after the put-back the pane is %s, want 33x117, the size asked during the freeze", sizeAfter)
	}
	if kernelAfter != "33x117" {
		t.Errorf("ASSERTION: after the put-back the kernel gives the program %s, want 33x117", kernelAfter)
	}
	if len(heard.Reports) == 0 || heard.Reports[len(heard.Reports)-1] != "33x117" {
		t.Errorf("ASSERTION: the program's last in-band size report is not 33x117: %v", heard.Reports)
	}
	if len(heard.Schemes) == 0 || heard.Schemes[len(heard.Schemes)-1] != "2" {
		t.Errorf("ASSERTION: the program was not told of the light scheme pushed during the freeze: %v", heard.Schemes)
	}
}
