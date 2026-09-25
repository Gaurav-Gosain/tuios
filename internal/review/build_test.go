package review

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileLinesStaysInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "one\r\ntwo\n")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("no symlinks here")
	}
	lines, err := FileLines(root, "a.txt")
	if err != nil || len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Errorf("a.txt = %q (%v)", lines, err)
	}
	for _, p := range []string{"link.txt", "../x", "/etc/passwd", "a/../../x", ""} {
		if _, err := FileLines(root, p); err == nil {
			t.Errorf("FileLines(%q) read a file outside the root", p)
		}
	}
}
