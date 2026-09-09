package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

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

// hostHit is the recorded rectangle of a machine's header row.
func hostHit(t *testing.T, m *OS, host string) sidebarRowHit {
	t.Helper()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowHost && h.SessionID == host {
			return h
		}
	}
	t.Fatalf("no header row recorded for host %q; hits: %+v", host, m.SidebarHits)
	return sidebarRowHit{}
}

// TestSidebarDrawsHostGroups is the on-screen assertion: every machine gets a
// header row wearing the fold mark, this machine's first, and a host's
// sessions sit under its header.
func TestSidebarDrawsHostGroups(t *testing.T) {
	m := hostRailOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail reserved no columns")
	}
	lines := railText(t, m)
	text := strings.Join(lines, "\n")

	for _, want := range []string{hostHeader(m, "local", false), "home", hostHeader(m, "build", false), "api", "web", hostHeader(m, "workstation", false)} {
		if !strings.Contains(text, want) {
			t.Errorf("ASSERTION: the rail does not show %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "@ ") {
		t.Errorf("ASSERTION: the rail still marks a machine with @:\n%s", text)
	}

	// Local first is the rule: this machine's header, then its session, then
	// the other machines.
	local := railLineIndex(lines, hostHeader(m, "local", false))
	home := railLineIndex(lines, "home")
	build := railLineIndex(lines, hostHeader(m, "build", false))
	api := railLineIndex(lines, "api")
	if !(local >= 0 && local < home && home < build && build < api) {
		t.Errorf("ASSERTION: rows are out of order (local %d, home %d, build %d, api %d):\n%s",
			local, home, build, api, text)
	}
}

// TestRemoteSessionRowSitsOnTheSpine keeps a remote session's name on the same
// column as a local one's, so a machine's rows read the same whether the
// client is on it or not. The header above says which machine; the ink says
// it is a listing.
func TestRemoteSessionRowSitsOnTheSpine(t *testing.T) {
	m := hostRailOS(t)
	lines := railText(t, m)
	home := railLineIndex(lines, "home")
	api := railLineIndex(lines, "api")
	if home < 0 || api < 0 {
		t.Fatalf("missing rows:\n%s", strings.Join(lines, "\n"))
	}
	if railColumnOf(lines[home], "home") != railColumnOf(lines[api], "api") {
		t.Errorf("ASSERTION: the remote row is not on the local row's spine:\n%q\n%q", lines[home], lines[api])
	}
}

// railColumnOf is the screen column needle starts on in a rail line, in cells
// rather than bytes: the marks in front of a name are multi-byte runes.
func railColumnOf(line, needle string) int {
	at := strings.Index(line, needle)
	if at < 0 {
		return -1
	}
	return lipgloss.Width(line[:at])
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
	at := strings.Index(text, hostHeader(m, "workstation", false))
	if at < 0 {
		t.Fatalf("no header for the unreachable host:\n%s", text)
	}
	after := text[at:]
	if strings.Contains(after, "api") || strings.Contains(after, "web") {
		t.Errorf("sessions are drawn under a host that did not answer:\n%s", after)
	}
}

// TestSingleMachineRailIsUnchanged is the promise to the default install: with
// no other machine the section has no machine headers at all.
func TestSingleMachineRailIsUnchanged(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.SessionName = "home"
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 0,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{Name: federation.LocalHostName, Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "home", WindowCount: 3}}},
		}},
	})
	text := hostRailText(t, m)
	if strings.Contains(text, hostHeader(m, "local", false)) {
		t.Errorf("ASSERTION: a rail with one machine draws a machine header:\n%s", text)
	}
	for _, nav := range m.SidebarNav {
		if nav.Kind == sidebarRowHost {
			t.Errorf("ASSERTION: a rail with one machine records a machine row: %+v", nav)
		}
	}
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

// TestHostOrderIsTheUsers applies the persisted machine order over the daemon's
// sorted one, with this machine pinned first whatever the order says.
func TestHostOrderIsTheUsers(t *testing.T) {
	m := hostRailOS(t)
	m.SidebarHostOrder = []string{"workstation", federation.LocalHostName, "build"}
	lines := railText(t, m)
	local := railLineIndex(lines, hostHeader(m, "local", false))
	build := railLineIndex(lines, hostHeader(m, "build", false))
	work := railLineIndex(lines, hostHeader(m, "workstation", false))
	if !(local >= 0 && local < work && work < build) {
		t.Errorf("ASSERTION: the machine order is not the user's (local %d, workstation %d, build %d):\n%s",
			local, work, build, strings.Join(lines, "\n"))
	}
	if got := m.SidebarHostIDs; strings.Join(got, ",") != "workstation,build" {
		t.Errorf("ASSERTION: the drawn machine order is %v, want workstation,build", got)
	}
}

// TestDraggingAHostHeaderReordersTheMachines is the reorder gesture on a
// machine's header: the same press, move and release a session row takes, and
// the order it leaves is persisted.
func TestDraggingAHostHeaderReordersTheMachines(t *testing.T) {
	m := hostRailOS(t)
	m.sidebarPanelLines()
	build := hostHit(t, m, "build")
	work := hostHit(t, m, "workstation")

	if !m.SidebarClick(build.X0+2, build.Y0, false) {
		t.Fatal("the press on the header was not consumed")
	}
	if !m.SidebarDragMotion(build.X0+2, work.Y0) {
		t.Fatal("the motion was not consumed")
	}
	if !m.SidebarDrag.Dragging || !m.SidebarDrag.Host {
		t.Fatalf("ASSERTION: moving off the header did not start a machine drag: %+v", m.SidebarDrag)
	}
	m.SidebarRelease(build.X0+2, work.Y0)
	if got := strings.Join(m.SidebarHostOrder, ","); got != "workstation,build" {
		t.Fatalf("ASSERTION: the drop left the machine order %q, want workstation,build", got)
	}
	// And the rail draws it.
	lines := railText(t, m)
	if railLineIndex(lines, hostHeader(m, "workstation", false)) > railLineIndex(lines, hostHeader(m, "build", false)) {
		t.Errorf("ASSERTION: the rail did not take the dragged order:\n%s", strings.Join(lines, "\n"))
	}
	// Nothing about the sessions' own order was touched.
	if m.SidebarOrder != nil {
		t.Errorf("a machine drag wrote the session order: %v", m.SidebarOrder)
	}
}

// TestThisMachineIsNotDragged keeps this machine pinned first: a drag that
// starts on its header is a click and nothing more.
func TestThisMachineIsNotDragged(t *testing.T) {
	m := hostRailOS(t)
	m.sidebarPanelLines()
	local := hostHit(t, m, federation.LocalHostName)
	build := hostHit(t, m, "build")
	m.SidebarClick(local.X0+2, local.Y0, false)
	m.SidebarDragMotion(local.X0+2, build.Y0+1)
	if m.SidebarDrag.Dragging {
		t.Fatalf("ASSERTION: this machine's header started a drag: %+v", m.SidebarDrag)
	}
	m.SidebarRelease(local.X0+2, build.Y0+1)
	if len(m.SidebarHostOrder) != 0 {
		t.Errorf("ASSERTION: the gesture wrote a machine order: %v", m.SidebarHostOrder)
	}
}

// TestHostHeaderClickFoldsTheGroup is the toggle the maintainer asked for: a
// click on a machine's header hides its rows, the header shows how many it is
// holding and wears the shut mark, and a second click opens it again. The
// keyboard's enter runs the same toggle.
func TestHostHeaderClickFoldsTheGroup(t *testing.T) {
	m := hostRailOS(t)
	m.sidebarPanelLines()
	build := hostHit(t, m, "build")

	m.SidebarClick(build.X0+2, build.Y0, false)
	m.SidebarRelease(build.X0+2, build.Y0)
	if !m.SidebarHostCollapsed("build") {
		t.Fatal("ASSERTION: a click on the header did not fold the group")
	}
	lines := railText(t, m)
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "api") || strings.Contains(text, "web") {
		t.Errorf("ASSERTION: a folded host still lists its sessions:\n%s", text)
	}
	at := railLineIndex(lines, hostHeader(m, "build", true))
	if at < 0 {
		t.Fatalf("ASSERTION: the folded header does not wear the shut mark:\n%s", text)
	}
	if !strings.Contains(lines[at], "2") {
		t.Errorf("ASSERTION: the folded header does not say how many sessions it holds: %q", lines[at])
	}
	// The other machines are untouched.
	if !strings.Contains(text, "home") || !strings.Contains(text, "offline") {
		t.Errorf("ASSERTION: folding one machine changed another:\n%s", text)
	}

	// Enter on the row opens it again.
	m.EnterSidebarFocus()
	for i, nav := range m.SidebarNav {
		if nav.Kind == sidebarRowHost && nav.SessionID == "build" {
			m.SidebarCursor = i
		}
	}
	m.SidebarActivateCursor()
	if m.SidebarHostCollapsed("build") {
		t.Fatal("ASSERTION: enter on the folded header did not open the group")
	}
	if text := hostRailText(t, m); !strings.Contains(text, "api") {
		t.Errorf("ASSERTION: the opened group does not list its sessions:\n%s", text)
	}
}

// TestFoldedHostOffersNoNewControl keeps the "+" off a shut group: the count
// takes that slot, and a person opens the group before adding to it.
func TestFoldedHostOffersNoNewControl(t *testing.T) {
	m := hostRailOS(t)
	m.SidebarToggleHostCollapsed("build")
	m.sidebarPanelLines()
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowHostNew && h.SessionID == "build" {
			t.Errorf("ASSERTION: a folded host offers a + control")
		}
	}
	m.SidebarToggleHostCollapsed("build")
	m.sidebarPanelLines()
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowHostNew && h.SessionID == "build" {
			found = true
		}
	}
	if !found {
		t.Errorf("ASSERTION: an open up host offers no + control")
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

// TestHostHeaderIsAFoldTarget makes the header row itself reachable: it is a
// nav row of its own kind, distinct from its + control, on every machine
// including the one the client is on.
func TestHostHeaderIsAFoldTarget(t *testing.T) {
	m := hostRailOS(t)
	if _, w := m.sidebarPanelLines(); w <= 0 {
		t.Fatal("the rail drew nothing")
	}
	seen := map[string]bool{}
	for _, nav := range m.SidebarNav {
		if nav.Kind == sidebarRowHost {
			seen[nav.SessionID] = true
		}
	}
	for _, host := range []string{"local", "build", "workstation"} {
		if !seen[host] {
			t.Errorf("ASSERTION: the %s header is not a keyboard target", host)
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

// TestHostsChangedPushStartsThePollAgain is the refresh fix: a client whose
// daemon had no hosts has stopped polling, and the daemon's push is what makes
// it ask once more. Without it the first host added from the command line
// stayed invisible until the client reattached.
func TestHostsChangedPushStartsThePollAgain(t *testing.T) {
	m := sidebarTestOS(t, 120, 40, "left")
	m.federationPolling = true
	m.applyFederationSnapshot(FederationHostsMsg{Configured: 0})
	if m.federationPolling {
		t.Fatal("the fixture is still polling, so this proves nothing")
	}

	_, cmd := m.Update(HostsChangedMsg{})
	if !m.federationPolling {
		t.Error("ASSERTION: the hosts-changed push did not arm the poll")
	}
	if cmd == nil {
		t.Error("ASSERTION: the hosts-changed push returned no poll")
	}

	// The wiring: the client event the daemon's push queues becomes that
	// message, not the leave event the channel's default case makes.
	ch := make(chan ClientEvent, 1)
	ch <- ClientEvent{Type: "hosts-changed"}
	if _, ok := ListenForClientEvents(ch)().(HostsChangedMsg); !ok {
		t.Error("ASSERTION: a hosts-changed client event is not delivered as HostsChangedMsg")
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

// TestHostGroupNodesShapeTheRows checks the row list itself: one header per
// other machine, then that machine's sessions, and a host that failed
// contributing its header alone.
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

// TestFoldedAttachedMachineKeepsTheFocusMark: folding the group the client is
// on hides the session row that wears the focus mark, so the header takes it.
// The fold must not make "where am I" disappear from the rail. herdr answers
// the same case by highlighting the folded machine's row; the rail's own
// vocabulary for "you are here" is the gutter mark.
func TestFoldedAttachedMachineKeepsTheFocusMark(t *testing.T) {
	m := hostRailOS(t)
	mark := m.Settings.GetRailFocusMark()
	lines := railText(t, m)
	at := railLineIndex(lines, hostHeader(m, "local", false))
	if at < 0 || strings.HasPrefix(lines[at], mark) {
		t.Fatalf("an open group's header wears the focus mark, or is missing: %d %q", at, lines)
	}

	m.SidebarToggleHostCollapsed(federation.LocalHostName)
	lines = railText(t, m)
	at = railLineIndex(lines, hostHeader(m, "local", true))
	if at < 0 {
		t.Fatalf("the folded header is missing:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[at], mark) {
		t.Errorf("ASSERTION: the folded header of the attached machine does not wear the focus mark: %q", lines[at])
	}
	// Another machine's folded header does not.
	m.SidebarToggleHostCollapsed("build")
	lines = railText(t, m)
	at = railLineIndex(lines, hostHeader(m, "build", true))
	if at >= 0 && strings.HasPrefix(lines[at], mark) {
		t.Errorf("ASSERTION: a folded machine the client is not on wears the focus mark: %q", lines[at])
	}
}

// TestKeyboardReordersMachines is the keyboard twin of the header drag: the
// reorder key on a machine's header moves it among the other machines and
// persists the order, and it does nothing on this machine's header.
func TestKeyboardReordersMachines(t *testing.T) {
	m := hostRailOS(t)
	m.sidebarPanelLines()
	m.EnterSidebarFocus()
	cursorOn := func(host string) {
		t.Helper()
		m.sidebarPanelLines()
		for i, nav := range m.SidebarNav {
			if nav.Kind == sidebarRowHost && nav.SessionID == host {
				m.SidebarCursor = i
				return
			}
		}
		t.Fatalf("no header row for %s in the nav list", host)
	}
	cursorOn("build")
	m.SidebarReorderCursor(1)
	if got := strings.Join(m.SidebarHostOrder, ","); got != "workstation,build" {
		t.Fatalf("ASSERTION: the reorder key left the machine order %q, want workstation,build", got)
	}
	lines := railText(t, m)
	if railLineIndex(lines, hostHeader(m, "workstation", false)) > railLineIndex(lines, hostHeader(m, "build", false)) {
		t.Errorf("ASSERTION: the rail did not take the keyboard order:\n%s", strings.Join(lines, "\n"))
	}
	cursorOn(federation.LocalHostName)
	m.SidebarReorderCursor(1)
	if got := strings.Join(m.SidebarHostOrder, ","); got != "workstation,build" {
		t.Errorf("ASSERTION: the reorder key moved this machine: %q", got)
	}
	if railLineIndex(railText(t, m), hostHeader(m, "local", false)) != 1 {
		t.Errorf("ASSERTION: this machine is no longer first:\n%s", strings.Join(railText(t, m), "\n"))
	}
}
