package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
