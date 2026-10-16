package tuie2e

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The live upgrade to another binary: the preflight, the fallback to the
// binary the daemon ran, the queued callers and the refusals. See
// docs/design/live-handoff.md, section 5, PR 4. The tests build on the
// helpers of live_handoff_test.go.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// copyTuios copies the binary under test to a path of its own, so an upgrade
// execs a different file than the one the daemon runs.
func copyTuios(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "second", "tuios")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(tuiosBin)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(dst); err == nil {
		dst = resolved
	}
	return dst
}

// executableOf is the file process pid runs.
func executableOf(t *testing.T, pid int) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
		if err != nil {
			t.Fatalf("read the executable of %d: %v", pid, err)
		}
		return exe
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps %d: %v", pid, err)
	}
	return strings.TrimSpace(string(out))
}

// handoffStats is what the handoff that made the daemon's image measured.
func handoffStats(t *testing.T, base string) map[string]any {
	t.Helper()
	res := mustCall(t, base, "test-handoff-stats", map[string]any{})
	st, _ := res["stats"].(map[string]any)
	return st
}

// saveUpgradeArtifact writes the artifact, and a copy where
// TUIOS_E2E_HANDOFF_ARTIFACT names one.
func saveUpgradeArtifact(t *testing.T, v any) {
	t.Helper()
	path := writeHandoffArtifact(t, v)
	if dst := os.Getenv("TUIOS_E2E_HANDOFF_ARTIFACT"); dst != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			err = os.WriteFile(dst, data, 0o644)
		}
		if err != nil {
			t.Errorf("copy the artifact to %s: %v", dst, err)
		}
	}
}

// TestDaemonUpgradeKeepsPaneProcesses upgrades the daemon to a second copy of
// the binary with three kinds of pane running (docs/design/live-handoff.md,
// section 1.4):
//
//   - a contiguous counter, whose output must have no gap and no repeat
//     across the upgrade;
//   - a fake agent that answers its input and reports its state through
//     tuios, which must keep doing both, and then exits 7, which the daemon
//     must report as 7;
//   - a program on the alternate screen with bracketed paste and SGR mouse
//     on, which must still be there, and must still get a paste bracketed.
//
// The daemon keeps its pid, runs the second file after, and every pane keeps
// its process. The artifact records the pids, the counter's range, the
// pause, the manifest's size, the heap the new image peaked at while it
// rebuilt the panes, and the most bytes one pane's kernel buffer held.
func TestDaemonUpgradeKeepsPaneProcesses(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-upgrade"
	a := newCommandWindow(t, base, sess, "a", false, "sh", "-c",
		`echo pid-$$; i=0; while :; do i=$((i+1)); echo tick-$i; sleep 0.01; done`)
	b := newCommandWindow(t, base, sess, "b", true, "sh", "-c",
		`echo pid-$$; while read l; do echo "got-$l"; [ "$l" = quit ] && exit 7; "$1" set-agent-state working >/dev/null && echo "state-ok-$l"; done`, "b", tuiosBin)
	c := newCommandWindow(t, base, sess, "c", false, "sh", "-c", strings.Join([]string{
		`echo pid-$$`,
		`printf '\033[?2004h\033[?1000h\033[?1006h\033[?1049h\033[2J\033[HALT-SCREEN-C\r\n'`,
		`stty -icanon -echo 2>/dev/null`,
		`exec cat -v`,
	}, "; "))
	// A fourth pane writes as fast as it can, so the kernel holds a full
	// buffer of its output when the panes stop: the artifact's pending_max.
	newCommandWindow(t, base, sess, "flood", false, "sh", "-c", `exec yes 0123456789abcdef`)
	for _, w := range []string{a, b} {
		waitHandoffPane(t, base, sess, w, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
	}
	waitHandoffPane(t, base, sess, c, regexp.MustCompile(`ALT-SCREEN-C`), 10*time.Second)
	// The counter runs a while first, so its output on either side of the
	// upgrade is long enough to read a gap in.
	waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^tick-150$`), 20*time.Second)
	before := panePIDs(t, base, sess)
	ticksBefore := counterLines(mustScrollback(t, base, sess, a), "tick-")

	img := helloImage(t, base)
	exeBefore := executableOf(t, img.PID)
	started := time.Now()
	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second})
	after := awaitNewImage(t, base, img, 30*time.Second)
	endToEnd := time.Since(started)
	exeAfter := executableOf(t, after.PID)
	afterPIDs := panePIDs(t, base, sess)
	stats := handoffStats(t, base)

	time.Sleep(300 * time.Millisecond)
	ticks := counterLines(mustScrollback(t, base, sess, a), "tick-")

	_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": b, "text": "x\n"})
	waitHandoffPane(t, base, sess, b, regexp.MustCompile(`^state-ok-x`), 10*time.Second)
	agent := readPSState(t, base, sess, b).State

	_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": c, "text": "PASTED", "paste": true})
	_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": c, "text": "\r"})
	pasted := waitHandoffPane(t, base, sess, c, regexp.MustCompile(`(\^\[\[200~)?PASTED(\^\[\[201~)?`), 10*time.Second)[0]
	altLines, _ := daemonPane(base, sess, c)

	_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": b, "text": "quit\n"})
	code := windowExitCode(t, base, sess, b, 10*time.Second)

	tickRange := []int{}
	if len(ticks) > 0 {
		tickRange = []int{ticks[0], ticks[len(ticks)-1]}
	}
	saveUpgradeArtifact(t, map[string]any{
		"os": runtime.GOOS, "daemon_pid_before": img.PID, "daemon_pid_after": after.PID,
		"executable_before": exeBefore, "executable_after": exeAfter,
		"pane_pids_before": before, "pane_pids_after": afterPIDs,
		"ticks_before_last": lastOf(ticksBefore), "ticks_checked": len(ticks), "tick_range": tickRange,
		"tick_gap_at": firstGap(ticks), "end_to_end_ms": endToEnd.Milliseconds(),
		"agent_state_after": agent, "pasted": pasted, "exit_after": code, "stats": stats,
	})

	if after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	if exeAfter != second {
		t.Errorf("ASSERTION: the daemon runs %s after the upgrade, want %s", exeAfter, second)
	}
	for id, pid := range before {
		if afterPIDs[id] != pid {
			t.Errorf("ASSERTION: window %s had pid %d, and %d after the upgrade", id, pid, afterPIDs[id])
		}
	}
	if len(ticksBefore) == 0 || len(ticks) == 0 || ticks[0] > lastOf(ticksBefore) || ticks[len(ticks)-1] <= lastOf(ticksBefore) {
		t.Errorf("ASSERTION: the counter's output does not span the upgrade (last before %d, after %v)", lastOf(ticksBefore), tickRange)
	} else if i := firstGap(ticks); i >= 0 {
		t.Errorf("ASSERTION: the counter jumps from %d to %d", ticks[i-1], ticks[i])
	}
	if agent != "working" {
		t.Errorf("ASSERTION: the agent pane reads %q after reporting working", agent)
	}
	if pasted != `^[[200~PASTED^[[201~` || !screenLinesHave(altLines, "ALT-SCREEN-C") {
		t.Errorf("ASSERTION: the alternate-screen pane lost its screen or its paste mode: paste %q, screen %q", pasted, altLines)
	}
	if code != 7 {
		t.Errorf("ASSERTION: the agent pane exited 7 after the upgrade and reads %d", code)
	}
}

func lastOf(nums []int) int {
	if len(nums) == 0 {
		return -1
	}
	return nums[len(nums)-1]
}

func screenLinesHave(lines []string, s string) bool {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// TestDaemonUpgradeRefusesAndChangesNothing asks for an upgrade to binaries
// the preflight must refuse: one that is not tuios at all, and, when
// TUIOS_E2E_BASELINE_BIN names one, a tuios from before live upgrades, which
// cannot answer the preflight. Each must be refused, and nothing may change:
// the same image, the same pane processes, and a client that was streaming a
// pane still streaming it.
func TestDaemonUpgradeRefusesAndChangesNothing(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-refuse"
	win := newCommandWindow(t, base, sess, "counter", false, "sh", "-c",
		`i=0; while :; do i=$((i+1)); echo r-$i; sleep 0.05; done`)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^r-\d+`), 10*time.Second)

	client, err := session.DialVerbClientAt(frameSocket(base), "e2e")
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	stream, _, err := client.OpenPaneStream(map[string]any{"session": sess, "window": win})
	if err != nil {
		t.Fatalf("stream-pane: %v", err)
	}
	defer stream.Close()
	frames := make(chan byte, 1024)
	go func() {
		for {
			kind, _, err := stream.ReadFrame()
			if err != nil {
				close(frames)
				return
			}
			frames <- kind
		}
	}()

	notTuios, err := exec.LookPath("false")
	if err != nil {
		t.Fatalf("find false: %v", err)
	}
	binaries := map[string]string{"not tuios": notTuios}
	if baseline := os.Getenv("TUIOS_E2E_BASELINE_BIN"); baseline != "" {
		binaries["an older tuios"] = baseline
	}
	results := map[string]any{}
	for name, bin := range binaries {
		img := helloImage(t, base)
		pids := panePIDs(t, base, sess)
		_, err := callVerb(t, base, "test-handoff", map[string]any{"binary": bin})
		if err == nil || !strings.Contains(err.Error(), "does not run as a tuios") {
			t.Errorf("ASSERTION: an upgrade to %s (%s) was not refused by the preflight: %v", bin, name, err)
		}
		after := helloImage(t, base)
		afterPIDs := panePIDs(t, base, sess)
		// The stream must still carry output after the refusal.
		drain := time.After(200 * time.Millisecond)
	drained:
		for {
			select {
			case _, ok := <-frames:
				if !ok {
					break drained
				}
			case <-drain:
				break drained
			}
		}
		var alive bool
		select {
		case _, alive = <-frames:
		case <-time.After(5 * time.Second):
		}
		results[name] = map[string]any{"binary": bin, "error": errString(err), "instance_kept": after.Instance == img.Instance, "stream_alive": alive}
		if after.Instance != img.Instance || after.PID != img.PID {
			t.Errorf("ASSERTION: a refused upgrade to %s changed the daemon: %+v then %+v", name, img, after)
		}
		for id, pid := range pids {
			if afterPIDs[id] != pid {
				t.Errorf("ASSERTION: a refused upgrade to %s changed window %s's pid from %d to %d", name, id, pid, afterPIDs[id])
			}
		}
		if !alive {
			t.Errorf("ASSERTION: the pane stream stopped after the refused upgrade to %s", name)
		}
	}
	writeHandoffArtifact(t, results)
}

// TestDaemonUpgradeFallsBackToPreviousBinary makes the new image fail to
// adopt the panes, on purpose, and checks the daemon comes back on the binary
// it ran before, from the copy it kept at start, with every pane's process
// and the same pid. The failure is an error, and in the second case a hang
// that only the watchdog ends.
func TestDaemonUpgradeFallsBackToPreviousBinary(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"error", []string{"TUIOS_TEST_HANDOFF_FAIL=error"}},
		{"hang", []string{"TUIOS_TEST_HANDOFF_FAIL=hang", "TUIOS_TEST_HANDOFF_WATCHDOG_MS=1500"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			startHandoffDaemon(t, base, tc.env...)
			second := copyTuios(t)
			const sess = "e2e-fallback"
			win := newCommandWindow(t, base, sess, "counter", false, "sh", "-c",
				`echo pid-$$; i=0; while :; do i=$((i+1)); echo f-$i; sleep 0.02; done`)
			waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
			before := panePIDs(t, base, sess)
			img := helloImage(t, base)
			started := time.Now()
			_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second})
			after := awaitNewImage(t, base, img, 40*time.Second)
			took := time.Since(started)
			exe := executableOf(t, after.PID)
			afterPIDs := panePIDs(t, base, sess)
			stats := handoffStats(t, base)
			time.Sleep(200 * time.Millisecond)
			ticks := counterLines(mustScrollback(t, base, sess, win), "f-")

			writeHandoffArtifact(t, map[string]any{
				"pid_before": img.PID, "pid_after": after.PID, "executable_after": exe,
				"panes_before": before, "panes_after": afterPIDs, "took_ms": took.Milliseconds(),
				"stats": stats, "tick_gap_at": firstGap(ticks),
			})
			if after.PID != img.PID {
				t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
			}
			if !strings.HasSuffix(exe, string(filepath.Separator)+filepath.Join("handoff", "tuios.prev")) {
				t.Errorf("ASSERTION: the daemon runs %s after the fallback, want the kept tuios.prev", exe)
			}
			if fb, _ := stats["fallback"].(bool); !fb {
				t.Errorf("ASSERTION: the image does not say it is the fallback: %v", stats)
			}
			for id, pid := range before {
				if afterPIDs[id] != pid {
					t.Errorf("ASSERTION: window %s had pid %d, and %d after the fallback", id, pid, afterPIDs[id])
				}
			}
			if i := firstGap(ticks); i >= 0 {
				t.Errorf("ASSERTION: the counter jumps from %d to %d", ticks[i-1], ticks[i])
			}
		})
	}
}

// TestDaemonUpgradeQueuesCLICallsDuringSwap calls the daemon in a loop while
// it upgrades, with a hold of a second between writing the manifest and the
// exec. The listening socket is never closed, so every call made meanwhile
// waits in the kernel's backlog and is answered by the new image: none may
// fail. The positive half: some calls were answered by each image, and some
// started while the daemon was stopped.
func TestDaemonUpgradeQueuesCLICallsDuringSwap(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	newCommandWindow(t, base, "e2e-queue", "anchor", false, "sh", "-c", "exec sleep 600")
	img := helloImage(t, base)

	type call struct {
		Start    time.Time `json:"start"`
		Took     float64   `json:"took_ms"`
		Instance string    `json:"instance"`
		Err      string    `json:"err,omitempty"`
	}
	var (
		mu    sync.Mutex
		calls []call
		stop  = make(chan struct{})
		done  = make(chan struct{})
	)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			c := call{Start: time.Now()}
			out, err := handoffCLI(t, base, 20*time.Second, "call", "hello", "{}")
			c.Took = float64(time.Since(c.Start).Microseconds()) / 1000
			if err != nil {
				c.Err = err.Error() + ": " + strings.TrimSpace(out)
			} else {
				var h daemonImage
				_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &h)
				c.Instance = h.Instance
			}
			mu.Lock()
			calls = append(calls, c)
			mu.Unlock()
		}
	}()
	time.Sleep(300 * time.Millisecond)
	upgradeAt := time.Now()
	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second, "hold_ms": 1000})
	after := awaitNewImage(t, base, img, 30*time.Second)
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-done

	var failed, oldImage, newImage, waited int
	for _, c := range calls {
		switch {
		case c.Err != "":
			failed++
		case c.Instance == img.Instance:
			oldImage++
		case c.Instance == after.Instance:
			newImage++
			if c.Start.After(upgradeAt) && c.Took > 300 {
				waited++
			}
		}
	}
	writeHandoffArtifact(t, map[string]any{
		"calls": len(calls), "failed": failed, "answered_by_old": oldImage,
		"answered_by_new": newImage, "waited_through_the_swap": waited, "detail": calls,
	})
	if failed > 0 {
		t.Errorf("ASSERTION: %d of %d calls made during the upgrade failed", failed, len(calls))
	}
	if oldImage == 0 || newImage == 0 || waited == 0 {
		t.Errorf("ASSERTION: the calls do not span the swap: %d answered by the old image, %d by the new, %d waited through it", oldImage, newImage, waited)
	}
}

// TestDaemonUpgradeRefusesWhileVerifyFanRuns asks for an upgrade while a fan
// check runs. The check reports its status on a pipe the daemon reads, which
// the exec would close, and the check would die of SIGPIPE when it writes
// its status. The upgrade must be refused, and the check's result must still
// arrive.
func TestDaemonUpgradeRefusesWhileVerifyFanRuns(t *testing.T) {
	t.Setenv("TUIOS_TEST_HANDOFF", "1")
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/up", "Do the thing."); err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	verify := make(chan string, 1)
	go func() {
		out, err := handoffCLI(t, base, 60*time.Second, "fan", "verify", "repo-try-up", "--", "sh", "-c", "sleep 4")
		verify <- out + errString(err)
	}()
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, _ := tuiosCLI(t, base, "fan", "compare", "repo-try-up", "--json", "--no-changes")
		if strings.Contains(out, `"running"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fan check never showed as running:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	img := helloImage(t, base)
	_, err := callVerb(t, base, "test-handoff", map[string]any{})
	var result string
	select {
	case result = <-verify:
	case <-time.After(60 * time.Second):
		t.Fatal("fan verify never returned")
	}
	after := helloImage(t, base)
	compare, _ := tuiosCLI(t, base, "fan", "compare", "repo-try-up", "--json", "--no-changes")

	writeHandoffArtifact(t, map[string]any{"refusal": errString(err), "verify": result, "compare": compare,
		"instance_kept": after.Instance == img.Instance})
	if err == nil || !strings.Contains(err.Error(), "fan check") {
		t.Errorf("ASSERTION: the upgrade was not refused while a fan check ran: %v", err)
	}
	if after.Instance != img.Instance {
		t.Errorf("ASSERTION: the daemon's image changed although the upgrade was refused")
	}
	if !strings.Contains(result, "2 of 2 passed") {
		t.Errorf("ASSERTION: the fan check's result did not arrive as passed:\n%s", result)
	}
}

// TestDaemonUpgradeMeasuresABusyDaemon measures an upgrade of a busy daemon:
// 60 panes at 200x50, each of which has printed 1000 coloured lines of 190
// characters, so every pane carries a full screen and 1000 rows of history.
// It is the case docs/design/live-handoff.md sizes its memory and pause
// figures for, and it is slow, so it runs only with
// TUIOS_E2E_HANDOFF_MEASURE=1. The artifact holds what the handoff measured:
// the pause from the first pane stopping to every pane reading again, the
// manifest's size, and the heap the new image peaked at while it rebuilt the
// panes.
func TestDaemonUpgradeMeasuresABusyDaemon(t *testing.T) {
	if os.Getenv("TUIOS_E2E_HANDOFF_MEASURE") == "" {
		t.Skip("set TUIOS_E2E_HANDOFF_MEASURE=1 to measure a busy daemon's upgrade")
	}
	base := t.TempDir()
	startHandoffDaemon(t, base)
	second := copyTuios(t)
	const sess = "e2e-busy"
	const panes = 60
	print := `i=0; while [ $i -lt 1000 ]; do i=$((i+1)); printf '\033[3%dm%04d %0186d\033[0m\n' $((i % 8)) $i 0; done; echo printed-1000; exec sleep 600`
	if _, err := callVerb(t, base, "new-session", map[string]any{"name": sess, "width": 200, "height": 50, "command": []string{"sh", "-c", print}}); err != nil {
		t.Fatal(err)
	}
	for range panes - 1 {
		_ = mustCall(t, base, "new-window", map[string]any{"session": sess, "focus": false, "command": []string{"sh", "-c", print}})
	}
	ids := make([]string, 0, panes)
	for id := range panePIDs(t, base, sess) {
		ids = append(ids, id)
	}
	if len(ids) != panes {
		t.Fatalf("made %d panes, want %d", len(ids), panes)
	}
	for _, id := range ids {
		waitHandoffPane(t, base, sess, id, regexp.MustCompile(`printed-1000`), 120*time.Second)
	}
	time.Sleep(2 * time.Second)

	img := helloImage(t, base)
	before := panePIDs(t, base, sess)
	started := time.Now()
	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second})
	after := awaitNewImage(t, base, img, 60*time.Second)
	endToEnd := time.Since(started)
	afterPIDs := panePIDs(t, base, sess)
	stats := handoffStats(t, base)
	kept := 0
	for id, pid := range before {
		if afterPIDs[id] == pid {
			kept++
		}
	}
	saveUpgradeArtifact(t, map[string]any{
		"os": runtime.GOOS, "panes": panes, "size": "200x50", "history_rows": 1000,
		"daemon_pid_before": img.PID, "daemon_pid_after": after.PID, "panes_kept": kept,
		"end_to_end_ms": endToEnd.Milliseconds(), "stats": stats,
	})
	if after.PID != img.PID || kept != panes {
		t.Errorf("ASSERTION: the busy upgrade kept the daemon pid %v and %d of %d panes", after.PID == img.PID, kept, panes)
	}
}
