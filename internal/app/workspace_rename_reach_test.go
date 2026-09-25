package app

import (
	"testing"
)

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
	m.ClearMenuTarget()
	m.CurrentWorkspace = 3
	m.BeginRenameCurrentWorkspace()
	if m.RenameTargetID != "3" {
		t.Errorf("a rename with no menu behind it targeted %q, want the current workspace 3", m.RenameTargetID)
	}
}
