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

// TestRemoteSessionsAreNotDragTargets keeps a remote session out of the local
// session machinery. It is opened over ssh, never switched to or reordered, so
// its namespaced id must not reach the drag-and-switch id list.
func TestRemoteSessionsAreNotDragTargets(t *testing.T) {
	m := hostRailOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail drew nothing")
	}
	for _, id := range m.SidebarSessionIDs {
		if strings.HasPrefix(id, "\x00host/") {
			t.Errorf("a remote row is in the local switch/drag id list: %q", id)
		}
	}
}

// cachedDownHostOS is the rail with a host whose link is not up but whose last
// listing is still cached. It is the fixture the two halves of the reachability
// rule need: an up host with sessions, and a down host with sessions.
func cachedDownHostOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 2,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{
				Name:   "build",
				Status: string(federation.StatusUp),
				Sessions: []FederationSession{
					{Name: "api", WindowCount: 3},
				},
			},
			{
				// A cached listing shown while the link redials. Its rows are
				// stale, so they are drawn and must not be reachable.
				Name:   "stale",
				Status: string(federation.StatusConnecting),
				Sessions: []FederationSession{
					{Name: "old", WindowCount: 1},
				},
			},
		}},
	})
	return m
}

// TestRemoteSessionIsATargetOnlyWhenTheHostIsUp is the reachability rule for
// #171, both halves in one fixture. A session under an up host is a keyboard
// and mouse target of kind sidebarRowHostSession. A session under a host that
// is not up is drawn and is not a target, because its listing is cached.
func TestRemoteSessionIsATargetOnlyWhenTheHostIsUp(t *testing.T) {
	m := cachedDownHostOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail drew nothing")
	}

	upTarget, downTarget := false, false
	for _, nav := range m.SidebarNav {
		if nav.Kind != sidebarRowHostSession {
			continue
		}
		switch {
		case nav.SessionID == "build" && nav.WindowID == "api":
			upTarget = true
		case nav.SessionID == "stale" && nav.WindowID == "old":
			downTarget = true
		}
	}
	if !upTarget {
		t.Error("ASSERTION: a session under an up host is not a keyboard target")
	}
	if downTarget {
		t.Error("ASSERTION: a session under a host that is not up is a keyboard target")
	}

	// The same rule on the mouse hit list: the up host's session records a
	// rectangle, the stale one does not.
	upHit, downHit := false, false
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowHostSession {
			continue
		}
		if h.SessionID == "build" {
			upHit = true
		}
		if h.SessionID == "stale" {
			downHit = true
		}
	}
	if !upHit {
		t.Error("ASSERTION: a session under an up host records no click rectangle")
	}
	if downHit {
		t.Error("ASSERTION: a session under a host that is not up is clickable")
	}
}

// TestUpHostOffersANewControl proves the create affordance is present and
// reachable on an up host, and absent on one that is not up.
func TestUpHostOffersANewControl(t *testing.T) {
	m := cachedDownHostOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail drew nothing")
	}
	up, down := false, false
	for _, h := range m.SidebarHits {
		if h.Kind != sidebarRowHostNew {
			continue
		}
		if h.SessionID == "build" {
			up = true
		}
		if h.SessionID == "stale" {
			down = true
		}
	}
	if !up {
		t.Error("ASSERTION: an up host has no + control to create a session")
	}
	if down {
		t.Error("ASSERTION: a host that is not up offers a + control")
	}
}

// TestHostHeaderIsNeverATarget keeps the group header itself inert: it names a
// machine, and there is nothing to do to a machine from its header row.
func TestHostHeaderIsNeverATarget(t *testing.T) {
	m := hostRailOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail drew nothing")
	}
	for _, nav := range m.SidebarNav {
		if nav.Kind == sidebarRowHostSession && nav.WindowID == "" {
			t.Errorf("a host header is a keyboard target: %+v", nav)
		}
	}
}

// TestHostRowsDoNotEnterTheColourArbitration keeps a machine elsewhere from
// changing what a local session looks like.
func TestHostRowsDoNotEnterTheColourArbitration(t *testing.T) {
	// Restored to whatever it was, not to false: this is a package global, and
	// another test in this binary asserts on it.
	prev := config.Global.SessionColors
	config.Global.SessionColors = true
	t.Cleanup(func() { config.Global.SessionColors = prev })

	m := hostRailOS(t)
	tree := m.BuildSessionTree()

	// Fixture sanity, checked against the tree rather than against the function
	// under test. Folding the two together made a failure of the filter read as
	// "the fixture produced no host rows", which blames the wrong thing.
	remotes := 0
	for _, n := range tree.Sessions {
		if isRemoteNode(n) {
			remotes++
		}
	}
	if remotes == 0 {
		t.Fatal("the fixture built a tree with no host rows, so this proves nothing")
	}

	local := localSessionNodes(tree.Sessions)
	for _, n := range local {
		if isRemoteNode(n) {
			t.Errorf("localSessionNodes kept a remote row, so a machine elsewhere joins the colour arbitration: %+v", n)
		}
	}
	if len(local) != len(tree.Sessions)-remotes {
		t.Errorf("localSessionNodes returned %d of %d rows with %d remote; it is not dropping exactly the remote ones",
			len(local), len(tree.Sessions), remotes)
	}
	if len(local) == 0 {
		t.Fatal("localSessionNodes dropped every row, this machine's included")
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
