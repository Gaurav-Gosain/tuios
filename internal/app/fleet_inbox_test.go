package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// The fleet in the client: the Inbox holds other machines' items, the rail
// waits for the daemon's push instead of polling, and a machine that dropped
// off keeps its rows, muted, saying when it was last heard from.

func hostItem(id, host, kind string, stale bool, seenAt int64) session.AttentionItem {
	it := item(id, kind, "remote-work", "w9", "approve Bash: make", time.Now().Add(-time.Minute).UnixNano())
	it.Host = host
	it.Stale, it.SeenAt = stale, seenAt
	return it
}

// TestAPushingDaemonReplacesTheHostPoll: with every host that is up streamed,
// the rail polls only as a slow backstop; a host the daemon cannot stream puts
// the old cadence back.
//
// Negative control: without the pushed branch in federationRefreshPlan the
// plan is the five second poll of an open rail.
func TestAPushingDaemonReplacesTheHostPoll(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.federationPolling = true
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Pushed:     true,
		Snapshot:   FederationSnapshot{Hosts: []FederationHost{{Name: "build", Status: "up"}}},
	})
	if after, refresh := m.federationRefreshPlan(); !refresh || after != hostRefreshPushed {
		t.Errorf("a pushing daemon is polled every %v (refresh %v), want the %v backstop", after, refresh, hostRefreshPushed)
	}
	// Attached to another machine, the push goes to the far daemon's
	// connection, not this client, so the rail keeps polling.
	m.AttachedHost = "build"
	if after, refresh := m.federationRefreshPlan(); !refresh || after != hostRefreshActive {
		t.Errorf("attached to another machine, the rail is polled every %v (refresh %v), want %v", after, refresh, hostRefreshActive)
	}
	m.AttachedHost = ""
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot:   FederationSnapshot{Hosts: []FederationHost{{Name: "build", Status: "up"}}},
	})
	if after, _ := m.federationRefreshPlan(); after != hostRefreshActive {
		t.Errorf("a host that is polled, with the rail open, is asked every %v, want %v", after, hostRefreshActive)
	}
}

// TestADroppedHostSaysWhenItWasLastSeen is the rail half of stale rows: the
// header says when, in words, and the rows of its last listing stay, muted and
// without an agent glyph.
func TestADroppedHostSaysWhenItWasLastSeen(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{{
			Name:     "build",
			Status:   string(federation.StatusUnreachable),
			LastOK:   time.Now().Add(-3 * time.Minute).Unix(),
			Sessions: []FederationSession{{Name: "api", WindowCount: 2, AgentState: "needs_input"}},
		}}},
	})
	text := hostRailText(t, m)
	if !strings.Contains(text, "seen 3m ago") {
		t.Errorf("a host that dropped off does not say when it was last seen:\n%s", text)
	}
	at := strings.Index(text, hostHeader(m, "build", false))
	if at < 0 || !strings.Contains(text[at:], "api") {
		t.Errorf("the dropped host's last rows are gone:\n%s", text)
	}
	if blocked, _ := m.hostAttention("build"); blocked != 0 {
		t.Errorf("a host nobody can reach counts %d sessions as waiting", blocked)
	}

	for _, tc := range []struct {
		status federation.Status
		want   string
	}{
		{federation.StatusNoDaemon, "no daemon"},
		{federation.StatusIncompatible, "version"},
		{federation.StatusUnreachable, "offline"},
	} {
		lastOK := int64(0)
		if tc.status != federation.StatusUnreachable {
			lastOK = time.Now().Unix()
		}
		if got := hostDownLabel(string(tc.status), lastOK, time.Now()); got != tc.want {
			t.Errorf("hostDownLabel(%s) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// TestTheInboxCountsAndAlertsForOtherMachines: a live host's item is counted
// and raises an alert naming its machine; a stale one is listed, muted, with
// when its machine was last seen, and neither counts nor alerts.
func TestTheInboxCountsAndAlertsForOtherMachines(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	captureHost(t, m)
	m.applyInboxEvents(opened(hostItem("build:1", "build", session.AttentionApproval, false, 0)))
	if c := m.inboxCounts(""); c.Blocked != 1 {
		t.Errorf("a live host's approval is not counted: %+v", c)
	}
	if c := m.inboxCounts("here"); c.Blocked != 0 {
		t.Errorf("a session filter on this machine counted another machine's item: %+v", c)
	}
	if len(m.Notifications) != 1 || !strings.Contains(m.Notifications[0].Message, "build:remote-work") {
		t.Fatalf("the alert does not name the machine: %+v", m.Notifications)
	}
	if tg := m.Notifications[0].Target; tg == nil || tg.Host != "build" || tg.SessionID != "remote-work" {
		t.Errorf("the alert targets %+v, want build:remote-work", tg)
	}
	m.Notifications = nil

	seen := time.Now().Add(-5 * time.Minute).UnixNano()
	stale := hostItem("build:1", "build", session.AttentionApproval, true, seen)
	m.applyInboxEvents(InboxEventsMsg{Events: []InboxEvent{{Action: session.AttentionUpdated, Item: &stale}}})
	if c := m.inboxCounts(""); c.Blocked != 0 {
		t.Errorf("a stale item is counted: %+v", c)
	}
	if m.inboxAlertable(stale, m.agentAlertPolicy()) {
		t.Error("a stale item can raise an alert")
	}
	if inboxNeedsYou(stale) {
		t.Error("next-attention visits an item on a machine nobody can reach")
	}
	m.OpenInbox("")
	out, _, _ := m.renderInbox()
	if plain := ansi.Strip(out); !strings.Contains(plain, "seen 5m ago") || !strings.Contains(plain, "build:remote-work") {
		t.Errorf("a stale item does not say where it is and when it was seen:\n%s", plain)
	}
}

// TestEnterOnAnItemOfAnUnreachableMachineSaysSo: the jump does not try to
// attach a machine nobody can reach, and says when it was last heard from.
func TestEnterOnAnItemOfAnUnreachableMachineSaysSo(t *testing.T) {
	m := inboxOS(t, zeroSettle())
	seen := time.Now().Add(-2 * time.Minute).UnixNano()
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{
		hostItem("build:1", "build", session.AttentionQuestion, true, seen),
	}})
	m.OpenInbox("")
	m.InboxActivate()
	if m.AttachedHost != "" {
		t.Fatalf("the client attached %q for a stale item", m.AttachedHost)
	}
	found := false
	for _, n := range m.Notifications {
		if strings.Contains(n.Message, "build cannot be reached") && strings.Contains(n.Message, "seen 2m ago") {
			found = true
		}
	}
	if !found {
		t.Errorf("the jump did not say the machine is unreachable: %+v", m.Notifications)
	}
}
