package tuie2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The live handoff: the daemon execs a new image in place, and every pane's
// process keeps running under it. See docs/design/live-handoff.md.
//
// These tests drive a real daemon through the handoff and read the outcome
// back through the verbs a user has: hello for the daemon's pid and image,
// list-windows for each pane's process, capture-pane for its output and
// wait-for for its exit status. Each test writes what it measured to a JSON
// file under artifactDir.
//
// The daemon runs with TUIOS_TEST_HANDOFF=1, which registers two test-only
// verbs: test-quiesce stops and restarts one pane's reader, and test-handoff
// hands every pane to a new image of the same binary. Neither exists in a
// daemon started without the variable.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// handoffTestEnv makes the daemon register the test-only handoff verbs.
var handoffTestEnv = []string{"TUIOS_TEST_HANDOFF=1"}

// startHandoffDaemon starts a scratch daemon under base with the test verbs.
func startHandoffDaemon(t *testing.T, base string) {
	t.Helper()
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, handoffTestEnv, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
}

// callVerb runs `tuios call verb params` and decodes the result.
func callVerb(t *testing.T, base, verb string, params any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("encode %s params: %v", verb, err)
	}
	out, err := tuiosCLIEnv(t, base, handoffTestEnv, "call", verb, string(raw))
	if err != nil {
		return nil, fmt.Errorf("call %s: %w: %s", verb, err, strings.TrimSpace(out))
	}
	var res map[string]any
	// The result is the last line; a session may print notices before it.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		return nil, fmt.Errorf("call %s: decode %q: %w", verb, out, err)
	}
	return res, nil
}

func mustCall(t *testing.T, base, verb string, params any) map[string]any {
	t.Helper()
	res, err := callVerb(t, base, verb, params)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// daemonImage is the daemon's pid and the id of its current image, which a
// handoff keeps and changes.
type daemonImage struct {
	PID      int    `json:"pid"`
	Instance string `json:"instance"`
	Version  string `json:"daemon_version"`
}

func helloImage(t *testing.T, base string) daemonImage {
	t.Helper()
	res := mustCall(t, base, "hello", map[string]any{})
	raw, _ := json.Marshal(res)
	var img daemonImage
	_ = json.Unmarshal(raw, &img)
	if img.PID == 0 || img.Instance == "" {
		t.Fatalf("hello carried no pid or instance: %s", raw)
	}
	return img
}

// awaitNewImage waits until the daemon answers hello from an image other
// than before, and returns it.
func awaitNewImage(t *testing.T, base string, before daemonImage, timeout time.Duration) daemonImage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		res, err := callVerb(t, base, "hello", map[string]any{})
		if err == nil {
			raw, _ := json.Marshal(res)
			var img daemonImage
			_ = json.Unmarshal(raw, &img)
			if img.Instance != "" && img.Instance != before.Instance {
				return img
			}
			last = fmt.Errorf("still image %s", img.Instance)
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the daemon never answered from a new image within %s: %v", timeout, last)
	return daemonImage{}
}

// newCommandWindow opens a window running argv in session, creating the
// session when it does not exist, and returns the window's id.
func newCommandWindow(t *testing.T, base, session, name string, keepOpen bool, argv ...string) string {
	t.Helper()
	if _, err := callVerb(t, base, "session-info", map[string]any{"session": session}); err != nil {
		res := mustCall(t, base, "new-session", map[string]any{"name": session, "command": argv, "window_name": name, "width": 100, "height": 30})
		id, _ := res["window_id"].(string)
		if keepOpen {
			t.Fatalf("newCommandWindow: the first window of a session cannot be kept open; open it second")
		}
		return id
	}
	res := mustCall(t, base, "new-window", map[string]any{"session": session, "name": name, "keep_open": keepOpen, "focus": false, "command": argv})
	id, _ := res["window_id"].(string)
	if id == "" {
		t.Fatalf("new-window returned no window_id: %v", res)
	}
	return id
}

// panePIDs is each window's process, by window id, as list-windows reports it.
func panePIDs(t *testing.T, base, session string) map[string]int {
	t.Helper()
	out, err := tuiosOut(base, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	var list struct {
		Windows []struct {
			ID  string `json:"window_id"`
			PID int    `json:"pid"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("list-windows: decode %q: %v", out, err)
	}
	pids := map[string]int{}
	for _, w := range list.Windows {
		pids[w.ID] = w.PID
	}
	return pids
}

// waitPane waits until a pane's scrollback matches re, and returns the match.
func waitHandoffPane(t *testing.T, base, session, window string, re *regexp.Regexp, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lines []string
	for time.Now().Before(deadline) {
		var err error
		lines, err = daemonScrollback(base, session, window, 2000)
		if err == nil {
			for _, l := range lines {
				if m := re.FindStringSubmatch(l); m != nil {
					return m
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane %s never showed %s; last capture:\n%s", window, re, strings.Join(lines, "\n"))
	return nil
}

// windowExitCode waits for a window's process to exit and returns its status.
func windowExitCode(t *testing.T, base, session, window string, timeout time.Duration) int {
	t.Helper()
	res := mustCall(t, base, "wait-for", map[string]any{
		"session": session, "window": window, "condition": "window-exit", "timeout": int(timeout / time.Millisecond),
	})
	code, ok := res["exit_code"].(float64)
	if !ok {
		t.Fatalf("wait-for window-exit carried no exit_code: %v", res)
	}
	return int(code)
}

// writeHandoffArtifact saves what a test measured.
func writeHandoffArtifact(t *testing.T, v any) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("encode the artifact: %v", err)
	}
	path := filepath.Join(artifactDir(t), "handoff.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write the artifact: %v", err)
	}
	t.Logf("artifact: %s\n%s", path, data)
	return path
}

// counterLines reads the numbers of a pane's "<prefix><n>" lines, oldest
// first, dropping the last line, which may be cut mid-write.
func counterLines(lines []string, prefix string) []int {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `(\d+)$`)
	var out []int
	for _, l := range lines {
		if m := re.FindStringSubmatch(strings.TrimRight(l, " ")); m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, n)
		}
	}
	if len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out
}

// firstGap is the index of the first number that does not follow the one
// before it, or -1.
func firstGap(nums []int) int {
	for i := 1; i < len(nums); i++ {
		if nums[i] != nums[i-1]+1 {
			return i
		}
	}
	return -1
}

// TestQuiesceLosesNoBytes quiesces a pane that prints a fast numbered counter,
// a hundred times, and checks that the reader took no byte while it was
// quiesced and that the output on either side of every quiesce is
// contiguous: no number lost and none repeated.
//
// How it could pass wrongly, and what stops it: a quiesce that never stopped
// the reader leaves the output contiguous too, because nothing execs. So the
// verb reports how far the pane's stream moved during each quiesce, summed,
// and it must be zero; the counter must also have moved between the first and
// the last capture, so the pane was producing the whole time.
func TestQuiesceLosesNoBytes(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const session = "e2e-quiesce"
	win := newCommandWindow(t, base, session, "counter", false,
		"sh", "-c", `i=0; while :; do i=$((i+1)); echo n-$i; done`)
	waitHandoffPane(t, base, session, win, regexp.MustCompile(`^n-\d+`), 10*time.Second)
	before := counterLines(mustScrollback(t, base, session, win), "n-")

	res := mustCall(t, base, "test-quiesce", map[string]any{"session": session, "window": win, "cycles": 100, "hold_ms": 5})
	read, _ := res["read_while_quiesced"].(float64)

	time.Sleep(200 * time.Millisecond)
	_ = mustCall(t, base, "send-keys", map[string]any{"session": session, "window": win, "keys": "ctrl+c"})
	after := counterLines(mustScrollback(t, base, session, win), "n-")

	artifact := map[string]any{"cycles": 100, "read_while_quiesced": int64(read), "lines_checked": len(after)}
	if len(after) > 0 {
		artifact["first"], artifact["last"] = after[0], after[len(after)-1]
	}
	writeHandoffArtifact(t, artifact)

	if read != 0 {
		t.Errorf("ASSERTION: the pane's reader took %d bytes while it was quiesced", int64(read))
	}
	if len(before) == 0 || len(after) < 100 || after[len(after)-1] <= before[len(before)-1] {
		t.Fatalf("ASSERTION: the counter did not run across the quiesces (before %d lines, after %d)", len(before), len(after))
	}
	if i := firstGap(after); i >= 0 {
		t.Errorf("ASSERTION: the counter jumps from %d to %d", after[i-1], after[i])
	}
}

func mustScrollback(t *testing.T, base, session, window string) []string {
	t.Helper()
	lines, err := daemonScrollback(base, session, window, 5000)
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	return lines
}

// TestQuiesceAfterBurstDoesNotWait quiesces a pane whose program printed a
// burst of output as fast as it could and then went quiet for good. Each
// quiesce must return at once.
//
// After a burst the reader has just taken read after read with output always
// waiting, so a reader that skipped the wait for output while output kept
// coming would be blocked in read for this one, holding readMu, and the
// quiesce would wait on a program that prints nothing more. The wait the
// reader parks in must report the master readable only when it is. Twenty
// cycles, each of which takes readMu, must all finish well inside one quiesce
// deadline (10s).
func TestQuiesceAfterBurstDoesNotWait(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const session = "e2e-burst"
	const marker = "sleep 600.4646"
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", marker).Run() })
	newCommandWindow(t, base, session, "anchor", false, "sh", "-c", "exec "+marker+"1")
	burst := newCommandWindow(t, base, session, "burst", false, "sh", "-c",
		`yes 0123456789abcdefghijklmnopqrstuvwxyz | head -c 8388608; printf '\nburst-done\n'; exec `+marker+`2`)
	waitHandoffPane(t, base, session, burst, regexp.MustCompile(`^burst-done`), 60*time.Second)

	started := time.Now()
	res, err := callVerbWithin(t, base, 20*time.Second, "test-quiesce", map[string]any{"session": session, "window": burst, "cycles": 20})
	took := time.Since(started)
	writeHandoffArtifact(t, map[string]any{"burst_bytes": 8388608, "cycles": 20, "quiesce_ms": took.Milliseconds(), "error": fmt.Sprint(err)})
	if err != nil {
		t.Fatalf("ASSERTION: quiescing the pane that went quiet after a burst failed after %s: %v", took.Round(time.Millisecond), err)
	}
	if read, _ := res["read_while_quiesced"].(float64); read != 0 {
		t.Errorf("ASSERTION: the pane's reader took %d bytes while it was quiesced", int64(read))
	}
	if took > 5*time.Second {
		t.Errorf("ASSERTION: twenty quiesces of a quiet pane took %s", took.Round(time.Millisecond))
	}
}

// TestExitDuringQuiesceKeepsStatus hands the panes to a new image while one of
// them exits: the pane exits with status 7 after the manifest is written and
// before the exec, with no output waiting to be read. The new image must
// report 7.
//
// The status exists once, and only the reap reads it. A daemon that reaped
// the pane before the exec kept 7 in memory the exec discards, and the new
// image found no child to wait on. The positive half is a pane that exits
// with 5 well before the handoff: it is reaped by the old image, written down
// as exited, and must also read 5 after it.
//
// On macOS a process that printed and then exited is not reapable while its
// output sits unread, which would hide the race, so the exiting pane prints
// only before the handoff starts.
func TestExitDuringQuiesceKeepsStatus(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const session = "e2e-exitq"
	newCommandWindow(t, base, session, "anchor", false, "sh", "-c", "exec sleep 600")
	early := newCommandWindow(t, base, session, "early", true, "sh", "-c", "exit 5")
	late := newCommandWindow(t, base, session, "late", true, "sh", "-c", "echo ready; sleep 2; exit 7")
	waitHandoffPane(t, base, session, late, regexp.MustCompile(`^ready`), 10*time.Second)
	if code := windowExitCode(t, base, session, early, 10*time.Second); code != 5 {
		t.Fatalf("the early pane exited with %d before any handoff, want 5", code)
	}

	img := helloImage(t, base)
	started := time.Now()
	res := mustCall(t, base, "test-handoff", map[string]any{"hold_ms": 3500})
	after := awaitNewImage(t, base, img, 20*time.Second)
	lateCode := windowExitCode(t, base, session, late, 10*time.Second)
	earlyCode := windowExitCode(t, base, session, early, 10*time.Second)

	writeHandoffArtifact(t, map[string]any{
		"pid_before": img.PID, "pid_after": after.PID,
		"late_exit": lateCode, "early_exit": earlyCode,
		"manifest_bytes": res["manifest_bytes"], "handoff_ms": time.Since(started).Milliseconds(),
	})
	if after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	if earlyCode != 5 {
		t.Errorf("ASSERTION: the pane that exited before the handoff reads %d after it, want 5", earlyCode)
	}
	if lateCode != 7 {
		t.Errorf("ASSERTION: the pane that exited during the handoff reads %d, want 7", lateCode)
	}
}

// TestSelfExecAdoptsPanes hands the panes to a new image of the same binary
// and checks the daemon kept its pid, every pane kept its process, and a pane
// that exits after the handoff reports its own status, 9, which only a parent
// can read.
//
// The second half lets a fork happen while the descriptors are exposed, and
// then stops the daemon and starts another. A child forked in that span would
// hold the start lock, and no daemon could start after.
func TestSelfExecAdoptsPanes(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const session = "e2e-selfexec"
	a := newCommandWindow(t, base, session, "a", false, "sh", "-c", "echo pid-$$; exec sleep 600")
	b := newCommandWindow(t, base, session, "b", true, "sh", "-c", "echo pid-$$; read x; exit 9")
	pidA := waitHandoffPane(t, base, session, a, regexp.MustCompile(`^pid-(\d+)`), 10*time.Second)[1]
	pidB := waitHandoffPane(t, base, session, b, regexp.MustCompile(`^pid-(\d+)`), 10*time.Second)[1]
	before := panePIDs(t, base, session)
	if strconv.Itoa(before[a]) != pidA || strconv.Itoa(before[b]) != pidB {
		t.Fatalf("list-windows pids %v do not match the panes' own pid-%s and pid-%s", before, pidA, pidB)
	}

	img := helloImage(t, base)
	started := time.Now()
	res := mustCall(t, base, "test-handoff", map[string]any{})
	after := awaitNewImage(t, base, img, 20*time.Second)
	pause := time.Since(started)
	afterPIDs := panePIDs(t, base, session)

	_ = mustCall(t, base, "send-text", map[string]any{"session": session, "window": b, "text": "go\n"})
	code := windowExitCode(t, base, session, b, 10*time.Second)

	writeHandoffArtifact(t, map[string]any{
		"pid_before": img.PID, "pid_after": after.PID,
		"panes_before": before, "panes_after": afterPIDs,
		"exit_after": code, "manifest_bytes": res["manifest_bytes"], "handoff_ms": pause.Milliseconds(),
	})
	if after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	for id, pid := range before {
		if afterPIDs[id] != pid {
			t.Errorf("ASSERTION: window %s had pid %d, and %d after the handoff", id, pid, afterPIDs[id])
		}
	}
	if code != 9 {
		t.Errorf("ASSERTION: the pane that exited after the handoff reads %d, want 9", code)
	}

	t.Run("fork in the exposed window", func(t *testing.T) {
		img := helloImage(t, base)
		_ = mustCall(t, base, "test-handoff", map[string]any{"fork_in_window": true})
		awaitNewImage(t, base, img, 20*time.Second)
		t.Cleanup(func() { _ = exec.Command("pkill", "-f", "sleep 20.4242").Run() })
		if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
			t.Fatalf("kill-server: %v\n%s", err, out)
		}
		if out, err := tuiosCLIEnv(t, base, handoffTestEnv, "start-server"); err != nil {
			t.Fatalf("ASSERTION: start-server after kill-server failed, so something kept the start lock: %v\n%s", err, out)
		}
	})
}

// callVerbWithin is callVerb with a deadline on the whole call, connect
// included: a daemon that stopped accepting would leave callVerb waiting in
// the listen backlog for as long as the test runs.
func callVerbWithin(t *testing.T, base string, within time.Duration, verb string, params any) (map[string]any, error) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("encode %s params: %v", verb, err)
	}
	pinPreV080Looks(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	cmd := exec.CommandContext(ctx, tuiosBin, "call", verb, string(raw))
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, handoffTestEnv...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("call %s did not return within %s", verb, within)
	}
	if err != nil {
		return nil, fmt.Errorf("call %s: %w: %s", verb, err, strings.TrimSpace(string(out)))
	}
	var res map[string]any
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		return nil, fmt.Errorf("call %s: decode %q: %w", verb, out, err)
	}
	return res, nil
}

// TestHandoffRefusedWhileHostedPaneRuns opens a pane this daemon runs for
// another machine and asks for a handoff. A hosted pane's master is not among
// the descriptors the exec keeps, so a handoff would close it and end the
// remote pane. It must be refused with an error that names the hosted pane,
// leave the daemon on its image and accepting, and leave the pane's process
// running.
//
// How it could pass wrongly, and what stops it: a handoff refused for any
// other reason also leaves everything as it was. So the error must name the
// hosted pane, and the positive half closes the hosted pane and hands off
// with everything else unchanged, which must then go through to a new image.
func TestHandoffRefusedWhileHostedPaneRuns(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const session = "e2e-hosted"
	const marker = "sleep 600.4343"
	newCommandWindow(t, base, session, "local", false, "sh", "-c", "exec "+marker+"1")
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", marker).Run() })

	// open-pane is what another machine's daemon sends over a link. The
	// connection becomes the pane's terminal, so it is held open, as the
	// owner's would be.
	raw, err := dialFileVerbs(t, base).call("open-pane", map[string]any{
		"width": 80, "height": 24, "command": []string{"sh", "-c", "exec " + marker + "2"},
	})
	if err != nil {
		t.Fatalf("open-pane: %v", err)
	}
	var opened struct {
		Pane string `json:"pane"`
	}
	if err := json.Unmarshal(raw, &opened); err != nil || opened.Pane == "" {
		t.Fatalf("open-pane returned no pane id: %s", raw)
	}
	pane := opened.Pane
	hostedRuns := func() bool { return len(commandLinesContaining(t, marker+"2")) > 0 }
	deadline := time.Now().Add(5 * time.Second)
	for !hostedRuns() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !hostedRuns() {
		t.Fatalf("the hosted pane's process never started")
	}

	img := helloImage(t, base)
	_, refusal := callVerbWithin(t, base, 30*time.Second, "test-handoff", map[string]any{})
	if refusal == nil {
		awaitNewImage(t, base, img, 20*time.Second)
		writeHandoffArtifact(t, map[string]any{"refusal": nil, "hosted_alive_after_handoff": hostedRuns()})
		t.Fatalf("ASSERTION: the handoff went ahead with a hosted pane open, which closes its terminal at the exec")
	}
	still := helloImage(t, base)
	hostedAlive := hostedRuns()

	_ = mustCall(t, base, "close-pane", map[string]any{"pane": pane})
	res, err := callVerbWithin(t, base, 30*time.Second, "test-handoff", map[string]any{})
	if err != nil {
		t.Fatalf("ASSERTION: the handoff after the hosted pane closed failed: %v (the refused one: %v)", err, refusal)
	}
	after := awaitNewImage(t, base, img, 20*time.Second)

	writeHandoffArtifact(t, map[string]any{
		"refusal": refusal.Error(), "instance_before": img.Instance, "instance_after_refusal": still.Instance,
		"hosted_alive_after_refusal": hostedAlive, "instance_after_close": after.Instance, "panes": res["panes"],
	})
	if !strings.Contains(refusal.Error(), "another machine") {
		t.Errorf("ASSERTION: the refusal does not name the hosted pane: %s", refusal)
	}
	if still.Instance != img.Instance || still.PID != img.PID {
		t.Errorf("ASSERTION: the refused handoff changed the daemon from %+v to %+v", img, still)
	}
	if !hostedAlive {
		t.Errorf("ASSERTION: the hosted pane's process did not survive the refused handoff")
	}
}

// TestHandoffRefusedByPaneNotReadingInput hands off while one pane's program
// asked for replies and never reads its input. The replies fill the terminal's
// input queue, so the write of the next one blocks until the program reads,
// which it never does. A handoff that waited for that write would hang with
// the sockets not accepting and every pane's open and close held, so the
// daemon would answer nobody. It must give up within its quiesce deadline,
// fail with an error that says why, and put the daemon back.
//
// How it could pass wrongly, and what stops it: the program might not fill
// the queue at all, and the handoff would go through. So the refusal is
// required, the daemon must answer hello on its old image after it, and the
// positive half closes the stuck pane, after which the handoff goes through.
func TestHandoffRefusedByPaneNotReadingInput(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const session = "e2e-noread"
	const marker = "sleep 600.4545"
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", marker).Run() })
	newCommandWindow(t, base, session, "anchor", false, "sh", "-c", "exec "+marker+"1")
	// Raw mode, so the input queue blocks the writer when full rather than
	// dropping, and no echo, so the replies are not printed back. Each DA1
	// query is answered with a reply of a dozen bytes or more, and 6000 of
	// them are far past the input queue on Linux (4 KB) and macOS.
	stuck := newCommandWindow(t, base, session, "stuck", false, "sh", "-c",
		`stty raw -echo; i=0; while [ $i -lt 6000 ]; do printf '\033[c'; i=$((i+1)); done; printf 'flooded\r\n'; exec `+marker+`2`)
	waitHandoffPane(t, base, session, stuck, regexp.MustCompile(`flooded`), 30*time.Second)

	img := helloImage(t, base)
	started := time.Now()
	// The quiesce deadline is 10s. Twice that is a hang.
	_, refusal := callVerbWithin(t, base, 20*time.Second, "test-handoff", map[string]any{})
	took := time.Since(started)
	t.Logf("the handoff answered after %s: %v", took.Round(time.Millisecond), refusal)
	if refusal == nil {
		t.Fatalf("ASSERTION: the handoff went ahead while a reply to a pane could not be written")
	}
	if strings.Contains(refusal.Error(), "did not return within") {
		t.Fatalf("ASSERTION: the handoff hung on the pane that does not read its input: %s", refusal)
	}
	still, helloErr := callVerbWithin(t, base, 10*time.Second, "hello", map[string]any{})
	if helloErr != nil {
		t.Fatalf("ASSERTION: the daemon did not answer after the refused handoff: %v", helloErr)
	}

	_ = mustCall(t, base, "close-window", map[string]any{"session": session, "window": stuck})
	if _, err := callVerbWithin(t, base, 30*time.Second, "test-handoff", map[string]any{}); err != nil {
		t.Fatalf("ASSERTION: the handoff after the stuck pane closed failed: %v (the refused one: %v)", err, refusal)
	}
	after := awaitNewImage(t, base, img, 20*time.Second)

	writeHandoffArtifact(t, map[string]any{
		"refusal": refusal.Error(), "refusal_ms": took.Milliseconds(), "instance_before": img.Instance,
		"instance_after_refusal": still["instance"], "instance_after_close": after.Instance,
	})
	if !strings.Contains(refusal.Error(), "not reading its input") {
		t.Errorf("ASSERTION: the refusal does not say the pane is not reading its input: %s", refusal)
	}
	if still["instance"] != img.Instance {
		t.Errorf("ASSERTION: the refused handoff changed the daemon's image from %s to %v", img.Instance, still["instance"])
	}
}
