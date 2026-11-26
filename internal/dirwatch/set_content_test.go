package dirwatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestContentSetSeesAWriteInPlace writes into a file that is already there,
// without making or renaming a name, which only a content set reports.
func TestContentSetSeesAWriteInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 8)
	set, err := NewContentSet(func(d string) { got <- d })
	if err == ErrUnsupported {
		t.Skip("no directory watch on this platform")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	if err := set.Add(dir); err != nil {
		t.Fatal(err)
	}
	if err := set.AddFile(path); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("// more\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-got:
		if d != dir {
			t.Errorf("the change was reported for %q, want %q", d, dir)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a write into a file of a watched folder was never reported")
	}
}
