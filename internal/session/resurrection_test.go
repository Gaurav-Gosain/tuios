package session

import (
	"os"
	"path/filepath"
	"testing"
)

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
