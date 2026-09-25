package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

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

// TestAStaleFederationTickIsDropped keeps the poll to one loop. The snapshot's
// re-arm retires the tick's own re-arm, so a tick from the older generation
// must fire nothing; without the guard every period doubled the timers.
func TestAStaleFederationTickIsDropped(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.federationPolling = true
	m.SidebarCollapsed = false

	_ = m.federationRefreshTick(hostRefreshActive) // gen 1, the tick's own re-arm
	old := m.federationTickGen
	_ = m.federationRefreshTick(hostRefreshActive) // gen 2, the snapshot's re-arm

	if _, cmd := m.Update(FederationRefreshTickMsg{Gen: old}); cmd != nil {
		t.Error("ASSERTION: a tick from a retired generation still polled")
	}
	if _, cmd := m.Update(FederationRefreshTickMsg{Gen: m.federationTickGen}); cmd == nil {
		t.Error("ASSERTION: the live generation's tick did not poll")
	}
}

// hostRailOS is the sidebar fixture with a federated snapshot already stored,
// which is the only way host rows ever reach the rail: the poll runs in a Cmd
// and the render path reads what it left behind. The attached session is
// named home so the rows can be told from the machine named local.
func hostRailOS(t *testing.T) *OS {
	t.Helper()
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 2,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{
				Name:     federation.LocalHostName,
				Status:   string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "home", WindowCount: 3}},
			},
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

// hostHeader is the text a machine's header row starts with: the fold mark
// and the name, as the active glyph set draws them.
func hostHeader(m *OS, name string, collapsed bool) string {
	mark := m.Settings.GetRailFoldOpenGlyph()
	if collapsed {
		mark = m.Settings.GetRailFoldShutGlyph()
	}
	return mark + " " + name
}

// railLineIndex is the index of the first rail line containing needle, or -1.
func railLineIndex(lines []string, needle string) int {
	for i, l := range lines {
		if strings.Contains(l, needle) {
			return i
		}
	}
	return -1
}

// TestMachineOrderHoldsAcrossASwitch is the complaint that started this: the
// machine groups keep their positions when the client switches onto a session
// on another machine. Before, build's rows moved to the top of the section and
// this machine's dropped into a group below them, so the row that was clicked
// moved out from under the pointer.
func TestMachineOrderHoldsAcrossASwitch(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"
	// build holds one session, which is what the live state below will show
	// once the client is on it: the fixture has no daemon client to list the
	// rest from.
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 2,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{Name: federation.LocalHostName, Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "home", WindowCount: 3}}},
			{Name: "build", Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "api", WindowCount: 3}}},
			{Name: "workstation", Status: string(federation.StatusUnreachable)},
		}},
	})
	headerLines := func() (out []int) {
		lines := railText(t, m)
		for _, host := range []string{"local", "build", "workstation"} {
			out = append(out, railLineIndex(lines, hostHeader(m, host, false)))
		}
		return out
	}
	before := headerLines()
	for _, at := range before {
		if at < 0 {
			t.Fatalf("a header is missing before the switch: %v\n%s", before, hostRailText(t, m))
		}
	}

	// The switch onto api on build, as adoptClient leaves the model: the
	// attached session is now build's, and this machine's sessions arrive in
	// the snapshot.
	m.AttachedHost = "build"
	m.SessionName = "api"
	m.sidebarCache.invalidate()

	after := headerLines()
	for i, host := range []string{"local", "build", "workstation"} {
		if before[i] != after[i] {
			t.Errorf("ASSERTION: the %s header moved from line %d to %d on a switch:\n%s",
				host, before[i], after[i], hostRailText(t, m))
		}
	}
	lines := railText(t, m)
	if railLineIndex(lines, "home") < railLineIndex(lines, hostHeader(m, "local", false)) {
		t.Errorf("ASSERTION: this machine's session is not under this machine's header:\n%s", strings.Join(lines, "\n"))
	}
	if strings.Contains(strings.Join(lines, "\n"), "@ build") {
		t.Errorf("ASSERTION: the section header names the attached machine:\n%s", strings.Join(lines, "\n"))
	}
	// The attached session is a local row under build, not a remote one.
	if got := m.SidebarSessionIDs; len(got) != 1 || got[0] != "api" {
		t.Errorf("ASSERTION: the attached session is not the switchable row: %v", got)
	}
}

// TestSidebarStatePersistsTheMachineLayout round-trips the machine order, the
// folded machines and the other machines' session orders through the state
// file, the way the section split persists.
func TestSidebarStatePersistsTheMachineLayout(t *testing.T) {
	m := hostRailOS(t)
	m.SidebarHostOrder = []string{"workstation", "build"}
	m.SidebarToggleHostCollapsed("workstation")
	m.setSidebarSessionOrder("build", []string{"web", "api"})
	m.saveSidebarState()

	data, err := os.ReadFile(filepath.Join(sidebarStateDir(), sidebarStateFileName))
	if err != nil {
		t.Fatalf("the state file was not written: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("the state file is not JSON: %v", err)
	}
	for _, key := range []string{"hosts_order", "hosts_collapsed", "host_session_order"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("ASSERTION: the state file has no %q:\n%s", key, data)
		}
	}

	fresh := &OS{}
	fresh.loadSidebarState()
	if got := strings.Join(fresh.SidebarHostOrder, ","); got != "workstation,build" {
		t.Errorf("ASSERTION: the machine order did not survive a reload: %q", got)
	}
	if !fresh.SidebarHostCollapsed("workstation") {
		t.Errorf("ASSERTION: the folded machine did not survive a reload")
	}
	if got := strings.Join(fresh.sidebarSessionOrderFor("build"), ","); got != "web,api" {
		t.Errorf("ASSERTION: build's session order did not survive a reload: %q", got)
	}
}

// TestRemoteSessionOrderIsPerMachine keeps one machine's drag order off
// another's rows: build's order names build's sessions, and this machine's
// SidebarOrder is not written by a drag while attached on build.
func TestRemoteSessionOrderIsPerMachine(t *testing.T) {
	m := hostRailOS(t)
	m.setSidebarSessionOrder("build", []string{"web", "api"})
	lines := railText(t, m)
	if railLineIndex(lines, "web") > railLineIndex(lines, "api") {
		t.Errorf("ASSERTION: build's rows ignore build's order:\n%s", strings.Join(lines, "\n"))
	}
	if len(m.SidebarOrder) != 0 {
		t.Errorf("ASSERTION: build's order was written into this machine's: %v", m.SidebarOrder)
	}

	m.AttachedHost = "build"
	m.SessionName = "api"
	m.SidebarDrag = sidebarDragState{Dragging: true, SessionID: "api", Order: []string{"api"}}
	m.SidebarRelease(0, 0)
	if len(m.SidebarOrder) != 0 {
		t.Errorf("ASSERTION: a drag on build wrote this machine's order: %v", m.SidebarOrder)
	}
	if got := strings.Join(m.sidebarSessionOrderFor("build"), ","); got != "api" {
		t.Errorf("ASSERTION: a drag on build did not write build's order: %q", got)
	}
}

// TestRemoteSessionsAreNotDragTargets keeps a remote session out of the local
// session machinery. It is attached over the link, never switched to as a local
// session or reordered, so its namespaced id must not reach the drag-and-switch
// id list.
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

// TestSessionCyclingSkipsOtherMachines keeps the cycle key on the attached
// machine: with one local session and two on build, there is nowhere to go,
// rather than a host row's id handed to SwitchToSession.
func TestSessionCyclingSkipsOtherMachines(t *testing.T) {
	m := hostRailOS(t)
	if got := m.railNeighbourSession(1); got != "" {
		t.Errorf("ASSERTION: cycling reached %q, which is not a session on this machine", got)
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

// TestMachineLayoutMovesTheRenderSignature: a fold, a machine reorder and a
// switch of machine each redraw the rail.
func TestMachineLayoutMovesTheRenderSignature(t *testing.T) {
	m := hostRailOS(t)
	sig := m.sidebarSignature()
	m.SidebarToggleHostCollapsed("build")
	if after := m.sidebarSignature(); after == sig {
		t.Error("ASSERTION: folding a machine did not move the signature")
	}
	sig = m.sidebarSignature()
	m.SidebarHostOrder = []string{"workstation"}
	if after := m.sidebarSignature(); after == sig {
		t.Error("ASSERTION: reordering the machines did not move the signature")
	}
	sig = m.sidebarSignature()
	m.AttachedHost = "build"
	if after := m.sidebarSignature(); after == sig {
		t.Error("ASSERTION: switching machine did not move the signature")
	}
}
