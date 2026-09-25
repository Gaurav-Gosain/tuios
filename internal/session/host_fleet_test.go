package session

import (
	"bufio"
	"bytes"
	"errors"
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
