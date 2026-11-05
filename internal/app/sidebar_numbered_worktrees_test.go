package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// TestNumberedWorktreeRowsKeepTheTreeSpine pins the row order on a numbered
// worktree row: the tree mark leads and the number rides behind it. A number
// before the mark pushed the mark off the parent's name spine, and the
// subtree's depth read wrong against the unnumbered repository row.
func TestNumberedWorktreeRowsKeepTheTreeSpine(t *testing.T) {
	m, tree := worktreeRailOS(t, 120, 40)
	prev := config.Global.SidebarShowNumbers
	config.Global.SidebarShowNumbers = true
	m.Settings.SidebarShowNumbers = true
	t.Cleanup(func() {
		config.Global.SidebarShowNumbers = prev
		m.Settings.SidebarShowNumbers = prev
	})
	lines, _ := m.sidebarPanelLinesForTree(tree)

	var repo, child string
	for _, l := range lines {
		if strings.Contains(l, "tuios") && repo == "" {
			repo = l
		}
		if strings.Contains(l, "feat/one") && child == "" {
			child = l
		}
	}
	if repo == "" || child == "" {
		t.Fatalf("worktree rows not found:\n%s", strings.Join(lines, "\n"))
	}

	// Columns read off the stripped lines: the styled ones carry escape
	// prefixes that shift every offset.
	plainRepo, plainChild := stripANSIForTrace(repo), stripANSIForTrace(child)
	repoCol := utf8.RuneCountInString(plainRepo[:strings.Index(plainRepo, "tuios")])
	childCol := utf8.RuneCountInString(plainChild[:strings.Index(plainChild, "├─")])
	if repoCol != childCol {
		t.Errorf("the worktree's tree mark sits at column %d, the repository's name at %d:\nrepo   %q\nchild  %q", childCol, repoCol, plainRepo, plainChild)
	}
	if !strings.Contains(plainChild, "├─ 2 feat/one") {
		t.Errorf("the numbered worktree row lost its number:\n%q", plainChild)
	}
}

// TestNumberedRowsUnderANumberedHead pins the default layout: the main
// checkout's session is its repository's row, numbered, and the worktree's
// number leads its tree mark, which already hangs under the head's name. The
// swap that serves a numberless head would move the mark under the number.
func TestNumberedRowsUnderANumberedHead(t *testing.T) {
	m, _ := worktreeRailOS(t, 120, 40)
	prev := config.Global.SidebarShowNumbers
	config.Global.SidebarShowNumbers = true
	m.Settings.SidebarShowNumbers = true
	t.Cleanup(func() {
		config.Global.SidebarShowNumbers = prev
		m.Settings.SidebarShowNumbers = prev
	})
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
		{Name: "tuios-mainsess", DisplayName: "mainsess", Named: true,
			Worktree: &sessiontree.WorktreeRef{Repo: "tuios", Branch: "main", Main: true}},
		{Name: "tuios-feat-one", Worktree: &sessiontree.WorktreeRef{Repo: "tuios", Branch: "feat/one"}},
	})
	lines := railPlain(t, m, tree)

	var head, child string
	for _, l := range lines {
		if strings.Contains(l, "mainsess") && head == "" {
			head = l
		}
		if strings.Contains(l, "feat/one") && child == "" {
			child = l
		}
	}
	if head == "" || child == "" {
		t.Fatalf("the group's rows are missing:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(head, "2 mainsess") {
		t.Errorf("the main checkout's row is not the numbered head:\n%q", head)
	}
	if !strings.Contains(child, "3 └─ feat/one") {
		t.Errorf("the worktree's number no longer leads its tree mark:\n%q", child)
	}
	headCol := utf8.RuneCountInString(head[:strings.Index(head, "mainsess")])
	childCol := utf8.RuneCountInString(child[:strings.Index(child, "└─")])
	if headCol != childCol {
		t.Errorf("the worktree's tree mark sits at column %d, the head's name at %d:\nhead  %q\nchild %q", childCol, headCol, head, child)
	}
}

// TestNumberedPillRowLinesItsBarUp pins the current worktree row on the pill
// row style: swapped like its siblings, so its tree mark sits in the column
// the siblings' marks sit in and not one number-width to the right.
func TestNumberedPillRowLinesItsBarUp(t *testing.T) {
	m, _ := worktreeRailOS(t, 120, 40)
	prevNumbers, prevStyle := config.Global.SidebarShowNumbers, config.Global.RailRowStyle
	config.Global.SidebarShowNumbers = true
	config.Global.RailRowStyle = config.RailRowPill
	m.Settings.SidebarShowNumbers = true
	m.Settings.RailRowStyle = config.RailRowPill
	t.Cleanup(func() {
		config.Global.SidebarShowNumbers, config.Global.RailRowStyle = prevNumbers, prevStyle
		m.Settings.SidebarShowNumbers, m.Settings.RailRowStyle = prevNumbers, prevStyle
	})
	m.SessionName = "tuios-feat-one"
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Focused: true, Workspace: 1},
		}},
		{Name: "tuios-feat-one", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "bbbbbbbb2222", Title: "claude", AgentState: "working", Harness: "claude-code", Workspace: 1},
		}, Worktree: &sessiontree.WorktreeRef{Repo: "tuios", Branch: "feat/one"}},
		{Name: "tuios-feat-two", Worktree: &sessiontree.WorktreeRef{Repo: "tuios", Branch: "feat/two"}},
	})
	lines := railPlain(t, m, tree)

	var current, sibling string
	for _, l := range lines {
		if strings.Contains(l, "feat/one") && current == "" {
			current = l
		}
		if strings.Contains(l, "feat/two") && sibling == "" {
			sibling = l
		}
	}
	if current == "" || sibling == "" {
		t.Fatalf("the worktree rows are missing:\n%s", strings.Join(lines, "\n"))
	}
	if !strings.Contains(current, "├─2 ") {
		t.Errorf("the attached row lost the swap:\n%q", current)
	}
	one := utf8.RuneCountInString(current[:strings.Index(current, "├─")])
	two := utf8.RuneCountInString(sibling[:strings.Index(sibling, "└─")])
	if one != two {
		t.Errorf("the attached row's bar sits at column %d and its sibling's at %d:\ncurrent  %q\nsibling  %q", one, two, current, sibling)
	}
}
