package session

import (
	"fmt"
	"testing"
	"time"
)

// TestSubscribeResumeAfterReconnect drives the resume over the wire: a
// subscriber reads one event, disconnects, misses two, and reconnects with the
// seq and boot id it kept. It must get exactly the two it missed, then the
// next live event, and nothing twice.
func TestSubscribeResumeAfterReconnect(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "work")
	const sub = `{"id":1,"verb":"subscribe","params":{"session":"work","types":["window-created"]}}`

	first := dialVerb(t, sp)
	ack := result(t, first.call(t, sub))
	bootID, _ := ack["boot_id"].(string)
	if bootID == "" {
		t.Fatalf("subscribe ack has no boot_id: %v", ack)
	}
	if _, has := ack["replayed"]; has {
		t.Fatalf("a plain subscribe reported replayed: %v", ack)
	}
	if _, err := sess.AddDaemonWindow("a", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	ev := first.readResp(t)
	if ev["boot_id"] != bootID {
		t.Fatalf("event boot_id = %v, want %q", ev["boot_id"], bootID)
	}
	lastSeq := uint64(ev["seq"].(float64))
	_ = first.conn.Close()

	for _, name := range []string{"b", "c"} {
		if _, err := sess.AddDaemonWindow(name, nil); err != nil {
			t.Fatalf("AddDaemonWindow: %v", err)
		}
	}

	second := dialVerb(t, sp)
	ack = result(t, second.call(t, fmt.Sprintf(
		`{"id":1,"verb":"subscribe","params":{"session":"work","types":["window-created"],"after_seq":%d,"boot_id":%q}}`,
		lastSeq, bootID)))
	if ack["replayed"] != float64(2) {
		t.Fatalf("ack replayed = %v, want 2", ack["replayed"])
	}
	baseline := uint64(ack["seq"].(float64))

	seen := map[uint64]bool{}
	prev := lastSeq
	for i := range 2 {
		ev := second.readResp(t)
		if ev["type"] != EventWindowCreated {
			t.Fatalf("replayed line %d = %v, want window-created", i, ev)
		}
		seq := uint64(ev["seq"].(float64))
		if seq <= prev || seq > baseline {
			t.Fatalf("replayed seq %d out of order (prev %d, baseline %d)", seq, prev, baseline)
		}
		seen[seq] = true
		prev = seq
	}

	if _, err := sess.AddDaemonWindow("d", nil); err != nil {
		t.Fatalf("AddDaemonWindow: %v", err)
	}
	live := second.readResp(t)
	liveSeq := uint64(live["seq"].(float64))
	if live["type"] != EventWindowCreated || liveSeq <= baseline || seen[liveSeq] {
		t.Fatalf("live event = %v, want a new window-created above seq %d", live, baseline)
	}

	_ = second.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if line, err := second.r.ReadBytes('\n'); err == nil {
		t.Fatalf("unexpected extra line after the live event: %s", line)
	}
}

// TestSubscribeResumeGapOnBootChange verifies a resume naming another boot id
// starts with a gap marker carrying the reason and the current boot id.
func TestSubscribeResumeGapOnBootChange(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")

	c := dialVerb(t, sp)
	ack := result(t, c.call(t, `{"id":1,"verb":"subscribe","params":{"after_seq":1,"boot_id":"an-older-boot"}}`))
	if ack["replayed"] != float64(0) {
		t.Fatalf("ack replayed = %v, want 0", ack["replayed"])
	}
	gap := c.readResp(t)
	if gap["type"] != EventGap || gap["reason"] != GapBootChanged || gap["boot_id"] != ack["boot_id"] {
		t.Fatalf("first line = %v, want a boot_changed gap with boot_id %v", gap, ack["boot_id"])
	}
}

// TestSubscribeResumeParamErrors verifies the two malformed resumes are refused
// with invalid_params: a boot id without a seq, and a seq this boot has not
// reached under its own boot id.
func TestSubscribeResumeParamErrors(t *testing.T) {
	d, sp := startTestDaemon(t)

	c := dialVerb(t, sp)
	if code := errCode(t, c.call(t, `{"id":1,"verb":"subscribe","params":{"boot_id":"x"}}`)); code != ErrVerbInvalidParams {
		t.Fatalf("boot_id without after_seq: code = %q, want %q", code, ErrVerbInvalidParams)
	}
	ahead := fmt.Sprintf(`{"id":2,"verb":"subscribe","params":{"after_seq":%d,"boot_id":%q}}`,
		d.events.currentSeq()+100, d.events.bootIdentity())
	if code := errCode(t, c.call(t, ahead)); code != ErrVerbInvalidParams {
		t.Fatalf("after_seq ahead: code = %q, want %q", code, ErrVerbInvalidParams)
	}
	// Neither refusal may leave the connection subscribed.
	result(t, c.call(t, `{"id":3,"verb":"subscribe","params":{"types":["bell"]}}`))
}

// TestWaitForAgentStateAnySession verifies any_session watches every session:
// the transition lands in a session that is not the most recently active one,
// and the result says which session matched.
func TestWaitForAgentStateAnySession(t *testing.T) {
	d, sp := startTestDaemon(t)
	quiet := makeSessionWithWindow(t, d, "alpha")
	makeSessionWithWindow(t, d, "beta")

	c := dialVerb(t, sp)
	done := make(chan map[string]any, 1)
	go func() {
		done <- c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"agent-state","any_session":true,"until":"needs_input","timeout":8000}}`)
	}()
	time.Sleep(150 * time.Millisecond)
	if err := quiet.SetDaemonWindowAgentState("Window", AgentStateNeedsInput, ""); err != nil {
		t.Fatalf("SetDaemonWindowAgentState: %v", err)
	}

	select {
	case resp := <-done:
		res := result(t, resp)
		if res["matched"] != true || res["session"] != "alpha" || res["state"] != "needs_input" {
			t.Fatalf("wait result = %v, want matched needs_input in alpha", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("wait-for agent-state any_session did not resolve")
	}
}
