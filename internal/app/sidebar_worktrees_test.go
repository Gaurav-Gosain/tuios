package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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

// TestRailGroupsWorktreeSessionsUnderTheirRepository is the whole promise on
// screen: a parent row per repository, its sessions under it labelled by
// branch and marked with the tree glyphs, and a session that is not a worktree
// untouched.
func TestRailGroupsWorktreeSessionsUnderTheirRepository(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 30)
	lines := railPlain(t, m, tree)

	tuios, _ := railRowLine(t, m, lines, sidebarRowRepo, "tuios")
	if !strings.Contains(tuios, "tuios") {
		t.Errorf("the tuios group header does not name its repository: %q", tuios)
	}
	docs, _ := railRowLine(t, m, lines, sidebarRowRepo, "docs")
	if !strings.Contains(docs, "docs") {
		t.Errorf("the docs group header does not name its repository: %q", docs)
	}

	one, _ := railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-one")
	if !strings.Contains(one, "├─ feat/one") {
		t.Errorf("the first worktree row is %q, want the branch behind the ├─ mark", one)
	}
	two, _ := railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-two")
	if !strings.Contains(two, "└─ feat/two") {
		t.Errorf("the last worktree row is %q, want the branch behind the └─ mark", two)
	}
	only, _ := railRowLine(t, m, lines, sidebarRowSession, "docs-fix")
	if !strings.Contains(only, "└─ fix/typo") {
		t.Errorf("a lone worktree row is %q, want the branch behind the └─ mark that closes its group", only)
	}

	// The control: a session with no worktree record is a plain row under no
	// parent, wearing neither mark.
	plain, _ := railRowLine(t, m, lines, sidebarRowSession, "main")
	if strings.Contains(plain, "├─") || strings.Contains(plain, "└─") {
		t.Errorf("the plain session row wears a tree mark: %q", plain)
	}
	if !strings.Contains(plain, "main") {
		t.Errorf("the plain session row lost its name: %q", plain)
	}

	// The parent lands above the members it holds.
	_, parent := railRowLine(t, m, lines, sidebarRowRepo, "tuios")
	_, first := railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-one")
	if parent.Y0 >= first.Y0 {
		t.Errorf("the tuios group header is on line %d and its first member on %d, want the header above", parent.Y0, first.Y0)
	}
}

// TestRailCollapsesAWorktreeGroup drives the fold the way a pointer does and
// asserts what the shut group says: no members, how many it is holding, and
// the worst state among them.
func TestRailCollapsesAWorktreeGroup(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 30)
	lines := railPlain(t, m, tree)

	errored := agentStateIndicator("errored")
	open, hit := railRowLine(t, m, lines, sidebarRowRepo, "tuios")
	if strings.Contains(open, errored) {
		t.Errorf("the open group header carries the roll-up glyph %q: %q; its members carry their own", errored, open)
	}

	// Exactly what the mouse path runs on a completed click.
	m.sidebarActivateRow(hit)
	if !m.SidebarRepoCollapsed("tuios") {
		t.Fatal("activating the group header did not fold the group")
	}
	lines = railPlain(t, m, tree)

	if lineOf(lines, "feat/one") >= 0 || lineOf(lines, "feat/two") >= 0 {
		t.Errorf("a folded group still draws its members:\n%s", strings.Join(lines, "\n"))
	}
	shut, hit := railRowLine(t, m, lines, sidebarRowRepo, "tuios")
	if !strings.Contains(shut, "2") {
		t.Errorf("the folded group header is %q, want the count of the 2 rows it hides", shut)
	}
	if !strings.Contains(shut, errored) {
		t.Errorf("the folded group header is %q, want the %q of its errored member: a shut group must still raise an alarm", shut, errored)
	}
	// The other repository is untouched by the fold.
	if lineOf(lines, "fix/typo") < 0 {
		t.Errorf("folding one repository hid another's rows:\n%s", strings.Join(lines, "\n"))
	}

	// And it opens again on the same gesture.
	m.sidebarActivateRow(hit)
	lines = railPlain(t, m, tree)
	if lineOf(lines, "feat/one") < 0 {
		t.Errorf("the group did not open again:\n%s", strings.Join(lines, "\n"))
	}
}

// TestRailWorktreeGroupIsReachableByKeyboard checks the parent is a nav row
// like any other: the cursor lands on it, and enter folds the group.
func TestRailWorktreeGroupIsReachableByKeyboard(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 30)
	m.SidebarFocused = true
	railPlain(t, m, tree)

	idx := -1
	for i, r := range m.SidebarNav {
		if r.Kind == sidebarRowRepo && r.SessionID == "tuios" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("the group header is not among the %d nav rows, so the cursor can never reach it", len(m.SidebarNav))
	}
	m.SidebarCursor = idx
	if leave := m.SidebarActivateCursor(); leave {
		t.Error("folding a group asked the rail to give the keyboard back; it is not a place to go")
	}
	if !m.SidebarRepoCollapsed("tuios") {
		t.Fatal("enter on the group header did not fold the group")
	}
	lines := railPlain(t, m, tree)
	if lineOf(lines, "feat/one") >= 0 {
		t.Errorf("the group folded by keyboard still draws its members:\n%s", strings.Join(lines, "\n"))
	}
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

// TestRailWorktreeRowSaysTheDirectoryIsGone covers the one tag a child row
// carries: the session runs on, and its worktree directory does not exist.
func TestRailWorktreeRowSaysTheDirectoryIsGone(t *testing.T) {
	m, _ := worktreeRailOS(t, 120, 30)

	lines := railPlain(t, m, worktreeTree(false))
	row, _ := railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-two")
	if strings.Contains(row, sidebarWorktreeGoneTag) {
		t.Fatalf("a worktree whose directory is there is tagged gone: %q", row)
	}

	lines = railPlain(t, m, worktreeTree(true))
	row, _ = railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-two")
	if !strings.Contains(row, sidebarWorktreeGoneTag) {
		t.Errorf("the row of a removed worktree is %q, want the %q tag", row, sidebarWorktreeGoneTag)
	}
	if !strings.Contains(row, "feat/two") {
		t.Errorf("the tag took the branch off the row: %q", row)
	}
}

// TestRailWorktreeMarksFallBackToASCII checks the two marks are glyph
// settings and not literals: --ascii-only draws the 7-bit pair.
func TestRailWorktreeMarksFallBackToASCII(t *testing.T) {
	prevCfg, prevOverlay := config.Global.UseASCIIOnly, overlay.UseASCII()
	config.Global.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.Global.UseASCIIOnly = prevCfg
		overlay.SetASCII(prevOverlay)
	})

	m, tree := worktreeRailOS(t, 120, 30)
	m.Settings = config.Global
	lines := railPlain(t, m, tree)

	one, _ := railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-one")
	if !strings.Contains(one, "|- feat/one") {
		t.Errorf("under --ascii-only the first worktree row is %q, want the ASCII |- mark", one)
	}
	two, _ := railRowLine(t, m, lines, sidebarRowSession, "tuios-feat-two")
	if !strings.Contains(two, "`- feat/two") {
		t.Errorf("under --ascii-only the last worktree row is %q, want the ASCII `- mark", two)
	}
	for _, l := range lines {
		if strings.Contains(l, "├") || strings.Contains(l, "└") {
			t.Errorf("a box-drawing mark survived --ascii-only: %q", l)
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
