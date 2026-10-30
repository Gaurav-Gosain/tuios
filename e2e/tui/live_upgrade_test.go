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
	"syscall"
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
		// A build without handoff-accepts fails to run it; one that reads
		// another handoff version says so.
		if err == nil || !strings.Contains(err.Error(), "does not run as a tuios") && !strings.Contains(err.Error(), "cannot read this daemon's state") {
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
			if runtime.GOOS == "linux" {
				// The fallback execs the descriptor of /proc/self/exe the
				// daemon kept, so the image runs the file it ran before.
				if want := resolvedTuios(t); exe != want {
					t.Errorf("ASSERTION: the daemon runs %s after the fallback, want the binary it ran before, %s", exe, want)
				}
			} else if !strings.HasSuffix(exe, string(filepath.Separator)+filepath.Join("handoff", "tuios.prev")) {
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

// TestDaemonUpgradeRefusesWhileHookOrPluginRuns asks for an upgrade while a
// hook command runs, and while a plugin command runs. Each is a child of the
// daemon whose output it reads on a pipe the exec would close: the command
// would die of SIGPIPE on its next write and never be reaped. Each upgrade
// must be refused, naming what runs. The positive half: once the command has
// finished, the same upgrade goes through.
func TestDaemonUpgradeRefusesWhileHookOrPluginRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config func(t *testing.T) string
		start  func(t *testing.T, base string)
		refuse string
	}{
		{
			name:   "hook",
			config: func(*testing.T) string { return "[hooks]\nafter-new-window = \"sleep 4\"\n" },
			start: func(t *testing.T, base string) {
				newCommandWindow(t, base, "e2e-pipes", "second", false, "sh", "-c", "exec sleep 600")
			},
			refuse: "the after-new-window hook is running",
		},
		{
			name: "plugin",
			config: func(t *testing.T) string {
				dir := filepath.Join(t.TempDir(), "slow")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				manifest := "id = \"e2e.slow\"\nname = \"E2E slow\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.5\"\n\n" +
					"[[actions]]\nid = \"wait\"\ntitle = \"Wait\"\ncontexts = [\"pane\"]\ncommand = [\"sh\", \"-c\", \"sleep 4; echo done\"]\n"
				if err := os.WriteFile(filepath.Join(dir, "herdr-plugin.toml"), []byte(manifest), 0o644); err != nil {
					t.Fatal(err)
				}
				return "[plugins]\nenabled = [\"e2e.slow\"]\ndirs = [" + strconv.Quote(dir) + "]\n"
			},
			start: func(t *testing.T, base string) {
				if out, err := handoffCLI(t, base, 20*time.Second, "plugins", "run", "e2e.slow", "wait"); err != nil {
					t.Fatalf("plugins run: %v\n%s", err, out)
				}
			},
			refuse: "a command of plugin e2e.slow is running",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			writeConfig(t, base, tc.config(t))
			startHandoffDaemon(t, base)
			second := copyTuios(t)
			newCommandWindow(t, base, "e2e-pipes", "anchor", false, "sh", "-c", "exec sleep 600")
			// The anchor's own hook, if any, finishes before the one under test.
			time.Sleep(4500 * time.Millisecond)
			img := helloImage(t, base)
			tc.start(t, base)
			_, refused := callVerb(t, base, "test-handoff", map[string]any{"binary": second})
			kept := helloImage(t, base)
			time.Sleep(5 * time.Second)
			_, again := callVerb(t, base, "test-handoff", map[string]any{"binary": second})
			var after daemonImage
			if again == nil {
				after = awaitNewImage(t, base, img, 30*time.Second)
			}
			writeHandoffArtifact(t, map[string]any{"refusal": errString(refused), "instance_kept": kept.Instance == img.Instance,
				"after_finish": errString(again), "new_instance": after.Instance})
			if refused == nil || !strings.Contains(refused.Error(), tc.refuse) {
				t.Errorf("ASSERTION: the upgrade was not refused while the %s ran (want %q): %v", tc.name, tc.refuse, refused)
			}
			if kept.Instance != img.Instance {
				t.Errorf("ASSERTION: the daemon's image changed although the upgrade was refused")
			}
			if again != nil {
				t.Errorf("ASSERTION: the upgrade was still refused after the %s finished: %v", tc.name, again)
			}
		})
	}
}

// TestDaemonUpgradeEndsWhenFallbackExecFails makes the new image hang while
// it adopts the panes, and removes the binary the fallback would exec while
// the upgrade is held between the manifest and the exec. The watchdog fires,
// the fallback exec fails, and the daemon must then exit, so every pane hangs
// up, rather than hold the panes and serve nothing.
func TestDaemonUpgradeEndsWhenFallbackExecFails(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("on Linux the fallback is a descriptor the daemon holds, not a file a test can remove, so its exec cannot be made to fail from outside; the exit after a failed fallback is the same code on every system")
	}
	base := t.TempDir()
	startHandoffDaemon(t, base, "TUIOS_TEST_HANDOFF_FAIL=hang", "TUIOS_TEST_HANDOFF_WATCHDOG_MS=1500")
	second := copyTuios(t)
	win := newCommandWindow(t, base, "e2e-fallback-fails", "counter", false, "sh", "-c",
		`echo pid-$$; while :; do echo x; sleep 0.05; done`)
	waitHandoffPane(t, base, "e2e-fallback-fails", win, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
	img := helloImage(t, base)
	pane := panePIDs(t, base, "e2e-fallback-fails")[win]
	if pane <= 0 {
		t.Fatalf("list-windows gave window %s no pid", win)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(img.PID, syscall.SIGKILL)
		_ = syscall.Kill(pane, syscall.SIGKILL)
	})
	var prev string
	_ = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "tuios.prev" && filepath.Base(filepath.Dir(path)) == "handoff" {
			prev = path
		}
		return nil
	})
	if prev == "" {
		t.Fatal("the daemon kept no handoff/tuios.prev under the test's folders")
	}

	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": second, "hold_ms": 1500})
	removeErr := os.Remove(prev)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && (processAlive(img.PID) || processAlive(pane)) {
		time.Sleep(100 * time.Millisecond)
	}
	daemonAlive, paneAlive := processAlive(img.PID), processAlive(pane)
	writeHandoffArtifact(t, map[string]any{"daemon_pid": img.PID, "pane_pid": pane, "removed": errString(removeErr),
		"daemon_alive": daemonAlive, "pane_alive": paneAlive})
	if removeErr != nil {
		t.Fatalf("remove %s during the hold: %v", prev, removeErr)
	}
	if daemonAlive {
		t.Errorf("ASSERTION: the daemon (pid %d) still runs 20s after its fallback exec failed, holding the panes and serving nothing", img.PID)
	}
	if paneAlive {
		t.Errorf("ASSERTION: the pane's process (pid %d) still runs 20s after its daemon's fallback failed, so it never hung up", pane)
	}
}

// TestDaemonUpgradePreflightRefusals asks for upgrades the preflight must
// refuse, each with a message that says what is wrong and what to do: a tuios
// its group can write, a program that runs but prints no answer, one that
// answers for another system, and tuios builds that read another handoff
// version than this daemon writes. The daemon's image and every pane's process
// must not change.
func TestDaemonUpgradePreflightRefusals(t *testing.T) {
	script := func(t *testing.T, body string) string {
		path := filepath.Join(t.TempDir(), "fake-tuios")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	other := "plan9"
	for _, tc := range []struct {
		name   string
		binary func(t *testing.T) string
		want   string
	}{
		{"group-writable", func(t *testing.T) string {
			bin := copyTuios(t)
			if err := os.Chmod(bin, 0o775); err != nil {
				t.Fatal(err)
			}
			return bin
		}, "can be written by its group or by others (mode 0775), so this daemon will not run it. Make it writable only by its owner: chmod go-w"},
		{"owned by another user", func(t *testing.T) string {
			if os.Geteuid() != 0 {
				t.Skip("giving the binary to another user needs root")
			}
			bin := copyTuios(t)
			if err := os.Chown(bin, 65534, 65534); err != nil {
				t.Fatal(err)
			}
			return bin
		}, "is owned by user 65534, not by you or root, so this daemon will not run it. Install it as yourself or as root"},
		{"no answer", func(t *testing.T) string { return script(t, "echo hello") },
			"so it is not a tuios this daemon can upgrade to. Check the path names a tuios binary"},
		{"another system", func(t *testing.T) string {
			return script(t, `echo '{"handoff":[1,1],"os":"`+other+`"}'`)
		}, "is built for " + other + ", and this daemon runs on " + runtime.GOOS + ". Upgrade to a tuios built for " + runtime.GOOS},
		// Version 1 is what v0.11.1 wrote, with a gob daemon record, and what
		// main wrote with a JSON one under the same number. A build that
		// reads only 1 must not be handed this daemon's records.
		{"reads only handoff v1", func(t *testing.T) string {
			return script(t, `echo '{"handoff":[1,1],"version":"0.11.1","os":"`+runtime.GOOS+`"}'`)
		}, "cannot read this daemon's state. This daemon writes handoff v" + strconv.Itoa(session.HandoffVersion) + ", and that binary reads v1 to v1"},
		{"reads only a newer handoff", func(t *testing.T) string {
			v := strconv.Itoa(session.HandoffVersion + 1)
			return script(t, `echo '{"handoff":[`+v+`,`+v+`],"version":"9.9.9","os":"`+runtime.GOOS+`"}'`)
		}, "cannot read this daemon's state. This daemon writes handoff v" + strconv.Itoa(session.HandoffVersion)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			startHandoffDaemon(t, base)
			newCommandWindow(t, base, "e2e-preflight", "anchor", false, "sh", "-c", "exec sleep 600")
			pids := panePIDs(t, base, "e2e-preflight")
			img := helloImage(t, base)
			bin := tc.binary(t)
			_, err := callVerb(t, base, "test-handoff", map[string]any{"binary": bin})
			time.Sleep(time.Second)
			after := helloImage(t, base)
			afterPIDs := panePIDs(t, base, "e2e-preflight")
			writeHandoffArtifact(t, map[string]any{"binary": bin, "refusal": errString(err), "instance_kept": after.Instance == img.Instance,
				"panes_before": pids, "panes_after": afterPIDs})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ASSERTION: the upgrade to %s was not refused with %q: %v", bin, tc.want, err)
			}
			if err != nil && !strings.Contains(err.Error(), "restart-server") && strings.Contains(tc.want, "handoff v") {
				t.Errorf("ASSERTION: the version refusal does not say to restart instead: %v", err)
			}
			if after.Instance != img.Instance || after.PID != img.PID {
				t.Errorf("ASSERTION: a refused upgrade changed the daemon: %+v then %+v", img, after)
			}
			if len(pids) == 0 {
				t.Fatalf("no pane before the upgrade")
			}
			for id, pid := range pids {
				if afterPIDs[id] != pid || !processAlive(pid) {
					t.Errorf("ASSERTION: a refused upgrade ended window %s: pid %d before, %d after", id, pid, afterPIDs[id])
				}
			}
		})
	}
}

// resolvedTuios is the binary under test with its links resolved, as
// /proc/<pid>/exe names it.
func resolvedTuios(t *testing.T) string {
	t.Helper()
	exe, err := filepath.EvalSymlinks(tuiosBin)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestDaemonUpgradeFallsBackAfterInstallerReplacesBinary is the case the
// Linux fallback descriptor exists for. The daemon runs a copy of tuios, and
// an installer then renames a new file over that path, as install.sh does, so
// the inode the daemon runs has no name left. The upgrade is asked for to that
// path, and the new image fails to adopt the panes on purpose. The fallback
// must exec the old image through the descriptor of /proc/self/exe the
// daemon kept at start: the daemon keeps its pid and every pane, says it is
// the fallback, and runs the old, now nameless, inode.
func TestDaemonUpgradeFallsBackAfterInstallerReplacesBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("elsewhere the fallback is the kept file tuios.prev, which TestDaemonUpgradeFallsBackToPreviousBinary covers")
	}
	base := t.TempDir()
	installed := copyTuios(t)
	killDaemon(t, base)
	cmd := exec.Command(installed, "start-server")
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TUIOS_TEST_HANDOFF_FAIL=error")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, handoffTestEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start-server from %s: %v\n%s", installed, err, out)
	}
	t.Cleanup(func() { killDaemon(t, base) })
	const sess = "e2e-installer"
	win := newCommandWindow(t, base, sess, "counter", false, "sh", "-c",
		`echo pid-$$; i=0; while :; do i=$((i+1)); echo i-$i; sleep 0.02; done`)
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
	img := helloImage(t, base)
	before := panePIDs(t, base, sess)
	if exe := executableOf(t, img.PID); exe != installed {
		t.Fatalf("the daemon runs %s, want the installed copy %s", exe, installed)
	}

	// The installer: a new file renamed over the path the daemon runs.
	fresh := copyTuios(t)
	if err := os.Rename(fresh, installed); err != nil {
		t.Fatalf("rename the new binary over %s: %v", installed, err)
	}

	_ = mustCall(t, base, "test-handoff", map[string]any{"binary": installed})
	after := awaitNewImage(t, base, img, 40*time.Second)
	exe := executableOf(t, after.PID)
	afterPIDs := panePIDs(t, base, sess)
	stats := handoffStats(t, base)
	time.Sleep(200 * time.Millisecond)
	ticks := counterLines(mustScrollback(t, base, sess, win), "i-")
	writeHandoffArtifact(t, map[string]any{
		"pid_before": img.PID, "pid_after": after.PID, "executable_after": exe,
		"panes_before": before, "panes_after": afterPIDs, "stats": stats, "tick_gap_at": firstGap(ticks),
	})
	if after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	if exe != installed+" (deleted)" {
		t.Errorf("ASSERTION: the daemon runs %q after the fallback, want the replaced inode, %q", exe, installed+" (deleted)")
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
}
