package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestWorkspaceRenameChordOpensTheEditor is the keyboard entry point. Renaming
// a workspace existed but was reachable only from inside the workspace
// switcher, which is not where a user looks for it.
func TestWorkspaceRenameChordOpensTheEditor(t *testing.T) {
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.NumWorkspaces = 9
	o.CurrentWorkspace = 3
	o.WorkspacePrefixActive = true

	o, _ = HandleWorkspacePrefixCommand(press("r"), o)

	if !o.Renaming() {
		t.Fatal("the workspace prefix chord did not open the rename editor")
	}
	if o.RenameKind != app.RenameWorkspace || o.RenameTargetID != "3" {
		t.Fatalf("rename = {kind:%v target:%q}, want the workspace in view", o.RenameKind, o.RenameTargetID)
	}
	if o.WorkspacePrefixActive {
		t.Error("the chord left the workspace prefix armed under the editor")
	}

	// The editor takes the keys, chord or not.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'q', Text: "q"}, o)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, o)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'a', Text: "a"}, o)
	if o.RenameBuffer != "q a" {
		t.Errorf("rename buffer = %q, want the typed name", o.RenameBuffer)
	}
}
