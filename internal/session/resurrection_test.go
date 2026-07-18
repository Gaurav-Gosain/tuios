package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResurrectionRoundTripMultiWindowMultiWorkspace saves a session spanning
// several windows across multiple workspaces (with tiling, focus, cwds and a
// BSP tree) and verifies every structural field survives a save/load cycle.
func TestResurrectionRoundTripMultiWindowMultiWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	original := &SessionState{
		Name:             "multi",
		CurrentWorkspace: 2,
		AutoTiling:       true,
		MasterRatio:      0.65,
		Width:            200,
		Height:           60,
		Mode:             1,
		FocusedWindowID:  "win-c",
		Windows: []WindowState{
			{ID: "win-a", Title: "shell", CustomName: "main", X: 0, Y: 0, Width: 100, Height: 30, Z: 0, Workspace: 1, PTYID: "pty-a", Cwd: "/home/user/project"},
			{ID: "win-b", Title: "logs", X: 100, Y: 0, Width: 100, Height: 30, Z: 1, Workspace: 1, PTYID: "pty-b", Cwd: "/var/log"},
			{ID: "win-c", Title: "vim", X: 0, Y: 0, Width: 200, Height: 60, Z: 2, Workspace: 2, PTYID: "pty-c", IsAltScreen: true, Cwd: "/tmp/edit"},
		},
		WorkspaceFocus: map[int]string{
			1: "win-b",
			2: "win-c",
		},
		WindowToBSPID:   map[string]int{"win-a": 1, "win-b": 2},
		NextBSPWindowID: 3,
		TilingScheme:    1,
		WorkspaceTrees: map[int]*SerializedBSPTree{
			1: {
				AutoScheme:   1,
				DefaultRatio: 0.5,
				Root: &SerializedBSPNode{
					SplitType:  1,
					SplitRatio: 0.6,
					Left:       &SerializedBSPNode{WindowID: 1},
					Right:      &SerializedBSPNode{WindowID: 2},
				},
			},
		},
	}

	if err := SaveSessionForResurrection(original); err != nil {
		t.Fatalf("SaveSessionForResurrection failed: %v", err)
	}

	loaded, err := LoadResurrectionState("multi")
	if err != nil {
		t.Fatalf("LoadResurrectionState failed: %v", err)
	}

	if loaded.ResurrectionVersion != ResurrectionVersion {
		t.Errorf("ResurrectionVersion = %d, want %d", loaded.ResurrectionVersion, ResurrectionVersion)
	}
	if loaded.CurrentWorkspace != 2 || !loaded.AutoTiling || loaded.MasterRatio != 0.65 {
		t.Errorf("top-level fields not preserved: %+v", loaded)
	}
	if loaded.FocusedWindowID != "win-c" {
		t.Errorf("FocusedWindowID = %q, want win-c", loaded.FocusedWindowID)
	}
	if len(loaded.Windows) != 3 {
		t.Fatalf("windows = %d, want 3", len(loaded.Windows))
	}
	for i, w := range loaded.Windows {
		o := original.Windows[i]
		if w.ID != o.ID || w.Workspace != o.Workspace || w.PTYID != o.PTYID || w.Cwd != o.Cwd {
			t.Errorf("window %d mismatch: got %+v want %+v", i, w, o)
		}
	}
	if loaded.Windows[2].IsAltScreen != true {
		t.Error("alt screen flag not preserved for win-c")
	}
	// Workspaces are distinct.
	ws := map[int]bool{}
	for _, w := range loaded.Windows {
		ws[w.Workspace] = true
	}
	if len(ws) != 2 {
		t.Errorf("expected windows across 2 workspaces, got %d", len(ws))
	}
	if loaded.WorkspaceFocus[1] != "win-b" || loaded.WorkspaceFocus[2] != "win-c" {
		t.Errorf("WorkspaceFocus not preserved: %+v", loaded.WorkspaceFocus)
	}
	tree := loaded.WorkspaceTrees[1]
	if tree == nil || tree.Root == nil || tree.Root.Left == nil || tree.Root.Right == nil {
		t.Fatalf("BSP tree not preserved: %+v", loaded.WorkspaceTrees)
	}
	if tree.Root.SplitRatio != 0.6 || tree.Root.Left.WindowID != 1 || tree.Root.Right.WindowID != 2 {
		t.Errorf("BSP tree content mismatch: %+v", tree.Root)
	}
	if loaded.NextBSPWindowID != 3 || loaded.WindowToBSPID["win-a"] != 1 {
		t.Errorf("BSP id mapping not preserved: next=%d map=%+v", loaded.NextBSPWindowID, loaded.WindowToBSPID)
	}
}

// TestLoadResurrectionCorruptFileArchived verifies a corrupt state file is
// archived (not deleted, not fatal) and reported as an error.
func TestLoadResurrectionCorruptFileArchived(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	path := filepath.Join(tmpDir, "broken.json")
	if err := os.WriteFile(path, []byte("{ this is not valid json"), 0600); err != nil {
		t.Fatalf("failed to seed corrupt file: %v", err)
	}

	_, err := LoadResurrectionState("broken")
	if err == nil {
		t.Fatal("expected error loading corrupt state")
	}

	// Original must be gone (moved to archive), not left to trip up every load.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("corrupt file still present at original path (stat err: %v)", statErr)
	}

	// A .bak copy must exist in the archive directory.
	archiveDir := filepath.Join(tmpDir, "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("archive dir not created: %v", err)
	}
	found := false
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an archived .bak file, got %d entries", len(entries))
	}

	// The corrupt session must not appear as resurrectable.
	infos, err := ListResurrectableInfos()
	if err != nil {
		t.Fatalf("ListResurrectableInfos failed: %v", err)
	}
	for _, info := range infos {
		if info.Name == "broken" {
			t.Errorf("corrupt session should not be listed as resurrectable")
		}
	}
}

// TestLoadResurrectionIncompatibleVersionArchived verifies state written by a
// newer, incompatible schema is archived and refused.
func TestLoadResurrectionIncompatibleVersionArchived(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	// Write a state file directly with a future version. Bypass Save (which
	// would stamp the current version) by writing JSON by hand.
	path := filepath.Join(tmpDir, "future.json")
	future := `{"name":"future","resurrection_version":999,"windows":[]}`
	if err := os.WriteFile(path, []byte(future), 0600); err != nil {
		t.Fatalf("failed to seed future file: %v", err)
	}

	_, err := LoadResurrectionState("future")
	if err == nil {
		t.Fatal("expected error loading incompatible-version state")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("incompatible file still present at original path")
	}
}

// TestSaveStampsCurrentVersion verifies every save is self-describing even when
// the in-memory state has no version set (clients never set it).
func TestSaveStampsCurrentVersion(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	state := &SessionState{Name: "stamp"} // ResurrectionVersion left at 0
	if err := SaveSessionForResurrection(state); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	// Save mutates the passed state's version in place.
	if state.ResurrectionVersion != ResurrectionVersion {
		t.Errorf("save did not stamp version on in-memory state: %d", state.ResurrectionVersion)
	}
	loaded, err := LoadResurrectionState("stamp")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.ResurrectionVersion != ResurrectionVersion {
		t.Errorf("loaded version = %d, want %d", loaded.ResurrectionVersion, ResurrectionVersion)
	}
}

func TestSaveAndLoadResurrection(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	state := &SessionState{
		Name:             "test-session",
		CurrentWorkspace: 2,
		AutoTiling:       true,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "win1", Title: "Shell", Workspace: 0, X: 0, Y: 0, Width: 60, Height: 40},
			{ID: "win2", Title: "Editor", Workspace: 0, X: 60, Y: 0, Width: 60, Height: 40},
		},
	}

	// Save
	err := SaveSessionForResurrection(state)
	if err != nil {
		t.Fatalf("SaveSessionForResurrection failed: %v", err)
	}

	// Verify file exists
	path := filepath.Join(tmpDir, "test-session.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("resurrection file not created")
	}

	// Load
	loaded, err := LoadResurrectionState("test-session")
	if err != nil {
		t.Fatalf("LoadResurrectionState failed: %v", err)
	}

	if loaded.Name != "test-session" {
		t.Errorf("expected name 'test-session', got %q", loaded.Name)
	}
	if loaded.CurrentWorkspace != 2 {
		t.Errorf("expected workspace 2, got %d", loaded.CurrentWorkspace)
	}
	if len(loaded.Windows) != 2 {
		t.Errorf("expected 2 windows, got %d", len(loaded.Windows))
	}
}

func TestListResurrectableSessions(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	// Save two sessions
	_ = SaveSessionForResurrection(&SessionState{Name: "session-a"})
	_ = SaveSessionForResurrection(&SessionState{Name: "session-b"})

	names, err := ListResurrectableSessions()
	if err != nil {
		t.Fatalf("ListResurrectableSessions failed: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(names))
	}
}

func TestRemoveResurrectionState(t *testing.T) {
	tmpDir := t.TempDir()
	resurrectionDirOverride = tmpDir
	defer func() { resurrectionDirOverride = "" }()

	_ = SaveSessionForResurrection(&SessionState{Name: "to-remove"})

	RemoveResurrectionState("to-remove")

	_, err := LoadResurrectionState("to-remove")
	if err == nil {
		t.Error("expected error loading removed session")
	}
}
