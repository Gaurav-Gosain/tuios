package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// worktreeRailOS is a rail attached to a plain session, beside two
// repositories' worktree sessions: "tuios" with two branches and "docs" with
// one. It is the shape the grouping exists for, and the plain session is the
// control: nothing about it may change.
func worktreeRailOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "nvim", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	m.DaemonClient = &session.TUIClient{}
	m.IsDaemonSession = true
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil

	return m, worktreeTree(false)
}

// worktreeTree is the listing the rail is built from. gone marks the second
// tuios worktree's directory as removed.
func worktreeTree(gone bool) sessiontree.Tree {
	return sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
		{
			Name:     "tuios-feat-one",
			Worktree: &sessiontree.WorktreeRef{Repo: "tuios", Branch: "feat/one"},
			Windows: []sessiontree.WindowInput{
				{ID: "bbbbbbbb2222", Title: "claude", AgentState: "working", Harness: "claude-code", Workspace: 1},
			},
		},
		{
			Name:     "tuios-feat-two",
			Worktree: &sessiontree.WorktreeRef{Repo: "tuios", Branch: "feat/two", Gone: gone},
			Windows: []sessiontree.WindowInput{
				{ID: "cccccccc3333", Title: "claude", AgentState: "errored", Harness: "claude-code", Workspace: 1},
			},
		},
		{
			Name:     "docs-fix",
			Worktree: &sessiontree.WorktreeRef{Repo: "docs", Branch: "fix/typo"},
		},
	})
}

// railRowLine is the drawn line of the first recorded row of the given kind and
// id, and the row's hit rectangle, so a test can assert on what a person sees
// and then act on the same row a pointer would.
func railRowLine(t *testing.T, m *OS, lines []string, kind sidebarRowKind, id string) (string, sidebarRowHit) {
	t.Helper()
	for _, h := range m.SidebarHits {
		if h.Kind != kind || h.SessionID != id {
			continue
		}
		y := h.Y0 - m.GetTopMargin()
		if y < 0 || y >= len(lines) {
			t.Fatalf("row %q landed on line %d, outside the %d drawn lines", id, y, len(lines))
		}
		return lines[y], h
	}
	t.Fatalf("no row of kind %d for %q was drawn:\n%s", kind, id, strings.Join(lines, "\n"))
	return "", sidebarRowHit{}
}

// TestRailWorktreeGroupHasNoSessionMenu checks the one thing a group header
// must not do: a repository is not a session, so the right-click cannot offer
// to rename or kill one under its name.
func TestRailWorktreeGroupHasNoSessionMenu(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 30)
	lines := railPlain(t, m, tree)
	_, hit := railRowLine(t, m, lines, sidebarRowRepo, "tuios")

	m.openSidebarContextMenu(hit, hit.X0, hit.Y0)
	if m.ContextMenu == nil {
		t.Fatal("the right-click on a group header opened nothing")
	}
	if m.ContextMenu.SessionID == "tuios" {
		t.Errorf("the menu is about a session named %q, which does not exist: a repository is not a session", m.ContextMenu.SessionID)
	}
	for _, item := range m.ContextMenu.Items {
		if item.Action == "kill_session" || item.Action == "rename_session" {
			t.Errorf("the group header offers %q, which would act on a session that does not exist", item.Action)
		}
	}
}

// TestRailWorktreeFoldSurvivesARestart checks the folded set is a preference
// and not runtime state: it is written to the sidebar state file and read back.
func TestRailWorktreeFoldSurvivesARestart(t *testing.T) {
	m, _ := worktreeRailOS(t, 120, 30)
	m.SidebarToggleRepoCollapsed("tuios")

	next := newNarrowOS(t, 120, 30)
	next.loadSidebarState()
	if !next.SidebarRepoCollapsed("tuios") {
		t.Error("a folded group came back open after a restart")
	}
	if next.SidebarRepoCollapsed("docs") {
		t.Error("a group nobody folded came back folded")
	}

	m.SidebarToggleRepoCollapsed("tuios")
	again := newNarrowOS(t, 120, 30)
	again.loadSidebarState()
	if again.SidebarRepoCollapsed("tuios") {
		t.Error("a group opened again came back folded")
	}
}

// TestRailSignatureFollowsTheWorktreeGroups is the repaint guard: the render
// cache serves the last rail whenever the signature is unchanged, so anything
// that changes what is drawn has to change it. A fold that did not would look
// like a dead click.
func TestRailSignatureFollowsTheWorktreeGroups(t *testing.T) {
	m, _ := worktreeRailOS(t, 120, 30)

	before := m.sidebarSignature()
	m.SidebarToggleRepoCollapsed("tuios")
	if after := m.sidebarSignature(); after == before {
		t.Error("folding a group left the rail's signature unchanged, so the frame would be served from the cache")
	}

	m.SessionWorktree = &session.WorktreeInfo{}
	m.SessionWorktree.Repo, m.SessionWorktree.Branch = "tuios", "feat/one"
	withRecord := m.sidebarSignature()
	m.SessionWorktree.Branch = "feat/two"
	if m.sidebarSignature() == withRecord {
		t.Error("the attached session moving to another branch left the signature unchanged")
	}
}

// TestRailFlatRepoGroup is the sessions-only rail: no repository rows, no
// heads, no folds — a session is a row that says its own name, and a worktree
// is only the session's directory. Flat mode's rows are the main ones: a
// session the worktrees section lists under its repository does not also
// take a row here.
func TestRailFlatRepoGroup(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 40)
	m.Settings.SidebarRepoGroup = config.RepoGroupFlat

	lines := strings.Join(railPlain(t, m, tree), "\n")
	if !strings.Contains(lines, "main") {
		t.Errorf("the flat rail does not show the attached session:\n%s", lines)
	}
	for _, gone := range []string{"tuios-feat-one", "docs-fix"} {
		if strings.Contains(lines, gone) {
			t.Errorf("the flat rail also drew the worktree session %q; it lives in the worktrees section\n%s", gone, lines)
		}
	}
	for _, hit := range m.SidebarHits {
		if hit.Kind == sidebarRowRepo {
			t.Errorf("the flat rail drew a repository row for %q", hit.SessionID)
		}
	}
}

// TestWorktreesSectionDrawsTheTreeWhileSessionsStayFlat pins the two halves
// of the arrangement the section exists for: the sessions section draws plain
// names in flat mode, and the worktrees section draws the repository tree
// with its folds anyway — the listing is the one place the tree survives.
func TestWorktreesSectionDrawsTheTreeWhileSessionsStayFlat(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 40)
	m.Settings.SidebarRepoGroup = config.RepoGroupFlat
	m.Settings.SidebarSections = "sessions:25,worktrees"

	lines, _ := m.sidebarPanelLinesForTree(tree)
	plain := strings.Join(lines, "\n")
	// The sessions section keeps the main sessions; the section lists the
	// worktrees under their repositories, by branch.
	for _, want := range []string{"main", "├─ feat/one", "└─ fix/typo"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the rail does not show %q:\n%s", want, plain)
		}
	}
	// The tree lives in the section: repository rows, and the members under
	// them with their marks. The no-repo-rows-in-flat case is pinned by
	// TestRailFlatRepoGroup, which names no worktrees section.
	if !strings.Contains(plain, "├─") && !strings.Contains(plain, "└─") {
		t.Errorf("the worktrees section drew no tree marks:\n%s", plain)
	}
}
