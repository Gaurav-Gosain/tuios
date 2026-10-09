package tuie2e

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The review fixes to stream-pane and attach-presence: the nonce scope of a
// presence, a lease that a silent client stops renewing, lease frames behind
// input that a pane does not read, and the snapshot charged to the read
// budget.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "stream-pane review
// fixes"):
//   - with humanNonceFor not checking covers, a presence made for one
//     session answers in another, and TestPresenceNonceScope fails at "a
//     presence for session phone answered in session other".
//   - with the expireLease call left out of paneStream.stream, the silent
//     stream holds its lease, and TestStreamPaneLeaseExpires fails at "the
//     lease of a silent client did not end".
//   - with I frames written to the pane on the goroutine that reads the
//     client, TestStreamPaneLeaseBehindBlockedInput fails at "a lease frame
//     behind input the pane does not read".
//   - with the read budget charge left out of snapshotSubscribe,
//     TestStreamPaneSnapshotCharged fails at "a second snapshot was taken
//     while the first held the budget".

// otherSession is a second session, for the nonce scope.
const otherSession = "other"

// TestPresenceNonceScope: a presence made for one session cannot answer an
// approval or respond in another session. A presence made with no session
// can. The approval is the real Claude Code hook's, held in session other.
func TestPresenceNonceScope(t *testing.T) {
	base, link, ctl, _, _ := limitsDaemon(t)
	if out, err := tuiosCLI(t, base, "new", otherSession, "--detach"); err != nil {
		t.Fatalf("create the second session: %v\n%s", err, out)
	}
	res, verr := ctl.call(t, "list-windows", map[string]any{"session": otherSession})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	ow := res["windows"].([]any)[0].(map[string]any)["window_id"].(string)

	scoped, verr := ctl.call(t, "attach-presence", map[string]any{"session": streamSession})
	if verr != nil {
		t.Fatalf("attach-presence for %s: %v", streamSession, verr)
	}
	anyConn := link.open(t, true)
	unscoped, verr := anyConn.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence with no session: %v", verr)
	}

	hook := startSessionApprovalHook(t, base, otherSession, ow)
	id := sessionApproval(t, ctl, otherSession, ow)

	// reply-approval in the other session, with the scoped nonce.
	if r, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "once", "human_nonce": scoped["human_nonce"]}); verr == nil || verr.Code != "not_human" {
		t.Fatalf("a presence for session %s answered in session %s: reply-approval %v %v", streamSession, otherSession, r, verr)
	}
	// respond in the other session, with the scoped nonce. The nonce is
	// checked before the prompt, so a nonce that passes gets another error.
	if r, verr := ctl.call(t, "respond", map[string]any{"session": otherSession, "window": ow, "action": "deny", "human_nonce": scoped["human_nonce"]}); verr == nil || verr.Code != "not_human" {
		t.Fatalf("a presence for session %s answered in session %s: respond %v %v", streamSession, otherSession, r, verr)
	}
	// The same scoped nonce still acts in its own session.
	if _, verr := ctl.call(t, "respond", map[string]any{"session": streamSession, "window": linkFirstWindow(t, ctl, streamSession), "action": "deny", "human_nonce": scoped["human_nonce"]}); verr != nil && verr.Code == "not_human" {
		t.Fatalf("a presence for session %s was refused in its own session: %v", streamSession, verr)
	}

	// The presence with no session answers both.
	if _, verr := anyConn.call(t, "respond", map[string]any{"session": otherSession, "window": ow, "action": "deny", "human_nonce": unscoped["human_nonce"]}); verr != nil && verr.Code == "not_human" {
		t.Fatalf("a presence with no session was refused by respond in session %s: %v", otherSession, verr)
	}
	r, verr := anyConn.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "once", "human_nonce": unscoped["human_nonce"]})
	if verr != nil || r["applied"] != true {
		t.Fatalf("a presence with no session did not answer the approval in session %s: %v %v", otherSession, r, verr)
	}
	if out := hook.wait(t); !strings.Contains(out, `"behavior":"allow"`) {
		t.Fatalf("the hook printed %q, want the allow decision", out)
	}
	writeReviewArtifact(t, "presence-nonce-scope.txt", fmt.Sprintf("scoped presence (%s): reply-approval and respond in %s refused not_human\nunscoped presence: respond passed the nonce check, reply-approval applied %v\nhook: %s\n", streamSession, otherSession, r["applied"], strings.TrimSpace(hook.out.String())))
}

// TestStreamPaneLeaseExpires: a client that stops sending L frames loses its
// lease after the documented 30 seconds, and the pane goes back to the size
// its clients asked for. A client that renews keeps its lease.
func TestStreamPaneLeaseExpires(t *testing.T) {
	_, link, ctl, w1, w2 := limitsDaemon(t)
	for _, w := range []string{w1, w2} {
		if _, verr := ctl.call(t, "resize", map[string]any{"session": streamSession, "window": w, "width": 80, "height": 24}); verr != nil {
			t.Fatalf("resize: %v", verr)
		}
	}
	silent := link.open(t, true)
	silent.streamPane(t, newSPTranscript(t), map[string]any{"session": streamSession, "window": w1, "lease_cols": 30, "lease_rows": 8})
	renew := link.open(t, true)
	renew.streamPane(t, newSPTranscript(t), map[string]any{"session": streamSession, "window": w2, "lease_cols": 31, "lease_rows": 9})
	start := time.Now()

	// Read the silent stream's frames while the renewing one sends an L
	// frame every 10 seconds.
	expired := make(chan time.Duration, 1)
	go func() {
		for {
			typ, payload, err := silent.frameWithin(45 * time.Second)
			if err != nil {
				return
			}
			if typ == 'E' && strings.Contains(string(payload), "lease_expired") {
				expired <- time.Since(start)
				return
			}
		}
	}()
	go func() {
		for {
			if _, _, err := renew.frameWithin(45 * time.Second); err != nil {
				return
			}
		}
	}()
	var took time.Duration
	deadline := time.After(40 * time.Second)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
wait:
	for {
		select {
		case took = <-expired:
			break wait
		case <-tick.C:
			renew.send(t, 'L', []byte{0, 31, 0, 9})
		case <-deadline:
			t.Fatalf("the lease of a silent client did not end within 40s")
		}
	}
	if took < 29*time.Second || took > 33*time.Second {
		t.Fatalf("the lease ended %s after the stream opened, want 30s", took.Round(time.Second))
	}
	spType(t, ctl, w1, "clear; echo EXP-$(stty size | tr ' ' x)\n")
	if !spShows(t, ctl, w1, "EXP-24x80") {
		t.Fatalf("the lease of a silent client did not end: the pane is not 80x24 again:\n%s", strings.Join(spLines(t, ctl, w1), "\n"))
	}
	spType(t, ctl, w2, "clear; echo REN-$(stty size | tr ' ' x)\n")
	if !spShows(t, ctl, w2, "REN-9x31") {
		t.Fatalf("a renewed lease ended:\n%s", strings.Join(spLines(t, ctl, w2), "\n"))
	}
	writeReviewArtifact(t, "stream-lease-expiry.txt", fmt.Sprintf("silent stream: lease_expired after %s, pane back at 80x24\nrenewing stream: pane held at 31x9 after %s\n", took.Round(100*time.Millisecond), time.Since(start).Round(time.Second)))
}

// TestStreamPaneLeaseBehindBlockedInput: input the pane does not read never
// holds up a lease frame, and input past the queue is refused as busy.
func TestStreamPaneLeaseBehindBlockedInput(t *testing.T) {
	_, link, ctl, w1, _ := limitsDaemon(t)
	if _, verr := ctl.call(t, "resize", map[string]any{"session": streamSession, "window": w1, "width": 80, "height": 24}); verr != nil {
		t.Fatalf("resize: %v", verr)
	}
	// A program that never reads its input, with the line discipline in raw
	// mode so the pane's input buffer fills.
	spType(t, ctl, w1, "stty raw -echo; echo NOREAD; sleep 600\n")
	spWait(t, ctl, w1, "NOREAD")
	ps := link.open(t, true)
	ps.streamPane(t, newSPTranscript(t), map[string]any{"session": streamSession, "window": w1})
	frames := make(chan [2]any, 1024)
	go func() {
		for {
			typ, payload, err := ps.frameWithin(60 * time.Second)
			if err != nil {
				close(frames)
				return
			}
			frames <- [2]any{typ, payload}
		}
	}()
	chunk := []byte(strings.Repeat("x", 60<<10))
	ps.send(t, 'I', chunk)
	ps.send(t, 'I', chunk)
	time.Sleep(500 * time.Millisecond)
	sent := time.Now()
	ps.send(t, 'L', []byte{0, 33, 0, 7})
	got := false
	timeout := time.After(5 * time.Second)
	for !got {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("the stream ended while waiting for the lease")
			}
			if f[0].(byte) == 'R' {
				p := f[1].([]byte)
				if binary.BigEndian.Uint16(p[8:10]) == 33 && binary.BigEndian.Uint16(p[10:12]) == 7 {
					got = true
				}
			}
		case <-timeout:
			t.Fatalf("a lease frame behind input the pane does not read was not applied within 5s")
		}
	}
	leaseTook := time.Since(sent)
	// Fill the queue: the next frames are refused as busy.
	busy := 0
	for range 8 {
		ps.send(t, 'I', chunk)
	}
	end := time.After(5 * time.Second)
	for busy == 0 {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("the stream ended while waiting for busy")
			}
			if f[0].(byte) == 'E' && frameCode(t, f[1].([]byte)) == "busy" {
				busy++
			}
		case <-end:
			t.Fatalf("input past the queue into a pane that does not read was never refused as busy")
		}
	}
	writeReviewArtifact(t, "stream-lease-behind-input.txt", fmt.Sprintf("lease frame after blocked input: R 33x7 after %s\ninput past the queue: E busy\n", leaseTook.Round(time.Millisecond)))
}

// TestStreamPaneSnapshotCharged: a snapshot is charged to the read budget
// until it is sent. While a client that does not read holds a snapshot of a
// pane as large as the budget, a second stream-pane is refused as busy, and
// once the first stream ends the next one gets its snapshot.
func TestStreamPaneSnapshotCharged(t *testing.T) {
	base, _, ctl, w1, _ := limitsDaemon(t)
	if _, verr := ctl.call(t, "resize", map[string]any{"session": streamSession, "window": w1, "width": 1200, "height": 100}); verr != nil {
		t.Fatalf("resize: %v", verr)
	}
	// Colour on every cell, so the snapshot is megabytes of VT bytes and
	// its write cannot finish into the buffers of a client that stopped
	// reading.
	spType(t, ctl, w1, `awk 'BEGIN{for(i=0;i<300;i++){s="";for(j=0;j<1200;j++)s=s "\033[3" (j%8) "mx";print s}; print "\033[0mFILLED"}'`+"\n")
	spWait(t, ctl, w1, "FILLED")

	slow := startPhoneLink(t, base)
	held := slow.open(t, true)
	slow.pause()
	req, _ := json.Marshal(map[string]any{"id": 1, "verb": "stream-pane", "params": map[string]any{"session": streamSession, "window": w1}})
	held.write(t, append(req, '\n'))
	time.Sleep(1500 * time.Millisecond)

	other := startPhoneLink(t, base)
	second := other.open(t, true)
	_, verr := second.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1})
	slow.resume()
	if verr == nil {
		t.Fatalf("a second snapshot was taken while the first held the budget")
	}
	if verr.Code != "busy" {
		t.Fatalf("a second stream-pane while the first snapshot held the budget: want busy, got %v", verr)
	}
	held.close()
	// After the first is sent or dropped, the budget is free again.
	deadline := time.Now().Add(20 * time.Second)
	for {
		third := other.open(t, true)
		res, verr := third.call(t, "stream-pane", map[string]any{"session": streamSession, "window": w1})
		if verr == nil && res["mode"] == "snapshot" {
			third.close()
			break
		}
		third.close()
		if time.Now().After(deadline) {
			t.Fatalf("the budget was not given back after the first snapshot: %v", verr)
		}
		time.Sleep(time.Second)
	}
	writeReviewArtifact(t, "stream-snapshot-charge.txt", fmt.Sprintf("pane 1200x100 with history: second stream-pane while the first was held: %s\nafter the first ended: snapshot\n", verr.Code))
}

// linkFirstWindow is the first window id of session.
func linkFirstWindow(t *testing.T, ctl *linkStream, session string) string {
	t.Helper()
	res, verr := ctl.call(t, "list-windows", map[string]any{"session": session})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	return res["windows"].([]any)[0].(map[string]any)["window_id"].(string)
}

// startSessionApprovalHook is startApprovalHook for a pane in session.
func startSessionApprovalHook(t *testing.T, base, session, window string) *approvalHook {
	t.Helper()
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", window)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"PermissionRequest","session_id":"e2e-scope","tool_name":"Bash","tool_input":{"command":"npm test"}}`)
	h := &approvalHook{done: make(chan error, 1)}
	cmd.Stdout = &h.out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the hook: %v", err)
	}
	go func() { h.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return h
}

// sessionApproval is spApproval for a pane in session.
func sessionApproval(t *testing.T, ctl *linkStream, session, window string) string {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		res, verr := ctl.call(t, "list-attention", map[string]any{"session": session})
		if verr != nil {
			t.Fatalf("list-attention: %v", verr)
		}
		items, _ := res["items"].([]any)
		for _, it := range items {
			m := it.(map[string]any)
			w, _ := m["window"].(string)
			if m["kind"] == "approval" && w != "" && strings.HasPrefix(window, w) && m["request_id"] != nil && m["request_id"] != "" {
				return m["request_id"].(string)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no held approval for %s showed in the Inbox", window)
	return ""
}

// writeReviewArtifact saves a short record of what a test saw under
// TUIOS_E2E_FRAMES, when it is set.
func writeReviewArtifact(t *testing.T, name, body string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("artifact dir: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Logf("write artifact: %v", err)
	}
}
