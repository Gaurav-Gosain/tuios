//go:build !slim

package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRestrictedSubscribeCarriesOnlyTheSessionsItReaches(t *testing.T) {
	d, sp, a1, _, b1 := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"read_only": true})
	wantForbidden(t, "subscribe to b", callP(c, t, "subscribe", map[string]any{"session": "b"}))
	wantForbidden(t, "subscribe with hosts", callP(c, t, "subscribe", map[string]any{"hosts": true}))
	ack := result(t, callP(c, t, "subscribe", map[string]any{"types": []string{EventAgentState}}))
	if ack["type"] != EventSubscribed {
		t.Fatalf("subscribe ack = %v", ack)
	}

	plain := dialVerb(t, sp)
	setAgentState(t, plain, "b", b1, "working", "", "")
	setAgentState(t, plain, "a", a1, "working", "", "")

	ev := readEvent(t, c)
	if ev["session"] != "a" || ev["window"] != a1 {
		t.Fatalf("the first event on a restricted stream is %v, want a's; b's must not be written", ev)
	}
}

// TestRestrictedResumeFromAnEvictedSeqGivesTheBaseline: a scope own subscriber
// whose session was quiet while another session pushed its seq out of the
// ring gets a gap marker and nothing else, and an ack whose seq is past every
// event it missed and counts no replayed events. tuios mcp resumes from that
// seq; if the ack gave anything less, the caller could never move past the gap.
func TestRestrictedResumeFromAnEvictedSeqGivesTheBaseline(t *testing.T) {
	d, sp, a1, _, _ := scopeFixture(t)
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	d.events.mu.Lock()
	start := d.events.seq
	d.events.mu.Unlock()
	for range defaultEventRing + 10 {
		d.events.publish(streamEvent{Type: EventAgentState, Session: "b"})
	}
	c := dialVerb(t, sp)
	restrict(t, c, map[string]any{"read_only": true})
	ack := result(t, callP(c, t, "subscribe", map[string]any{
		"types":     []string{EventAgentState},
		"after_seq": start,
		"boot_id":   d.events.bootIdentity(),
	}))
	seq, _ := ack["seq"].(float64)
	if uint64(seq) < start+defaultEventRing+10 {
		t.Errorf("ack seq = %v, want at least %d", ack["seq"], start+defaultEventRing+10)
	}
	if ack["replayed"] != float64(0) {
		t.Errorf("ack replayed = %v, want 0: every retained event is another session's", ack["replayed"])
	}
	if ev := readEvent(t, c); ev["type"] != EventGap || ev["reason"] != GapEvicted {
		t.Fatalf("first event = %v, want a gap with reason evicted", ev)
	}
	// Nothing else is written: the next line is a live event of a.
	plain := dialVerb(t, sp)
	setAgentState(t, plain, "a", a1, "working", "", "")
	if ev := readEvent(t, c); ev["session"] != "a" || ev["seq"].(float64) <= seq {
		t.Fatalf("event after the gap = %v, want a live event of a above seq %v", ev, seq)
	}
}

// readEvent reads one event line from a subscribed connection.
func readEvent(t *testing.T, c *verbConn) map[string]any {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second * testDeadlineScale))
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("decode event %q: %v", line, err)
	}
	return ev
}
