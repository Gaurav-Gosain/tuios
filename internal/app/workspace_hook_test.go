package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("after-workspace-switch hook never ran")
}

// Switching to the workspace already shown is a no-op, so it must not fire.
func TestSwitchToSameWorkspaceDoesNotFireHook(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "switched")

	mgr := hooks.NewManager()
	mgr.Register(hooks.AfterWorkspaceSwitch, "touch "+marker)

	m := &OS{
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

	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("hook fired for a switch to the workspace already shown")
	}
}
