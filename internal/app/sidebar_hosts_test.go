package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// hostRailOS is the sidebar fixture with a federated snapshot already stored,
// which is the only way host rows ever reach the rail: the poll runs in a Cmd
// and the render path reads what it left behind.
func hostRailOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 2,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{
				Name:   "build",
				Status: string(federation.StatusUp),
				Reason: "The host is answering.",
				Sessions: []FederationSession{
					{Name: "api", WindowCount: 3},
					{Name: "web", WindowCount: 1},
				},
			},
			{
				Name:   "workstation",
				Status: string(federation.StatusUnreachable),
				Reason: "The host did not answer.",
			},
		}},
	})
	return m
}

// hostRailText is railText joined into one block, so an assertion can talk
// about the order rows appear in as well as their content.
func hostRailText(t *testing.T, m *OS) string {
	t.Helper()
	return strings.Join(railText(t, m), "\n")
}

// TestSidebarDrawsHostGroups is the on-screen assertion: a host that answers
// gets a header row and its sessions sit under it, below every local session.
func TestSidebarDrawsHostGroups(t *testing.T) {
	m := hostRailOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail reserved no columns")
	}
	text := hostRailText(t, m)

	for _, want := range []string{"@ build", "api", "web", "@ workstation"} {
		if !strings.Contains(text, want) {
			t.Errorf("the rail does not show %q:\n%s", want, text)
		}
	}

	// Local first is the rule. The attached session's own row has to appear
	// above every host header, or the rail has buried this machine under the
	// others.
	local := strings.Index(text, "local")
	host := strings.Index(text, "@ build")
	if local < 0 || host < 0 {
		t.Fatalf("could not find both the local session and the host header:\n%s", text)
	}
	if local > host {
		t.Errorf("the local session is drawn below the host group:\n%s", text)
	}
}

// TestSidebarShowsAnUnreachableHost is section 7 on screen: the machine stays
// listed with a word saying why, rather than disappearing.
func TestSidebarShowsAnUnreachableHost(t *testing.T) {
	m := hostRailOS(t)
	text := hostRailText(t, m)
	if !strings.Contains(text, "offline") {
		t.Errorf("an unreachable host is not marked on the rail:\n%s", text)
	}
	// It also must not be shown as holding sessions it could not report.
	after := text[strings.Index(text, "@ workstation"):]
	if strings.Contains(after, "api") || strings.Contains(after, "web") {
		t.Errorf("sessions are drawn under a host that did not answer:\n%s", after)
	}
}

// TestRemoteRowsAreNotTargets is the read-only rule enforced where it can
// actually be broken. A remote row that recorded a hit would be clickable, and
// a click resolves to a session switch, which is an attach across a link that
// this release does not carry.
func TestRemoteRowsAreNotTargets(t *testing.T) {
	m := hostRailOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail drew nothing")
	}

	for _, id := range m.SidebarSessionIDs {
		if strings.HasPrefix(id, "\x00host/") {
			t.Errorf("a remote row is in the clickable id list: %q", id)
		}
	}
	for _, hit := range m.SidebarHits {
		if strings.HasPrefix(hit.SessionID, "\x00host/") {
			t.Errorf("a remote row recorded a hit rectangle: %+v", hit)
		}
	}
	for _, nav := range m.SidebarNav {
		if strings.HasPrefix(nav.SessionID, "\x00host/") {
			t.Errorf("a remote row is a keyboard target: %+v", nav)
		}
	}
}

// TestHostRowsDoNotEnterTheColourArbitration keeps a machine elsewhere from
// changing what a local session looks like.
func TestHostRowsDoNotEnterTheColourArbitration(t *testing.T) {
	// Restored to whatever it was, not to false: this is a package global, and
	// another test in this binary asserts on it.
	prev := config.SessionColors
	config.SessionColors = true
	t.Cleanup(func() { config.SessionColors = prev })

	m := hostRailOS(t)
	tree := m.BuildSessionTree()
	local := localSessionNodes(tree.Sessions)
	if len(local) == len(tree.Sessions) {
		t.Fatal("the fixture produced no host rows, so this proves nothing")
	}
	for _, n := range local {
		if isRemoteNode(n) {
			t.Errorf("localSessionNodes kept a remote row: %+v", n)
		}
	}
	if len(local) == 0 {
		t.Fatal("localSessionNodes dropped every row")
	}
}

// TestFederationPollStopsWithNoHosts keeps the default install from paying for
// a feature it is not using. The first answer says zero hosts and no further
// poll is scheduled.
func TestFederationPollStopsWithNoHosts(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.federationPolling = true

	m.applyFederationSnapshot(FederationHostsMsg{Configured: 0})
	if _, refresh := m.federationRefreshPlan(); refresh {
		t.Error("the client keeps polling a daemon that reported no hosts")
	}
	if len(m.FederationHosts) != 0 {
		t.Errorf("hosts are stored for a daemon that has none: %+v", m.FederationHosts)
	}

	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot:   FederationSnapshot{Hosts: []FederationHost{{Name: "build", Status: "up"}}},
	})
	if _, refresh := m.federationRefreshPlan(); !refresh {
		t.Error("the client stopped polling a daemon that has a host")
	}
}

// TestFederationSnapshotMovesTheRenderSignature is what makes a status change
// visible. The rail is cached on a signature, so a snapshot the signature does
// not fold would be drawn once and then never updated.
func TestFederationSnapshotMovesTheRenderSignature(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	before := m.sidebarSignature()

	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot:   FederationSnapshot{Hosts: []FederationHost{{Name: "build", Status: "up"}}},
	})
	if after := m.sidebarSignature(); after == before {
		t.Fatal("the rail's cache signature did not move when a host snapshot landed")
	}
}

// TestHostGroupNodesShapeTheRows checks the row list itself: one header per
// host, then that host's sessions, and a host that failed contributing its
// header alone.
func TestHostGroupNodesShapeTheRows(t *testing.T) {
	m := hostRailOS(t)
	nodes := m.hostGroupNodes()

	var kinds []string
	for _, n := range nodes {
		switch n.Kind {
		case sessiontree.KindHost:
			kinds = append(kinds, "host:"+n.Title)
		case sessiontree.KindSession:
			kinds = append(kinds, "session:"+n.Title)
		}
	}
	want := []string{"host:build", "session:api", "session:web", "host:workstation"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("rows are %v, want %v", kinds, want)
	}
	for _, n := range nodes {
		if n.Host == "" {
			t.Errorf("a federated row does not name its host: %+v", n)
		}
		if !isRemoteNode(n) {
			t.Errorf("a federated row is not treated as remote: %+v", n)
		}
	}
}
