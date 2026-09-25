package session

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// The fleet, proved with two real daemons linked by the real proxy: what waits
// on the far machine reaches the hub's Inbox and listings without anyone
// polling, and stays visible, marked stale, while the link is down.

// fleetBudget bounds a wait on something crossing the link.
const fleetBudget = 15 * time.Second

// hubVerb dials the hub's socket, which GetSocketPath names in these tests.
func hubVerb(t *testing.T) *verbConn {
	t.Helper()
	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}
	return dialVerb(t, sp)
}

// waitHostAttention polls the hub's list-attention until pred holds.
func waitHostAttention(t *testing.T, c *verbConn, params, why string, pred func([]map[string]any) bool) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(fleetBudget * testDeadlineScale)
	for {
		items, _ := listAttention(t, c, params)
		if pred(items) {
			return items
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the hub's Inbox holds %v", why, items)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFleetLive blocks until the hub follows host by stream.
func waitFleetLive(t *testing.T, hub *Daemon, host string) {
	t.Helper()
	deadline := time.Now().Add(fleetBudget * testDeadlineScale)
	for time.Now().Before(deadline) {
		if mode, _ := hub.fleet.mode(host); mode == fleetEventsLive {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mode, note := hub.fleet.mode(host)
	t.Fatalf("the hub never followed %s live: mode %q, note %q", host, mode, note)
}

func withHost(host string) func([]map[string]any) bool {
	return func(items []map[string]any) bool {
		for _, it := range items {
			if it["host"] == host {
				return true
			}
		}
		return false
	}
}

func withoutHost(host string) func([]map[string]any) bool {
	return func(items []map[string]any) bool { return !withHost(host)(items) }
}

// TestAHostsItemsGoStaleWhenTheLinkDropsAndFreshWhenItReturns covers the rows
// of a machine nobody can reach: they stay, marked stale with when the host
// was last heard from, and the listings keep the host's sessions. A redial
// resumes and clears the mark.
func TestAHostsItemsGoStaleWhenTheLinkDropsAndFreshWhenItReturns(t *testing.T) {
	hub, far := startHubAndFar(t)
	sess := makeSessionWithWindow(t, far.daemon, "remote-work")
	win := sess.GetState().Windows[0].ID
	waitForHostUp(t, hub, "build")
	waitFleetLive(t, hub, "build")

	farC := dialVerb(t, far.socket)
	setAgentState(t, farC, "remote-work", win, "errored", "", "rate limited")
	c := hubVerb(t)
	waitHostAttention(t, c, "", "the errored pane on build", withHost("build"))

	// The session listing is cached by the refresh the event triggered.
	deadline := time.Now().Add(fleetBudget * testDeadlineScale)
	for {
		if rows, at := hub.fleet.cachedSessions("build"); len(rows) > 0 && !at.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hub never cached build's sessions")
		}
		time.Sleep(20 * time.Millisecond)
	}

	dials := far.dialCount()
	far.breakLink()
	stale := waitHostAttention(t, c, "", "the item marked stale", func(items []map[string]any) bool {
		for _, it := range items {
			if it["host"] == "build" && it["stale"] == true {
				return true
			}
		}
		return false
	})
	if seen, _ := stale[0]["seen_at"].(float64); seen <= 0 {
		t.Errorf("a stale item carries no seen_at: %v", stale[0])
	}

	// The listing keeps build's rows while it is down, stale.
	res := result(t, c.call(t, `{"id":1,"verb":"list-host-sessions","params":{"host":"build"}}`))
	hosts, _ := res["hosts"].([]any)
	var entry map[string]any
	for _, h := range hosts {
		if m := h.(map[string]any); m["host"] == "build" {
			entry = m
		}
	}
	if entry == nil || entry["stale"] != true || entry["fetched_at"] == nil {
		t.Fatalf("a down host's entry is %v, want its cached rows marked stale", entry)
	}
	if !strings.Contains(fmt.Sprint(entry["sessions"]), "remote-work") {
		t.Errorf("the stale entry lost the host's sessions: %v", entry["sessions"])
	}

	far.waitForDial(t, dials)
	waitForHostUp(t, hub, "build")
	waitHostAttention(t, c, "", "the item fresh again", func(items []map[string]any) bool {
		for _, it := range items {
			if it["host"] == "build" && it["stale"] == nil {
				return true
			}
		}
		return false
	})
}

// TestDismissingAHostItemHidesItHereOnly holds the per-person rule: the hub's
// person dismissing a far item does not touch the far Inbox, and the item comes
// back when the far agent changes it.
func TestDismissingAHostItemHidesItHereOnly(t *testing.T) {
	hub, far := startHubAndFar(t)
	sess := makeSessionWithWindow(t, far.daemon, "remote-work")
	win := sess.GetState().Windows[0].ID
	makeSessionWithWindow(t, hub, "here")
	waitForHostUp(t, hub, "build")
	waitFleetLive(t, hub, "build")

	farC := dialVerb(t, far.socket)
	setAgentState(t, farC, "remote-work", win, "needs_input", "question", "which branch?")
	c := hubVerb(t)
	items := waitHostAttention(t, c, "", "the question on build", withHost("build"))
	id := items[0]["id"].(string)

	sp, _ := GetSocketPath()
	nonce := attachTUI(t, sp, "here").HumanNonce()
	res := result(t, c.call(t, fmt.Sprintf(`{"id":1,"verb":"dismiss-attention","params":{"id":%q,"human_nonce":%q}}`, id, nonce)))
	if res["host"] != "build" {
		t.Errorf("the dismiss answer is %v, want it to name build", res)
	}
	waitHostAttention(t, c, "", "the dismissed item gone here", withoutHost("build"))
	farItems, _ := listAttention(t, farC, "")
	if len(farItems) != 1 {
		t.Fatalf("dismissing on the hub changed the far Inbox: %v", farItems)
	}

	setAgentState(t, farC, "remote-work", win, "needs_input", "question", "which branch, main or dev?")
	waitHostAttention(t, c, "", "the item back after it changed on build", withHost("build"))
}

// TestTheHubListsEveryAgentOnEveryHost is the other half of P17: a host's
// agents are listed from every session it holds, each row naming its session,
// where the listing used to read only the host's most recent session.
func TestTheHubListsEveryAgentOnEveryHost(t *testing.T) {
	hub, far := startHubAndFar(t)
	a := makeSessionWithWindow(t, far.daemon, "alpha")
	b := makeSessionWithWindow(t, far.daemon, "beta")
	waitForHostUp(t, hub, "build")

	farC := dialVerb(t, far.socket)
	setAgentState(t, farC, "alpha", a.GetState().Windows[0].ID, "working", "", "")
	setAgentState(t, farC, "beta", b.GetState().Windows[0].ID, "done", "", "")

	c := hubVerb(t)
	res := result(t, c.call(t, `{"id":1,"verb":"list-host-agents","params":{"host":"build"}}`))
	hosts, _ := res["hosts"].([]any)
	var entry map[string]any
	for _, h := range hosts {
		if m := h.(map[string]any); m["host"] == "build" {
			entry = m
		}
	}
	if entry == nil {
		t.Fatalf("no build entry in %v", res)
	}
	agents, _ := entry["agents"].([]any)
	sessions := map[string]bool{}
	for _, raw := range agents {
		row := raw.(map[string]any)
		s, _ := row["session"].(string)
		sessions[s] = true
	}
	if !sessions["alpha"] || !sessions["beta"] {
		t.Fatalf("the host's agents came from %v, want alpha and beta: %v", sessions, agents)
	}
	if entry["session"] != nil {
		t.Errorf("an entry spanning two sessions names one: %v", entry["session"])
	}
}

// TestRelayedHostEventsNeedTheHostsParam keeps an existing subscriber's world
// the same: an agent-state event from another machine reaches only a
// subscriber that asked for hosts.
func TestRelayedHostEventsNeedTheHostsParam(t *testing.T) {
	hub, far := startHubAndFar(t)
	sess := makeSessionWithWindow(t, far.daemon, "remote-work")
	win := sess.GetState().Windows[0].ID
	waitForHostUp(t, hub, "build")
	waitFleetLive(t, hub, "build")

	plain := hubVerb(t)
	result(t, plain.call(t, `{"id":1,"verb":"subscribe","params":{"types":["agent-state"]}}`))
	withHosts := hubVerb(t)
	result(t, withHosts.call(t, `{"id":1,"verb":"subscribe","params":{"types":["agent-state"],"hosts":true}}`))

	farC := dialVerb(t, far.socket)
	setAgentState(t, farC, "remote-work", win, "working", "", "")

	ev := readStreamEvent(t, withHosts)
	if ev["host"] != "build" || ev["session"] != "remote-work" || ev["state"] != "working" {
		t.Fatalf("the relayed event is %v", ev)
	}

	_ = plain.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if line, err := plain.r.ReadBytes('\n'); err == nil {
		t.Fatalf("a subscriber without hosts got another machine's event: %s", line)
	}
}

// TestASessionFilterDoesNotMatchAHostsInboxItem: a subscriber filtering on a
// session name reads it as a session on this machine, so an Inbox item of a
// far session with the same name is not delivered to it.
//
// Negative control: without Host on attentionEvent the far item's event
// carries session remote-work and no host, and the filtered subscriber gets it.
func TestASessionFilterDoesNotMatchAHostsInboxItem(t *testing.T) {
	hub, far := startHubAndFar(t)
	sess := makeSessionWithWindow(t, far.daemon, "remote-work")
	makeSessionWithWindow(t, hub, "remote-work")
	waitForHostUp(t, hub, "build")
	waitFleetLive(t, hub, "build")

	filtered := hubVerb(t)
	result(t, filtered.call(t, `{"id":1,"verb":"subscribe","params":{"types":["attention"],"session":"remote-work"}}`))
	all := hubVerb(t)
	result(t, all.call(t, `{"id":1,"verb":"subscribe","params":{"types":["attention"]}}`))

	farC := dialVerb(t, far.socket)
	setAgentState(t, farC, "remote-work", sess.GetState().Windows[0].ID, "errored", "", "boom")

	ev := readStreamEvent(t, all)
	if ev["host"] != "build" {
		t.Fatalf("the unfiltered subscriber got %v, want the item of build with host set", ev)
	}
	_ = filtered.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if line, err := filtered.r.ReadBytes('\n'); err == nil {
		t.Fatalf("a subscriber filtering on this machine's session got another machine's item: %s", line)
	}
}

// cannedConn answers each request line with the next canned reply.
type cannedConn struct {
	replies []string
	out     bytes.Buffer
	pending bytes.Buffer
}

func (c *cannedConn) Write(p []byte) (int, error) {
	c.out.Write(p)
	if len(c.replies) > 0 {
		c.pending.WriteString(c.replies[0] + "\n")
		c.replies = c.replies[1:]
	}
	return len(p), nil
}

func (c *cannedConn) Read(p []byte) (int, error) {
	if c.pending.Len() == 0 {
		return 0, io.EOF
	}
	return c.pending.Read(p)
}

func (c *cannedConn) Close() error { return nil }

func fleetConnOn(replies ...string) *fleetConn {
	c := &cannedConn{replies: replies}
	return &fleetConn{rw: c, br: bufio.NewReader(c)}
}

// TestAnOldHostIsPolledWithANote is the version skew: a host whose tuios has
// no Inbox, or cannot resume its stream, is not streamed, and what it is told
// to do about it is in the note list-hosts carries.
func TestAnOldHostIsPolledWithANote(t *testing.T) {
	noInbox := fleetConnOn(`{"id":1,"error":{"code":"unknown_verb","message":"unknown verb"}}`)
	_, err := noInbox.listAttention()
	var old errFleetOld
	if !errors.As(err, &old) || !strings.Contains(old.note, "Update tuios on the host") {
		t.Errorf("a host with no Inbox gave %v, want an errFleetOld naming the update", err)
	}

	noHostParam := fleetConnOn(
		`{"id":1,"error":{"code":"invalid_params","message":"verb list-attention has no parameter \"host\"","hint":{"param":"host"}}}`,
		`{"id":2,"result":{"items":[],"seq":7}}`,
	)
	_, err = noHostParam.listAttention()
	if !errors.As(err, &old) {
		t.Errorf("a host with an Inbox but no boot_id gave %v, want errFleetOld", err)
	}

	noResume := fleetConnOn(`{"id":1,"error":{"code":"invalid_params","message":"verb subscribe has no parameter \"after_seq\"","hint":{"param":"after_seq"}}}`)
	if err := noResume.subscribe(7, "b"); !errors.As(err, &old) {
		t.Errorf("a host that cannot resume gave %v, want errFleetOld", err)
	}
}
