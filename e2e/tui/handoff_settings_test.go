package tuie2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image/color"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuitest"
)

// The settings and records a window carries outside the pane's own state,
// across a live handoff (docs/design/live-handoff.md, section 7.2): a window
// that closes when its program exits, the order sessions list in and which
// one a call naming none acts on, where a session goes when its workspace
// empties, the colours its programs are told, the plugin that opened a pane,
// the protocol a headless agent's pane speaks, the pane that opened another,
// and the session a moved window came from. Each test sets one up, hands the
// panes to a new image with test-handoff, and reads the outcome back through
// the verbs a user has. Each writes what it measured to handoff.json under
// artifactDir.
//
// How these could pass wrongly, written down first:
//   - A window could close because the handoff lost it rather than because
//     its program exited. Every test checks the window is still listed after
//     the handoff, before its program is ended.
//   - A setting could be re-learned after the handoff rather than carried:
//     each assertion is made before anything that would teach it again (no
//     client attaches, no new report is sent).
//   - The handoff could not have happened: each test checks the image changed
//     and the daemon kept its pid.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Live handoff").

// windowListed reports whether session lists window.
func windowListed(t *testing.T, base, session, window string) bool {
	t.Helper()
	_, ok := panePIDs(t, base, session)[window]
	return ok
}

// waitWindowGone waits until session no longer lists window, and reports
// whether it went.
func waitWindowGone(t *testing.T, base, session, window string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !windowListed(t, base, session, window) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// handOver runs test-handoff with params and waits for the new image. It
// fails the test unless the daemon kept its pid, and returns the handoff's
// answer.
func handOver(t *testing.T, base string, params map[string]any) map[string]any {
	t.Helper()
	img := helloImage(t, base)
	res := mustCall(t, base, "test-handoff", params)
	after := awaitNewImage(t, base, img, 20*time.Second)
	if after.PID != img.PID {
		t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", img.PID, after.PID)
	}
	return res
}

// TestSelfExecClosesCloseOnExitWindows hands over three windows made with
// new-window: two with close_on_exit, and one without as the control.
//
//   - "during" exits two seconds after it starts, inside the hold between
//     the manifest and the exec, so its exit is reaped by the new image. Its
//     window must close once the new image is up.
//   - "after" exits when a line is typed into it after the handoff. Its
//     window must close.
//   - "plain" exits the same way and must stay, with its status, as a
//     window without close_on_exit does on a detached session.
//
// The handoff must carry all four panes (the anchor included): a window that
// exited and closed before the manifest was written would make the "during"
// half pass without exercising anything.
func TestSelfExecClosesCloseOnExitWindows(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-coe"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	open := func(name string, closeOnExit bool, script string) string {
		res := mustCall(t, base, "new-window", map[string]any{
			"session": sess, "name": name, "focus": false, "close_on_exit": closeOnExit, "command": []string{"sh", "-c", script},
		})
		id, _ := res["window_id"].(string)
		if id == "" {
			t.Fatalf("new-window %s returned no window_id: %v", name, res)
		}
		return id
	}
	during := open("during", true, "echo ready; sleep 2; exit 0")
	after := open("after", true, "echo ready; read x; exit 3")
	plain := open("plain", false, "echo ready; read x; exit 4")
	for _, w := range []string{during, after, plain} {
		waitHandoffPane(t, base, sess, w, regexp.MustCompile(`^ready`), 10*time.Second)
	}

	res := handOver(t, base, map[string]any{"hold_ms": 3500})
	carried, _ := res["panes"].(float64)
	duringClosed := waitWindowGone(t, base, sess, during, 10*time.Second)
	afterListed := windowListed(t, base, sess, after)
	plainListed := windowListed(t, base, sess, plain)

	for _, w := range []string{after, plain} {
		_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": w, "text": "go\n"})
	}
	plainCode := windowExitCode(t, base, sess, plain, 10*time.Second)
	afterClosed := waitWindowGone(t, base, sess, after, 10*time.Second)
	time.Sleep(500 * time.Millisecond)
	plainStays := windowListed(t, base, sess, plain)

	writeHandoffArtifact(t, map[string]any{
		"panes_carried": int(carried), "during_closed": duringClosed,
		"after_listed_after_handoff": afterListed, "plain_listed_after_handoff": plainListed,
		"after_closed": afterClosed, "plain_exit": plainCode, "plain_stays": plainStays,
	})
	if int(carried) != 4 {
		t.Fatalf("ASSERTION: the handoff carried %d panes, want 4: the during window exited before the manifest", int(carried))
	}
	if !afterListed || !plainListed {
		t.Fatalf("ASSERTION: a window whose program still ran was gone after the handoff (after %v, plain %v)", afterListed, plainListed)
	}
	if !duringClosed {
		t.Errorf("ASSERTION: the close_on_exit window whose program exited during the handoff stayed open")
	}
	if !afterClosed {
		t.Errorf("ASSERTION: the close_on_exit window whose program exited after the handoff stayed open")
	}
	if plainCode != 4 || !plainStays {
		t.Errorf("ASSERTION: the window without close_on_exit exited %d and is listed %v, want 4 and listed", plainCode, plainStays)
	}
}

// TestSelfExecKeepsWorkspaceReturn hands over two sessions shaped as tuios
// xpanes leaves one: a close_on_exit pane alone on workspace 5, which the
// session shows. When it exits after the handoff, the workspace empties and
// the session must go back where it came from.
//
//   - "ret" came to 5 with select-workspace return_to 3 from workspace 1, as
//     xpanes does: it must show 3. Without the record it walks the trail,
//     which ends at 1.
//   - "trail" came to 5 from 3, which it came to from 1, with no return_to:
//     it must show 3, the newest workspace on its trail with panes. Without
//     the trail it shows the lowest workspace with panes, 1.
func TestSelfExecKeepsWorkspaceReturn(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	jobs := map[string]string{}
	for _, sess := range []string{"ret", "trail"} {
		newCommandWindow(t, base, sess, "home", false, "sh", "-c", "exec sleep 600")
		mustCall(t, base, "new-window", map[string]any{"session": sess, "name": "three", "workspace": 3, "focus": false, "command": []string{"sh", "-c", "exec sleep 600"}})
		res := mustCall(t, base, "new-window", map[string]any{
			"session": sess, "name": "job", "workspace": 5, "focus": false, "close_on_exit": true,
			"command": []string{"sh", "-c", "echo ready; read x; exit 0"},
		})
		jobs[sess], _ = res["window_id"].(string)
		waitHandoffPane(t, base, sess, jobs[sess], regexp.MustCompile(`^ready`), 10*time.Second)
	}
	mustCall(t, base, "select-workspace", map[string]any{"session": "ret", "workspace": 5, "return_to": 3})
	mustCall(t, base, "select-workspace", map[string]any{"session": "trail", "workspace": 3})
	mustCall(t, base, "select-workspace", map[string]any{"session": "trail", "workspace": 5})
	for _, sess := range []string{"ret", "trail"} {
		if ws := currentWorkspace(t, base, sess); ws != 5 {
			t.Fatalf("session %s shows workspace %d before the handoff, want 5", sess, ws)
		}
	}

	handOver(t, base, map[string]any{})
	got := map[string]int{}
	for _, sess := range []string{"ret", "trail"} {
		if !windowListed(t, base, sess, jobs[sess]) {
			t.Fatalf("ASSERTION: the job pane of %s was gone after the handoff", sess)
		}
		_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": jobs[sess], "text": "go\n"})
		if !waitWindowGone(t, base, sess, jobs[sess], 10*time.Second) {
			t.Fatalf("ASSERTION: the job pane of %s did not close when its program exited", sess)
		}
		got[sess] = currentWorkspace(t, base, sess)
	}
	writeHandoffArtifact(t, map[string]any{"shows_after_the_job": got})
	for sess, ws := range got {
		if ws != 3 {
			t.Errorf("ASSERTION: session %s shows workspace %d after its job ended, want 3", sess, ws)
		}
	}
}

// TestSelfExecKeepsSessionOrder hands over five sessions made one after
// another, the second used last. Every listing of sessions (the rail, the
// switcher, tuios ls, herdr) is in order of creation, and a call that names
// no session acts on the one active last. Both must be the same after the
// handoff, and so must each session's created and last active times as
// list-sessions reports them, to the second.
//
// How it could pass wrongly: the image adopts the sessions in the daemon's
// map order, which for a few sessions is often the order they were made in,
// so an image that stamped each with its own time could still list them
// right. The times cannot: the handoff runs more than a second after the
// last session was touched, so a session stamped at the handoff reports a
// later second. Sessions stamped in the same instant list by name, so the
// names also run against the order they were made in.
func TestSelfExecKeepsSessionOrder(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	names := []string{"e2e-ord-e", "e2e-ord-d", "e2e-ord-c", "e2e-ord-b", "e2e-ord-a"}
	for _, name := range names {
		newCommandWindow(t, base, name, "home", false, "sh", "-c", "exec sleep 600")
		time.Sleep(20 * time.Millisecond)
	}
	// A new pane is activity in its session.
	newCommandWindow(t, base, names[1], "used", false, "sh", "-c", "exec sleep 600")
	type listedSession struct {
		Name       string `json:"name"`
		Created    int64  `json:"created"`
		LastActive int64  `json:"last_active"`
	}
	listed := func() []listedSession {
		res := mustCall(t, base, "list-sessions", map[string]any{})
		raw, _ := json.Marshal(res["sessions"])
		var list []listedSession
		_ = json.Unmarshal(raw, &list)
		return list
	}
	order := func(list []listedSession) string {
		var out []string
		for _, s := range list {
			out = append(out, s.Name)
		}
		return strings.Join(out, ",")
	}
	current := func() string {
		name, _ := mustCall(t, base, "session-info", map[string]any{})["session_name"].(string)
		return name
	}
	before, currentBefore := listed(), current()
	time.Sleep(1100 * time.Millisecond)

	handOver(t, base, map[string]any{})
	after, currentAfter := listed(), current()

	writeHandoffArtifact(t, map[string]any{
		"before": before, "after": after,
		"session_info_before": currentBefore, "session_info_after": currentAfter,
	})
	if order(before) != strings.Join(names, ",") || currentBefore != names[1] {
		t.Fatalf("before the handoff the sessions list as %s and session-info names %q", order(before), currentBefore)
	}
	if order(after) != order(before) {
		t.Errorf("ASSERTION: the sessions list as %s after the handoff, and as %s before", order(after), order(before))
	}
	if currentAfter != currentBefore {
		t.Errorf("ASSERTION: a call naming no session acts on %q after the handoff, and on %q before", currentAfter, currentBefore)
	}
	times := func(list []listedSession) map[string][2]int64 {
		out := map[string][2]int64{}
		for _, s := range list {
			out[s.Name] = [2]int64{s.Created, s.LastActive}
		}
		return out
	}
	was, is := times(before), times(after)
	for _, name := range names {
		if was[name][0] != is[name][0] {
			t.Errorf("ASSERTION: session %s reports it was created at %d after the handoff, and at %d before", name, is[name][0], was[name][0])
		}
		if was[name][1] != is[name][1] {
			t.Errorf("ASSERTION: session %s reports it was last active at %d after the handoff, and at %d before", name, is[name][1], was[name][1])
		}
	}
}

// TestSelfExecKeepsPluginPane opens a herdr plugin's split pane, and after
// the handoff asks plugin.pane.focus for it, which answers only for a pane
// the daemon knows a plugin opened, and then ends the pane's program, which
// must close the window as it did before.
//
// The positive half is the same plugin.pane.focus before the handoff.
func TestSelfExecKeepsPluginPane(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, pluginsConfig(t, []string{"e2e.panes"}, "panes"))
	startHandoffDaemon(t, base)
	const sess = "e2e-plugin"
	newCommandWindow(t, base, sess, "anchor", false, "sh", "-c", "exec sleep 600")
	opened := herdrCall(t, base, "plugin.pane.open", map[string]any{"plugin_id": "e2e.panes", "entrypoint": "side"})
	paneID, _ := dig(opened, "plugin_pane", "pane", "pane_id").(string)
	if opened["type"] != "plugin_pane_opened" || paneID == "" {
		t.Fatalf("plugin.pane.open answered %v", opened)
	}
	var win string
	deadline := time.Now().Add(10 * time.Second)
	for win == "" && time.Now().Before(deadline) {
		out, _ := tuiosOut(base, "list-windows", "-s", sess, "--json")
		var list struct {
			Windows []struct {
				ID   string `json:"window_id"`
				Name string `json:"display_name"`
				PID  int    `json:"pid"`
			} `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out), &list)
		for _, w := range list.Windows {
			if w.Name == "E2E split" && w.PID > 0 {
				win = w.ID
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if win == "" {
		t.Fatal("the plugin's pane never listed a process")
	}
	waitHandoffPane(t, base, sess, win, regexp.MustCompile(`PLUGIN-PANE-READY side`), 10*time.Second)
	focusBefore := herdrCall(t, base, "plugin.pane.focus", map[string]any{"pane_id": paneID})

	handOver(t, base, map[string]any{})
	focusAfter := handoffHerdrTry(t, base, "plugin.pane.focus", map[string]any{"pane_id": paneID})
	listed := windowListed(t, base, sess, win)
	pid := panePIDs(t, base, sess)[win]
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	closed := waitWindowGone(t, base, sess, win, 10*time.Second)

	writeHandoffArtifact(t, map[string]any{
		"pane_id": paneID, "focus_before": focusBefore, "focus_after": focusAfter,
		"listed_after_handoff": listed, "pid": pid, "closed_on_exit": closed,
	})
	if focusBefore["type"] != "plugin_pane_focused" {
		t.Fatalf("plugin.pane.focus before the handoff answered %v", focusBefore)
	}
	if focusAfter["type"] != "plugin_pane_focused" {
		t.Errorf("ASSERTION: after the handoff plugin.pane.focus answered %v, want plugin_pane_focused", focusAfter)
	}
	if !listed || pid == 0 {
		t.Fatalf("ASSERTION: the plugin's pane was gone after the handoff (listed %v, pid %d)", listed, pid)
	}
	if !closed {
		t.Errorf("ASSERTION: the plugin's pane stayed open after its program ended")
	}
}

// handoffHerdrTry is herdrCall that returns herdr's error as the answer rather than
// failing the test.
func handoffHerdrTry(t *testing.T, base, method string, params any) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", herdrSocket(base))
	if err != nil {
		t.Fatalf("dial the herdr socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	line, _ := json.Marshal(map[string]any{"id": "e2e", "method": method, "params": params})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal(reply, &out); err != nil {
		t.Fatalf("%s: %q: %v", method, reply, err)
	}
	if e, ok := out["error"].(map[string]any); ok {
		return e
	}
	res, _ := out["result"].(map[string]any)
	return res
}

// TestSelfExecKeepsProtocolPane runs an ACP agent headless with start-agent
// --protocol acp, and checks list-agents names the pane's protocol after the
// handoff as it did before. The protocol is the daemon's mark on the window,
// the one request-approval reads to let the pane hold a permission.
func TestSelfExecKeepsProtocolPane(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	fake := buildFakeACP(t)
	if out, err := handoffCLI(t, base, 60*time.Second, "start-agent", fake, "--protocol", "acp", "-s", "e2e-proto", "--name", "helper", "--prompt", "hello there"); err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	protocol := func() string {
		out, err := tuiosCLI(t, base, "list-agents", "-s", "e2e-proto", "--json")
		if err != nil {
			t.Fatalf("list-agents: %v\n%s", err, out)
		}
		var listing struct {
			Agents []struct {
				Name     string `json:"name"`
				Protocol string `json:"protocol"`
			} `json:"agents"`
		}
		if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Agents) != 1 {
			t.Fatalf("list-agents: %v\n%s", err, out)
		}
		return listing.Agents[0].Protocol
	}
	before := protocol()
	handOver(t, base, map[string]any{})
	after := protocol()

	writeHandoffArtifact(t, map[string]any{"protocol_before": before, "protocol_after": after})
	if before != "acp" {
		t.Fatalf("list-agents names protocol %q before the handoff, want acp", before)
	}
	if after != "acp" {
		t.Errorf("ASSERTION: list-agents names protocol %q after the handoff, want acp", after)
	}
}

// TestSelfExecKeepsPaneCreator runs with the agent features off, where a
// pane may type only into a pane it opened, or one whose shell is at its
// prompt. Pane A opens B, and B's shell runs cat, so it is not at its prompt.
// After the handoff A types into B, and B must take it. The control is C,
// which the test opened and whose shell also runs cat: A is refused there,
// before the handoff and after, so the rule is in force.
func TestSelfExecKeepsPaneCreator(t *testing.T) {
	base := t.TempDir()
	writeAgentsConfig(t, base, true)
	startHandoffDaemon(t, base)
	const sess = "e2e-creator"
	a := newCommandWindow(t, base, sess, "A", false, "sh", "-c",
		`T="$1"; echo ready; while read -r l; do eval "$l"; done`, "A", tuiosBin)
	waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^ready`), 10*time.Second)
	cRes := mustCall(t, base, "new-window", map[string]any{"session": sess, "name": "C", "focus": false})
	c, _ := cRes["window_id"].(string)
	run := func(n int, line string) string {
		t.Helper()
		_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": a, "text": line + `; echo "RC-` + strconv.Itoa(n) + `-$?"` + "\n"})
		return waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^RC-`+strconv.Itoa(n)+`-(\d+)`), 20*time.Second)[1]
	}
	run(1, `"$T" call new-window '{"session":"`+sess+`","name":"B","focus":false}' >/dev/null`)
	var b string
	for id, pid := range panePIDs(t, base, sess) {
		if id != a && id != c && pid > 0 {
			b = id
		}
	}
	if b == "" {
		t.Fatal("pane A opened no window")
	}
	// cat runs as a job of each shell, in a process group of its own, so the
	// shell is not at its prompt. The test types it: a caller outside every
	// pane is not held to the rule.
	for _, w := range []string{b, c} {
		_ = mustCall(t, base, "send-text", map[string]any{"session": sess, "window": w, "text": "echo cat-$((6*7)); cat\n"})
		waitHandoffPane(t, base, sess, w, regexp.MustCompile(`^cat-42`), 10*time.Second)
	}
	time.Sleep(300 * time.Millisecond)
	typeInto := func(n int, window, mark string) string {
		return run(n, `"$T" send-text -s `+sess+` -w `+window+` `+mark)
	}
	bBefore := typeInto(2, b, "MARK-B-BEFORE")
	cBefore := typeInto(3, c, "MARK-C-BEFORE")

	handOver(t, base, map[string]any{})
	bAfter := typeInto(4, b, "MARK-B-AFTER")
	cAfter := typeInto(5, c, "MARK-C-AFTER")
	bTook := false
	deadline := time.Now().Add(10 * time.Second)
	for !bTook && time.Now().Before(deadline) {
		bTook = strings.Contains(strings.Join(mustScrollback(t, base, sess, b), "\n"), "MARK-B-AFTER")
		time.Sleep(100 * time.Millisecond)
	}

	writeHandoffArtifact(t, map[string]any{
		"b_before": bBefore, "c_before": cBefore, "b_after": bAfter, "c_after": cAfter, "b_took_it": bTook,
	})
	if bBefore != "0" || cBefore == "0" {
		t.Fatalf("before the handoff A typing into its own B exited %s and into C %s, want 0 and a refusal", bBefore, cBefore)
	}
	if bAfter != "0" || !bTook {
		t.Errorf("ASSERTION: after the handoff A typing into the pane it opened exited %s (B shows it: %v), want 0", bAfter, bTook)
	}
	if cAfter == "0" {
		t.Errorf("ASSERTION: after the handoff A typed into C, which it did not open")
	}
}

// TestSelfExecFollowsMovedWindow moves a window from alpha to beta, and after
// the handoff makes the call an agent's hook in that pane makes: one that
// names alpha, the session in its TUIOS_SESSION, and the window. It must
// reach the window in beta, as it did before the handoff.
func TestSelfExecFollowsMovedWindow(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	newCommandWindow(t, base, "alpha", "home", false, "sh", "-c", "exec sleep 600")
	newCommandWindow(t, base, "beta", "home", false, "sh", "-c", "exec sleep 600")
	win := newCommandWindow(t, base, "alpha", "mover", false, "sh", "-c", "echo ready; exec sleep 600")
	waitHandoffPane(t, base, "alpha", win, regexp.MustCompile(`^ready`), 10*time.Second)
	mustCall(t, base, "move-window", map[string]any{"session": "alpha", "window": win, "to_session": "beta"})
	_, errBefore := callVerb(t, base, "set-agent-state", map[string]any{"session": "alpha", "window": win, "state": "working"})

	handOver(t, base, map[string]any{})
	_, errAfter := callVerb(t, base, "set-agent-state", map[string]any{"session": "alpha", "window": win, "state": "done"})
	state := readPSState(t, base, "beta", win).State

	writeHandoffArtifact(t, map[string]any{"before": fmt.Sprint(errBefore), "after": fmt.Sprint(errAfter), "state_in_beta": state})
	if errBefore != nil {
		t.Fatalf("before the handoff a call naming alpha did not reach the moved window: %v", errBefore)
	}
	if errAfter != nil || state != "done" {
		t.Errorf("ASSERTION: after the handoff a call naming alpha did not reach the moved window: %v, beta reads %q", errAfter, state)
	}
}

// TestSelfExecKeepsReportColors attaches a client with a theme to a daemon
// session, behind the light stand-in host, so the client pushes the theme's
// colours for the panes to be told. The client then leaves, and the panes
// are handed over with no client attached. A program asking OSC 11, 10 and
// 4 after the handoff must be told the theme's colours still, not the
// emulator's own black and white: nothing pushes them again until a client
// attaches.
func TestSelfExecKeepsReportColors(t *testing.T) {
	base := t.TempDir()
	host := buildHostTerm(t)
	const themeID = "dracula"
	pal, fg, bg, _, ok := theme.Colors(themeID)
	if !ok {
		t.Fatalf("theme %s is not built in", themeID)
	}
	xrgb := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("rgb:%04x/%04x/%04x", (r>>8)*0x101, (g>>8)*0x101, (b>>8)*0x101)
	}
	useShippedLooks(base)
	writeConfig(t, base, "[appearance]\ntheme = \""+themeID+"\"\n")
	startHandoffDaemon(t, base)
	const sess = "e2e-colors"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{
		args:         []string{"attach", sess},
		shippedLooks: true,
		env:          []string{"COLORTERM=truecolor"},
		wrap:         []string{host, "run", "-bg", "#fdf6e3", "-fg", "#2a2a2a", "-ansi", hostANSISpec(), "--"},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	want := map[string]string{"11": xrgb(bg), "10": xrgb(fg), "4;1": xrgb(pal[1])}
	before := askPane(t, base, sess, host, want)
	_ = term.Close()
	deadline := time.Now().Add(10 * time.Second)
	for sessionAttached(t, base, sess) {
		if time.Now().After(deadline) {
			t.Fatal("the client stayed attached after it closed")
		}
		time.Sleep(100 * time.Millisecond)
	}

	handOver(t, base, map[string]any{})
	after := askPane(t, base, sess, host, want)

	writeHandoffArtifact(t, map[string]any{"want": want, "before": before, "after": after})
	for spec, w := range want {
		if before[spec] != w {
			t.Fatalf("before the handoff a pane's OSC %s query was answered %q, want the theme's %q", spec, before[spec], w)
		}
	}
	for spec, w := range want {
		if after[spec] != w {
			t.Errorf("ASSERTION: after the handoff a pane's OSC %s query was answered %q, want the theme's %q", spec, after[spec], w)
		}
	}
}

// sessionAttached reports whether a client shows session.
func sessionAttached(t *testing.T, base, session string) bool {
	t.Helper()
	res := mustCall(t, base, "list-sessions", map[string]any{})
	raw, _ := json.Marshal(res["sessions"])
	var list []struct {
		Name     string `json:"name"`
		Attached bool   `json:"attached"`
	}
	_ = json.Unmarshal(raw, &list)
	for _, s := range list {
		if s.Name == session {
			return s.Attached
		}
	}
	return false
}

// TestDaemonUpgradeAcrossHandoffVersions needs a tuios that writes the
// previous handoff version, named by TUIOS_E2E_HANDOFF_PREV_BIN: a build of
// main before HandoffVersion moved, so its manifest has every field main
// added at that version. It checks the three directions a version change
// opens, and the task state an older manifest carries:
//
//   - older to newer: a daemon of the older build hands its panes to this
//     one, which reads the older manifest, keeps every pane, and runs;
//   - newer to older: this build's daemon asks the older one to take over,
//     and the preflight refuses with nothing changed;
//   - the fallback: the older daemon hands over to this build, which fails
//     to adopt on purpose, and the older binary, kept as tuios.prev, adopts
//     the manifest it wrote;
//   - task holds and dispatch settings: a pane of the older build holds a
//     card on another machine (far_holds in its manifest), and this build
//     lets the card go there when the pane closes after the upgrade.
func TestDaemonUpgradeAcrossHandoffVersions(t *testing.T) {
	prev := os.Getenv("TUIOS_E2E_HANDOFF_PREV_BIN")
	if prev == "" {
		t.Skip("TUIOS_E2E_HANDOFF_PREV_BIN is not set: build tuios from the commit before the handoff version moved and name it there")
	}
	startPrev := func(t *testing.T, base string, env ...string) {
		t.Helper()
		killDaemon(t, base)
		if out, err := tuiosCLIBinEnv(t, prev, base, append([]string{"TUIOS_TEST_HANDOFF=1"}, env...), "start-server"); err != nil {
			t.Fatalf("start-server with %s: %v\n%s", prev, err, out)
		}
	}
	results := map[string]any{}

	t.Run("older to newer and back", func(t *testing.T) {
		base := t.TempDir()
		startPrev(t, base)
		const sess = "e2e-versions"
		a := newCommandWindow(t, base, sess, "a", false, "sh", "-c", `echo pid-$$; i=0; while :; do i=$((i+1)); echo v-$i; sleep 0.02; done`)
		waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
		pidsBefore := panePIDs(t, base, sess)
		older := helloImage(t, base)
		mustCall(t, base, "test-handoff", map[string]any{"binary": tuiosBin})
		newer := awaitNewImage(t, base, older, 20*time.Second)
		hello := mustCall(t, base, "hello", map[string]any{})
		pidsAfter := panePIDs(t, base, sess)
		time.Sleep(300 * time.Millisecond)
		ticks := counterLines(mustScrollback(t, base, sess, a), "v-")

		_, refusal := callVerb(t, base, "test-handoff", map[string]any{"binary": prev})
		kept := helloImage(t, base)
		results["older_to_newer"] = map[string]any{
			"pid_before": older.PID, "pid_after": newer.PID, "panes_before": pidsBefore, "panes_after": pidsAfter,
			"tick_gap_at": firstGap(ticks), "handoff_after": hello["handoff"], "newer_to_older": errString(refusal),
			"instance_kept_on_refusal": kept.Instance == newer.Instance,
		}
		if newer.PID != older.PID {
			t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", older.PID, newer.PID)
		}
		for id, pid := range pidsBefore {
			if pidsAfter[id] != pid {
				t.Errorf("ASSERTION: window %s had pid %d, and %d after the upgrade", id, pid, pidsAfter[id])
			}
		}
		if i := firstGap(ticks); i >= 0 {
			t.Errorf("ASSERTION: the counter jumps from %d to %d", ticks[i-1], ticks[i])
		}
		if refusal == nil || !strings.Contains(refusal.Error(), "cannot read this daemon's state") || kept.Instance != newer.Instance {
			t.Errorf("ASSERTION: a hand-over to the older build was not refused by the preflight with nothing changed: %v", refusal)
		}
	})

	t.Run("fallback to the older binary", func(t *testing.T) {
		base := t.TempDir()
		startPrev(t, base, "TUIOS_TEST_HANDOFF_FAIL=error")
		const sess = "e2e-versions-fb"
		a := newCommandWindow(t, base, sess, "a", false, "sh", "-c", `echo pid-$$; exec sleep 600`)
		waitHandoffPane(t, base, sess, a, regexp.MustCompile(`^pid-\d+`), 10*time.Second)
		pidsBefore := panePIDs(t, base, sess)
		older := helloImage(t, base)
		mustCall(t, base, "test-handoff", map[string]any{"binary": tuiosBin})
		after := awaitNewImage(t, base, older, 40*time.Second)
		stats := handoffStats(t, base)
		exe := executableOf(t, after.PID)
		pidsAfter := panePIDs(t, base, sess)
		results["fallback"] = map[string]any{"pid_before": older.PID, "pid_after": after.PID, "executable_after": exe, "stats": stats, "panes_before": pidsBefore, "panes_after": pidsAfter}
		if after.PID != older.PID {
			t.Fatalf("ASSERTION: the daemon's pid changed from %d to %d", older.PID, after.PID)
		}
		if fb, _ := stats["fallback"].(bool); !fb || !strings.HasSuffix(exe, "tuios.prev") {
			t.Errorf("ASSERTION: the older binary did not take the panes back: runs %s, stats %v", exe, stats)
		}
		for id, pid := range pidsBefore {
			if pidsAfter[id] != pid {
				t.Errorf("ASSERTION: window %s had pid %d, and %d after the fallback", id, pid, pidsAfter[id])
			}
		}
	})
	// The task state main keeps beside a v3 manifest: a pane of the older
	// build holds a card on another machine (the manifest's far_holds), and
	// the older daemon has dispatch settings of its own. This build adopts
	// both: the card is let go on home when the pane closes, and the settings
	// read the same.
	t.Run("task holds and dispatch settings from the older build", func(t *testing.T) {
		pp := startPoolOn(t, 2, true, prev)
		t.Cleanup(func() { dumpDaemonLogOnFail(t, pp.base); dumpDaemonLogOnFail(t, pp.remote) })
		const proj = "https://example.com/acme/api.git"
		mustCLI(t, pp.base, "task", "add", "-s", "home", "--project", proj, "-p", "Retry on a 503.", "Fix the retry") // t1
		mustCLI(t, pp.base, "task", "dispatch", "mode", "auto", "--agent", "claude", "--machine", "build", "--project", proj, "-s", "home")
		cards, _ := waitAutoCards(t, pp.base, "home", "auto did not start t1 on build",
			func(c map[string]autoCard) bool { return held(c, "t1") && c["t1"].Holder.Host != "" })
		window := cards["t1"].Holder.Window
		untilTrue(t, 30*time.Second, "the agent of t1 never came up on build", func() bool {
			return claudeAgents(t, mustCLI(t, pp.remote, "list-agents", "--all-sessions", "--json")) == 1
		})
		mustCLI(t, pp.remote, "task", "dispatch", "quiet", "22:00-07:00")
		mustCLI(t, pp.remote, "task", "dispatch", "pause")
		settings, _ := dispatchStatus(t, pp.remote)

		older := helloImage(t, pp.remote)
		res, out, err := daemonUpgrade(t, pp.remote, copyTuios(t))
		if err != nil {
			t.Fatalf("ASSERTION: daemon upgrade on build from the older build: %v\n%s", err, out)
		}
		newer := helloImage(t, pp.remote)
		hello := mustCall(t, pp.remote, "hello", map[string]any{})
		got, raw := dispatchStatus(t, pp.remote)
		_, heldRaw := autoCards(t, pp.base, "home")
		entry := map[string]any{"pid_before": older.PID, "pid_after": newer.PID, "upgrade": res, "handoff_after": hello["handoff"],
			"dispatch_after": raw, "home_cards_after_upgrade": heldRaw}
		results["far_hold"] = entry
		if newer.PID != older.PID || newer.Instance == older.Instance || res["fallback"] != false {
			t.Fatalf("ASSERTION: build's upgrade from the older build was not a live one: %v", res)
		}
		if !settings.Paused || settings.Quiet != "22:00-07:00" {
			t.Fatalf("ASSERTION: build's dispatch settings were not set before the upgrade: paused %v, quiet %q", settings.Paused, settings.Quiet)
		}
		if got.Paused != settings.Paused || got.Quiet != settings.Quiet {
			t.Errorf("ASSERTION: after the upgrade build's dispatch settings read paused %v, quiet %q; want paused %v, quiet %q",
				got.Paused, got.Quiet, settings.Paused, settings.Quiet)
		}
		if cards, raw := autoCards(t, pp.base, "home"); !held(cards, "t1") {
			t.Fatalf("ASSERTION: t1 is not held right after build's upgrade:\n%s", raw)
		}

		ls := ansiCSI.ReplaceAllString(mustCLI(t, pp.remote, "ls"), "")
		sess := regexp.MustCompile(`[a-z0-9-]*task-fix-the-retry[a-z0-9-]*`).FindString(ls)
		if sess == "" {
			t.Fatalf("no session on build holds t1's agent:\n%s", ls)
		}
		mustCLI(t, pp.remote, "close-window", "-s", sess, window)
		_, raw = waitAutoCards(t, pp.base, "home", "t1 on home was not let go when its pane on build closed after build's upgrade from the older build",
			func(c map[string]autoCard) bool { return !held(c, "t1") })
		entry["home_cards_after_close"] = raw
	})

	writeHandoffArtifact(t, results)
}
