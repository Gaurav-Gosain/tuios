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
