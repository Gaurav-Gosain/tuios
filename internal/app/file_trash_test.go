package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The trash tests point trashDirFunc at a temporary tree. Nothing here goes
// anywhere near the user's own trash, and the real home trash path is checked
// separately without writing to it.

// tempTrash redirects the trash at a directory made for this test.
func tempTrash(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Trash")
	prev := trashDirFunc
	trashDirFunc = func() (string, error) { return dir, nil }
	t.Cleanup(func() { trashDirFunc = prev })
	return dir
}

// trashInfoOf reads the .trashinfo written for a name.
func trashInfoOf(t *testing.T, trash, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(trash, "info", name+".trashinfo"))
	if err != nil {
		t.Fatalf("no info file for %q: %v", name, err)
	}
	return string(body)
}

// TestTrashEncodesAnAwkwardName is the .trashinfo half of the "no shell, no
// injection" claim. The info file is parsed by line, so a name holding a
// newline must not be able to write a line of its own into it.
func TestTrashEncodesAnAwkwardName(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	path := filepath.Join(dir, awkwardName)
	mustWrite(t, path, "body")

	if done, err := trashPaths([]string{path}, time.Now()); err != nil || done != 1 {
		t.Fatalf("trashPaths returned %d, %v", done, err)
	}
	if _, err := os.Lstat(filepath.Join(trash, "files", awkwardName)); err != nil {
		t.Fatalf("the file did not land in the trash under its own name: %v", err)
	}

	info := trashInfoOf(t, trash, awkwardName)
	if lines := strings.Split(strings.TrimRight(info, "\n"), "\n"); len(lines) != 3 {
		t.Fatalf("the info file has %d lines, want 3; a name wrote a line of its own:\n%q", len(lines), info)
	}
	if strings.ContainsAny(strings.SplitN(info, "\n", 3)[1], " \"'`") {
		t.Errorf("the Path line was not encoded:\n%s", info)
	}
	// It has to decode back to the path it came from, or a restore goes
	// somewhere else.
	if got := trashDecode(t, info); got != path {
		t.Errorf("the encoded path reads back as %q, want %q", got, path)
	}
}

// trashDecode reverses trashInfoPath for one info file's Path line.
func trashDecode(t *testing.T, info string) string {
	t.Helper()
	for _, line := range strings.Split(info, "\n") {
		raw, ok := strings.CutPrefix(line, "Path=")
		if !ok {
			continue
		}
		var out []byte
		for i := 0; i < len(raw); i++ {
			if raw[i] == '%' && i+2 < len(raw) {
				var v int
				if _, err := fmtSscanHex(raw[i+1:i+3], &v); err == nil {
					out = append(out, byte(v))
					i += 2
					continue
				}
			}
			out = append(out, raw[i])
		}
		return string(out)
	}
	t.Fatalf("no Path line in:\n%s", info)
	return ""
}

// fmtSscanHex parses two hex digits.
func fmtSscanHex(s string, v *int) (int, error) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			n = n*16 + int(c-'0')
		case c >= 'A' && c <= 'F':
			n = n*16 + int(c-'A'+10)
		case c >= 'a' && c <= 'f':
			n = n*16 + int(c-'a'+10)
		default:
			return 0, errors.New("not hex")
		}
	}
	*v = n
	return 1, nil
}

// TestTwoFilesWithOneNameBothSurviveTheTrash is the claim behind claiming the
// info file with O_EXCL: deleting src/a.txt and then dst/a.txt must leave two
// files in the trash, not one on top of the other.
func TestTwoFilesWithOneNameBothSurviveTheTrash(t *testing.T) {
	trash := tempTrash(t)
	first, second := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(first, "a.txt"), "first")
	mustWrite(t, filepath.Join(second, "a.txt"), "second")

	for _, p := range []string{filepath.Join(first, "a.txt"), filepath.Join(second, "a.txt")} {
		if done, err := trashPaths([]string{p}, time.Now()); err != nil || done != 1 {
			t.Fatalf("trashing %q returned %d, %v", p, done, err)
		}
	}

	got := namesIn(t, filepath.Join(trash, "files"))
	if len(got) != 2 {
		t.Fatalf("the trash holds %q; both files must survive", got)
	}
	body, err := os.ReadFile(filepath.Join(trash, "files", "a.txt"))
	if err != nil || string(body) != "first" {
		t.Errorf("the first file was replaced: %q %v", body, err)
	}
	body, err = os.ReadFile(filepath.Join(trash, "files", "a.1.txt"))
	if err != nil || string(body) != "second" {
		t.Errorf("the second file did not get a free name: %q %v", body, err)
	}
}
