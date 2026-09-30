package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// after-workspace-switch was a valid, documented hook event that config
// validation accepted and nothing ever raised, so a user's command sat in
// config.toml doing nothing. SwitchToWorkspace now fires it.
func TestSwitchToWorkspaceFiresHook(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "switched")

	mgr := hooks.NewManager()
	mgr.Register(hooks.AfterWorkspaceSwitch, "touch "+marker)

	m := &OS{
		Settings:             config.Global,
		HookManager:          mgr,
		NumWorkspaces:        4,
		CurrentWorkspace:     1,
		FocusedWindow:        -1,
		WorkspaceFocus:       map[int]int{},
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
	}

	m.SwitchToWorkspace(2)

	if m.CurrentWorkspace != 2 {
		t.Fatalf("CurrentWorkspace = %d, want 2", m.CurrentWorkspace)
	}

	// FireHookContext hands the hook to the manager before it returns, and
	// Wait joins every hook fired so far.
	mgr.Wait()
	if _, err := os.Stat(marker); err != nil {
		t.Error("after-workspace-switch hook never ran")
	}
}

// Switching to the workspace already shown is a no-op, so it must not fire.
func TestSwitchToSameWorkspaceDoesNotFireHook(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "switched")

	mgr := hooks.NewManager()
	mgr.Register(hooks.AfterWorkspaceSwitch, "touch "+marker)

	m := &OS{
		Settings:             config.Global,
		HookManager:          mgr,
		NumWorkspaces:        4,
		CurrentWorkspace:     1,
		FocusedWindow:        -1,
		WorkspaceFocus:       map[int]int{},
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
	}

	m.SwitchToWorkspace(1)

	mgr.Wait()
	if _, err := os.Stat(marker); err == nil {
		t.Error("hook fired for a switch to the workspace already shown")
	}
}
