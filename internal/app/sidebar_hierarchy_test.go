package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The rail's two levels: a machine is a heading and a session is an item under
// it. The e2e suite reads the weight off a real grid; these read the geometry,
// which is the half a unit test can hold honestly.

// hierarchyOS is a rail attached to one session on this machine, with one
// other machine holding one session.
func hierarchyOS(t *testing.T) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, 120, 30)
	m.CurrentWorkspace = 1
	m.SessionName = "tuios"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "nvim", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil
	m.SidebarCollapsedHosts = nil
	m.SidebarCollapsedRepos = nil
	m.applyFederationSnapshot(FederationHostsMsg{
		Configured: 1,
		Snapshot: FederationSnapshot{Hosts: []FederationHost{
			{Name: federation.LocalHostName, Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "tuios", WindowCount: 1}}},
			{Name: "oci", Status: string(federation.StatusUp),
				Sessions: []FederationSession{{Name: "session-0", WindowCount: 1}}},
		}},
	})
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "tuios", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
		{Name: "site-feat-one", Worktree: &sessiontree.WorktreeRef{Repo: "site", Branch: "feat/one"}},
		{Name: "site-feat-two", Worktree: &sessiontree.WorktreeRef{Repo: "site", Branch: "feat/two"}},
	})
	tree.Sessions = append(tree.Sessions, m.hostGroupNodes()...)
	return m, tree
}

// colOf is the column name starts at on the rail line holding it, or -1.
func colOf(lines []string, name string) int {
	for _, l := range lines {
		if i := strings.Index(l, name); i >= 0 {
			return len([]rune(l[:i]))
		}
	}
	return -1
}

// TestRailStepsAMachinesRowsInUnderItsHeading is the geometry of the fix: with
// more than one machine on the rail every session row of the sessions section
// steps in under the machine that holds it, and the machine's own name stays
// on the rail's name column.
func TestRailStepsAMachinesRowsInUnderItsHeading(t *testing.T) {
	m, tree := hierarchyOS(t)
	lines := railPlain(t, m, tree)

	head := colOf(lines, "local")
	if head != sidebarNameCol {
		t.Fatalf("ASSERTION: a machine's name starts at column %d, want the name column %d:\n%s",
			head, sidebarNameCol, strings.Join(lines, "\n"))
	}
	for _, name := range []string{"tuios", "session-0", "site"} {
		if got := colOf(lines, name); got != head+sidebarGroupIndent {
			t.Errorf("ASSERTION: %q starts at column %d, want %d, a step in under its machine:\n%s",
				name, got, head+sidebarGroupIndent, strings.Join(lines, "\n"))
		}
	}
	// The other machine's heading is on the same column as this one's: they are
	// the same level, and a heading that drifted would read as a child.
	if got := colOf(lines, "oci"); got != head {
		t.Errorf("ASSERTION: the second machine's name starts at column %d, want %d:\n%s",
			got, head, strings.Join(lines, "\n"))
	}
}

// TestRailKeepsWorktreeMarksUnderAMachine is the answer to "the same rail
// answers the same question twice". It does not: the step says a row belongs
// to the heading above it, and the tree marks say a row's name is a branch of
// the row above it and where that group ends. Under a machine, a worktree
// session wears both, one after the other, and nothing is drawn twice.
func TestRailKeepsWorktreeMarksUnderAMachine(t *testing.T) {
	m, tree := hierarchyOS(t)
	lines := railPlain(t, m, tree)

	repo := colOf(lines, "site")
	one := colOf(lines, "├─ feat/one")
	last := colOf(lines, "└─ feat/two")
	if repo < 0 || one < 0 || last < 0 {
		t.Fatalf("ASSERTION: the repository group is not on the rail:\n%s", strings.Join(lines, "\n"))
	}
	// The tree mark starts where its repository's name does: the repository is
	// an item, and its members hang off it rather than off the machine.
	if one != repo || last != repo {
		t.Errorf("ASSERTION: the tree marks start at %d and %d, want the repository's own column %d:\n%s",
			one, last, repo, strings.Join(lines, "\n"))
	}
}

// TestRailOfOneMachineTakesNoStep is the default install. With no other
// machine there are no headings, so there is nothing to step in under and the
// rail is the one it always was.
func TestRailOfOneMachineTakesNoStep(t *testing.T) {
	m, tree := hierarchyOS(t)
	m.FederationHosts = nil
	tree.Sessions = tree.Sessions[:len(localSessionNodes(tree.Sessions))]
	lines := railPlain(t, m, tree)

	if col := colOf(lines, "tuios"); col != sidebarNameCol {
		t.Errorf("ASSERTION: on a rail with one machine the session starts at column %d, want %d:\n%s",
			col, sidebarNameCol, strings.Join(lines, "\n"))
	}
	if m.sidebarRowIndent() != 0 {
		t.Errorf("ASSERTION: a rail with one machine takes a step of %d, want none", m.sidebarRowIndent())
	}
}

// TestMachineMenuOffersItsMoves is the affordance. Reordering the machines
// already worked by drag and nothing on screen said so, so the header's own
// menu carries the two moves, and this machine's rows are dimmed rather than
// missing.
func TestMachineMenuOffersItsMoves(t *testing.T) {
	m, tree := hierarchyOS(t)
	railPlain(t, m, tree)

	title, items := m.machineMenu("oci")
	if title != "oci" {
		t.Errorf("ASSERTION: the menu is titled %q, want the machine it acts on", title)
	}
	var actions []string
	for _, it := range items {
		actions = append(actions, it.Action)
	}
	if len(actions) != 2 || actions[0] != "reorder_up" || actions[1] != "reorder_down" {
		t.Fatalf("ASSERTION: a machine's menu names %v, want the two reorder actions", actions)
	}
	// One machine besides this one, so it can move neither way.
	if !items[0].Dim || !items[1].Dim {
		t.Errorf("ASSERTION: the only other machine can move up or down, and it has nowhere to go")
	}

	_, local := m.machineMenu(federation.LocalHostName)
	if !local[0].Dim || !local[1].Dim {
		t.Errorf("ASSERTION: this machine is pinned first and its move rows are live")
	}
}

// TestReorderingThisMachineSaysWhyItWillNotMove is the other half of the
// affordance: the refusal used to draw nothing, which reads as a rail that
// cannot be reordered at all.
func TestReorderingThisMachineSaysWhyItWillNotMove(t *testing.T) {
	m, tree := hierarchyOS(t)
	railPlain(t, m, tree)

	m.sidebarReorderHost(federation.LocalHostName, 1)
	said := ""
	if n := len(m.Notifications); n > 0 {
		said = m.Notifications[n-1].Message
	}
	if !strings.Contains(said, "stays first") {
		t.Errorf("ASSERTION: moving this machine said %q, want a refusal naming the rule", said)
	}
}
