package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLeftoverTempFilesAreCleaned covers the write that dies between its
// os.WriteFile and its rename.
func TestLeftoverTempFilesAreCleaned(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	// The residue of two interrupted writes, beside one good state file.
	// The first two are the fixed names older builds used, the third the
	// fresh name atomicfile gives a temporary file.
	leftovers := []string{"work.json.tmp", "notes.json.tmp", ".work.json.2840193.tmp"}
	for _, name := range leftovers {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte("{partial"), 0600); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}
	good := &SessionState{Name: "work", Windows: []WindowState{{ID: "w1"}}}
	if err := SaveSessionForResurrection(good); err != nil {
		t.Fatalf("saving the good state: %v", err)
	}

	CleanResurrectionDir()

	for _, name := range leftovers {
		if _, err := os.Stat(filepath.Join(tmpDir, name)); !os.IsNotExist(err) {
			t.Errorf("leftover temp file %s survived the sweep (stat err: %v)", name, err)
		}
	}
	// In the other state folders only an old temporary file goes: a young one
	// may be a write still in flight.
	attention := filepath.Dir(attentionPath())
	if err := os.MkdirAll(attention, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(attention, ".items.json.1187.tmp")
	young := filepath.Join(attention, ".items.json.5520.tmp")
	for _, p := range []string{stale, young} {
		if err := os.WriteFile(p, []byte("{partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	CleanResurrectionDir()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("an hour-old temp file in the attention folder survived the sweep (stat err: %v)", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("the sweep removed a temp file a write may still own: %v", err)
	}
	// The sweep must not touch real state.
	if _, err := LoadResurrectionState("work"); err != nil {
		t.Errorf("the sweep took a real state file with it: %v", err)
	}
}

// TestOldArchivedStateIsPrunedAndRecentStateIsKept covers the other unbounded
// directory. The archive is for looking at state that would not load, so recent
// entries must survive: pruning everything would defeat the point of archiving
// rather than deleting.
func TestOldArchivedStateIsPrunedAndRecentStateIsKept(t *testing.T) {
	tmpDir := t.TempDir()
	defer useResurrectionDir(tmpDir)()

	archiveDir := ResurrectionArchiveDir()
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}

	stale := filepath.Join(archiveDir, "ancient.json.1.bak")
	fresh := filepath.Join(archiveDir, "recent.json.2.bak")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
			t.Fatalf("seeding %s: %v", p, err)
		}
	}
	old := time.Now().Add(-archiveRetention - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("aging %s: %v", stale, err)
	}

	CleanResurrectionDir()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("an archived file past the retention window survived (stat err: %v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a recently archived file was pruned, so there is nothing left to inspect: %v", err)
	}
}
