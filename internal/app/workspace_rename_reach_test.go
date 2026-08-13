package app

import (
	"strconv"
	"strings"
	"testing"
)

// TestWorkspacePillMenuOffersRename is the discoverability fix, checked where a
// user would find it: right-click a workspace tab and the menu that opens says
// what that tab is called and offers to rename it.
//
// The menu is opened at every column of every pill, both edges included, off
// the rectangles the renderer recorded as it drew. A menu that only opens in a
// pill's middle is a menu with an unclickable border, which is the failure this
// strip has had before.
func TestWorkspacePillMenuOffersRename(t *testing.T) {
	names := map[int]string{2: "review", 4: "deploy-fix"}
	for _, capped := range []bool{false, true} {
		t.Run(strconv.FormatBool(capped), func(t *testing.T) {
			build := pillOS
			if capped {
				build = pillCapsOS
			}
			m := build(t, 120, names, 1, 2, 3, 4)
			dockBarRow(t, m) // draw, so the strip records its rectangles
			if len(m.dockWorkspaceHits) == 0 {
				t.Fatal("the strip recorded no rectangles")
			}

			for _, h := range m.dockWorkspaceHits {
				for x := h.X0; x < h.X1; x++ {
					m.CloseContextMenu()
					m.OpenContextMenu(x, h.Y)
					cm := m.ContextMenu
					if cm == nil {
						t.Fatalf("column %d of workspace %d opened no menu", x, h.Workspace)
					}
					if h.Workspace == 0 {
						// The "+" tab is a workspace that does not exist yet.
						if cm.Target == CtxTargetWorkspacePill {
							t.Errorf("column %d of the + tab opened a pill menu", x)
						}
						continue
					}
					if cm.Target != CtxTargetWorkspacePill {
						t.Fatalf("column %d of workspace %d opened target %d, want the pill menu",
							x, h.Workspace, cm.Target)
					}
					if cm.Workspace != h.Workspace {
						t.Fatalf("the menu at column %d points at workspace %d, want %d",
							x, cm.Workspace, h.Workspace)
					}
					if !menuHasAction(cm, "workspace_prefix_rename") {
						t.Fatalf("the pill menu offers no rename: %+v", cm.Items)
					}
					if want := m.workspacePillLabel(h.Workspace); !strings.Contains(cm.Title, want) {
						t.Errorf("the menu is titled %q, want it to name the workspace (%q)", cm.Title, want)
					}
				}
			}
		})
	}
}

// TestWorkspacePillMenuRenamesThePillItWasOpenedOn: the row runs after the menu
// has closed, so the workspace has to survive that gap, and it must not outlive
// it and hijack a later rename reached by key.
func TestWorkspacePillMenuRenamesThePillItWasOpenedOn(t *testing.T) {
	m := pillOS(t, 120, map[int]string{2: "review"}, 1, 2, 3)
	dockBarRow(t, m)

	var pill dockWorkspaceHit
	for _, h := range m.dockWorkspaceHits {
		if h.Workspace == 2 {
			pill = h
		}
	}
	if pill.Workspace != 2 {
		t.Fatal("workspace 2 drew no pill")
	}

	m.OpenContextMenu(pill.X0, pill.Y)
	action := m.ContextMenuSelectedActionAt(t, "workspace_prefix_rename")
	m.CloseContextMenu()
	if action != "workspace_prefix_rename" {
		t.Fatalf("selected action = %q", action)
	}

	m.BeginRenameCurrentWorkspace()
	if m.RenameKind != RenameWorkspace || m.RenameTargetID != "2" {
		t.Fatalf("rename = {kind:%v target:%q}, want workspace 2 even though the user is on 1",
			m.RenameKind, m.RenameTargetID)
	}
	if m.RenameBuffer != "review" {
		t.Errorf("editor seeded with %q, want the pill's current name", m.RenameBuffer)
	}
	m.EndRename()

	// With no menu behind it, the same entry point renames the workspace in view.
	m.ClearMenuWorkspace()
	m.CurrentWorkspace = 3
	m.BeginRenameCurrentWorkspace()
	if m.RenameTargetID != "3" {
		t.Errorf("a rename with no menu behind it targeted %q, want the current workspace 3", m.RenameTargetID)
	}
}

// menuHasAction reports whether any row carries the action.
func menuHasAction(cm *ContextMenu, action string) bool {
	for _, it := range cm.Items {
		if it.Action == action {
			return true
		}
	}
	return false
}

// ContextMenuSelectedActionAt selects the row carrying action and takes it the
// way the keyboard path does, so the carry is set exactly as it is in use.
func (m *OS) ContextMenuSelectedActionAt(t *testing.T, action string) string {
	t.Helper()
	cm := m.ContextMenu
	for i, it := range cm.Items {
		if it.Action == action {
			cm.Selected = i
			return m.ContextMenuSelectedAction()
		}
	}
	t.Fatalf("no row carries %q", action)
	return ""
}
