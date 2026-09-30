package session

import (
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestHerdrWorktreeOpenChecksTheCallerFirst: worktree.open runs git, so a
// pane that may not list worktrees is refused before git runs. It learns
// nothing of whether a branch exists: a branch that exists and one that
// does not both answer forbidden.
func TestHerdrWorktreeOpenChecksTheCallerFirst(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.manager.SetPanePermissions(config.ResolvedPermissions{Strict: true, Grants: []string{}})
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	repo := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("-c", "user.name=t", "-c", "user.email=t@t.invalid", "commit", "-q", "--allow-empty", "-m", "x")
	git("worktree", "add", "-q", "-b", "secret-branch", filepath.Join(t.TempDir(), "wt"))
	for _, branch := range []string{"secret-branch", "no-such-branch"} {
		if _, e := herdrCallAs(t, sp, "worktree.open", map[string]any{"cwd": repo, "branch": branch}); !strings.HasPrefix(e, "forbidden") {
			t.Errorf("worktree.open of %s from a pane with no grants answered %q, want forbidden", branch, e)
		}
	}
	if _, e := herdrCallAs(t, sp, "events.subscribe", map[string]any{"subscriptions": []any{
		map[string]any{"type": "pane.agent_status_changed", "pane_id": "w000000000000:p000000000000"},
	}}); !strings.HasPrefix(e, "forbidden") {
		t.Errorf("a subscription for an unknown pane from a pane with no grants answered %q, want forbidden", e)
	}
}

// herdrGoroutinesSettle waits until the goroutine count is below limit.
func herdrGoroutinesSettle(limit int, within time.Duration) int {
	deadline := time.Now().Add(within)
	for {
		n := runtime.NumGoroutine()
		if n < limit || time.Now().After(deadline) {
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestWaitsEndWithTheirClient: a wait whose client closes the connection
// ends, on the herdr socket and on the verb socket, instead of holding its
// goroutine and event subscription until a timeout an hour away. A timeout
// past 24 hours, or one that would overflow, is refused.
func TestWaitsEndWithTheirClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "w")
	w := sess.GetState().Windows[0].ID
	time.Sleep(200 * time.Millisecond)
	before := runtime.NumGoroutine()
	const n = 20
	for i := range 2 * n {
		var conn net.Conn
		var err error
		var line []byte
		if i%2 == 0 {
			conn, err = net.Dial("unix", HerdrSocketPath(sp))
			line, _ = json.Marshal(map[string]any{"id": "x", "method": "pane.wait_for_output", "params": map[string]any{
				"pane_id": herdrPaneID(sess.ID, w), "source": "recent",
				"match": map[string]any{"type": "substring", "value": "never-printed-zzz"}, "timeout_ms": 3_600_000}})
		} else {
			conn, err = net.Dial("unix", sp)
			line, _ = json.Marshal(map[string]any{"id": 1, "verb": "wait-for", "params": map[string]any{
				"session": "w", "window": w, "condition": "window-output", "pattern": "never-printed-zzz", "timeout": 3_600_000}})
		}
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write(append(line, '\n'))
		time.Sleep(10 * time.Millisecond)
		_ = conn.Close()
	}
	if after := herdrGoroutinesSettle(before+n/2, 5*time.Second); after >= before+n/2 {
		t.Errorf("%d goroutines before, %d after %d waits whose clients closed: the waits outlived their clients", before, after, 2*n)
	}
	for _, timeout := range []int64{86_400_001, 1 << 62} {
		start := time.Now()
		_, e := herdrCallAs(t, sp, "pane.wait_for_output", map[string]any{
			"pane_id": herdrPaneID(sess.ID, w), "source": "recent",
			"match": map[string]any{"type": "substring", "value": "never-printed-zzz"}, "timeout_ms": timeout})
		if !strings.HasPrefix(e, "invalid_params") || time.Since(start) > 5*time.Second {
			t.Errorf("timeout_ms %d answered %q after %v, want invalid_params at once", timeout, e, time.Since(start))
		}
	}
}

// TestHerdrConnectionsPerCallerAreCapped: one process holds at most
// herdrConnsPerCaller connections on the herdr socket at once.
func TestHerdrConnectionsPerCallerAreCapped(t *testing.T) {
	_, sp := startTestDaemon(t)
	var held []net.Conn
	defer func() {
		for _, c := range held {
			_ = c.Close()
		}
	}()
	for range herdrConnsPerCaller {
		c, err := net.Dial("unix", HerdrSocketPath(sp))
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	time.Sleep(300 * time.Millisecond)
	if code := herdrCode(herdrDial(t, sp, "ping", nil)); code != "rate_limited" {
		t.Errorf("connection %d answered %q, want rate_limited", herdrConnsPerCaller+1, code)
	}
	for _, c := range held {
		_ = c.Close()
	}
	held = nil
	deadline := time.Now().Add(5 * time.Second)
	for herdrCode(herdrDial(t, sp, "ping", nil)) != "" {
		if time.Now().After(deadline) {
			t.Fatal("the count did not come back down after the connections closed")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pasteProbePane is a window running cat -v after it turns bracketed paste
// on, so what reaches it shows on its screen, escapes visible.
func pasteProbePane(t *testing.T, d *Daemon, name string) (*Session, string) {
	t.Helper()
	sess, err := d.manager.CreateSession(name, &SessionConfig{}, 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Command: []string{"sh", "-c", `stty -echo; printf '\033[?2004h'; exec cat -v`}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for pty := sess.GetPTY(win.PTYID); pty == nil || !pty.BracketedPasteOn(); pty = sess.GetPTY(win.PTYID) {
		if time.Now().After(deadline) {
			t.Fatal("the pane never turned bracketed paste on")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sess, win.ID
}

// waitScreen waits for want on a pane's screen.
func waitScreen(t *testing.T, sess *Session, window string, want ...string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := sess.GetState()
		w, _ := findWindowState(st, window)
		text := ""
		if pty := sess.GetPTY(w.PTYID); pty != nil {
			text = pty.CaptureContent(true, false)
		}
		ok := true
		for _, s := range want {
			ok = ok && strings.Contains(text, s)
		}
		if ok {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %q:\n%s", want, text)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestHerdrAgentPromptSubmitsAPaste: agent.prompt types the prompt the way
// ask-agent does, one paste and then the harness's Enter, not raw text.
func TestHerdrAgentPromptSubmitsAPaste(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, w := pasteProbePane(t, d, "prompt")
	result(t, callP(dialVerb(t, sp), t, "set-agent-state", map[string]any{"session": "prompt", "window": w, "state": "idle", "harness": "claude-code"}))
	res := herdrOK(t, "agent.prompt", herdrDial(t, sp, "agent.prompt", map[string]any{"target": herdrPaneID(sess.ID, w), "text": "fix it\nthen test\n"}))
	if res["type"] != "agent_prompted" {
		t.Errorf("agent.prompt answered %v", res)
	}
	waitScreen(t, sess, w, "^[[200~fix it", "then test^[[201~")
}

// TestHerdrLeavesScratchTerminalsOut: a scratch terminal is not a herdr
// pane. It is not in the snapshot, its workspace is not a tab, its id finds
// nothing, and its events are not sent. It still reports its own agent.
func TestHerdrLeavesScratchTerminalsOut(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "sc")
	tr := d.newHerdrTranslator()
	scratch, err := sess.AddDaemonWindowWith(NewWindowOptions{Popup: true, Scratch: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := herdrOK(t, "snapshot", herdrDial(t, sp, "session.snapshot", nil))["snapshot"].(map[string]any)
	if n := len(snap["panes"].([]any)); n != 1 {
		t.Errorf("the snapshot lists %d panes, want 1: the scratch terminal is not a pane", n)
	}
	for _, tb := range snap["tabs"].([]any) {
		if tb.(map[string]any)["number"].(float64) >= herdrScratchWorkspaceBase {
			t.Errorf("a scratch workspace is a tab: %v", tb)
		}
	}
	if ws := snap["workspaces"].([]any)[0].(map[string]any); ws["pane_count"] != float64(1) {
		t.Errorf("pane_count %v, want 1", ws["pane_count"])
	}
	if code := herdrCode(herdrDial(t, sp, "pane.get", map[string]any{"pane_id": herdrPaneID(sess.ID, scratch.ID)})); code != "pane_not_found" {
		t.Errorf("pane.get of the scratch terminal answered %q", code)
	}
	for _, typ := range []string{EventWindowCreated, EventWindowFocused, EventAgentState} {
		if got := tr.translate(streamEvent{Type: typ, Session: "sc", Window: scratch.ID}); len(got) != 0 {
			t.Errorf("%s of the scratch terminal gave %+v", typ, got)
		}
	}
	d.setApprovalPeer(func(*connState) (bool, string) { return true, scratch.ID })
	if _, e := herdrCallAs(t, sp, "pane.report_agent", map[string]any{"pane_id": herdrPaneID(sess.ID, scratch.ID), "source": "crush", "agent": "crush", "state": "working", "seq": 1}); e != "" {
		t.Errorf("the scratch terminal's own report: %s", e)
	}
}
