package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDraftSaveDoesNotWriteThroughAPlantedTempLink is a security boundary: a
// link planted at the old fixed temporary name ("<theme>.json.tmp") must not
// make Save write the theme into the file the link names. The save uses a
// fresh temporary name, so the planted link is never opened.
func TestDraftSaveDoesNotWriteThroughAPlantedTempLink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mine.json")
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	d, err := NewDraft("nord")
	if err != nil {
		t.Fatal(err)
	}
	d.ID = "mine"
	d.src = &draftSource{path: path}
	if _, err := d.SaveOver(); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := os.ReadFile(victim)
	if string(got) != "keep" {
		t.Errorf("the save wrote through the planted link: the victim now holds %d bytes", len(got))
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the theme file was not written: %v", err)
	}
}
