package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestTrashMovesTheFileAndRecordsWhereItCameFrom(t *testing.T) {
	trash := tempTrash(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	mustWrite(t, path, "body")

	when := time.Date(2026, 8, 27, 9, 30, 0, 0, time.UTC)
	done, err := trashPaths([]string{path}, when)
	if err != nil || done != 1 {
		t.Fatalf("trashPaths returned %d, %v; want 1, nil", done, err)
	}
	if _, err := os.Lstat(path); err == nil {
		t.Error("the file is still where it was after a trash")
	}
	body, err := os.ReadFile(filepath.Join(trash, "files", "report.txt"))
	if err != nil || string(body) != "body" {
		t.Fatalf("the file is not in the trash: %q %v", body, err)
	}

	info := trashInfoOf(t, trash, "report.txt")
	if !strings.HasPrefix(info, "[Trash Info]\n") {
		t.Errorf("the info file does not open with the spec's header:\n%s", info)
	}
	if !strings.Contains(info, "Path="+trashInfoPath(path)+"\n") {
		t.Errorf("the info file does not record the original path:\n%s", info)
	}
	if !strings.Contains(info, "DeletionDate=2026-08-27T09:30:00\n") {
		t.Errorf("the info file does not record the deletion time:\n%s", info)
	}
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

// TestTrashLeavesNoInfoBehindWhenTheMoveFails is the rollback. The info file is
// written first, so a move that then fails would leave the trash holding a
// record of a file that is still where it was, and a restore of it would fail.
func TestTrashLeavesNoInfoBehindWhenTheMoveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only folder anyway")
	}
	trash := tempTrash(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	mustWrite(t, path, "body")

	// The trash's files folder is made unwritable, so the claim is written and
	// the move that follows it fails.
	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(trash, "info"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(trash, "files"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(trash, "files"), 0o700) })

	if done, err := trashPaths([]string{path}, time.Now()); err == nil || done != 0 {
		t.Fatalf("the trash reported %d, %v; the move cannot have worked", done, err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the file was lost by a trash that failed: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(trash, "info")); err == nil && len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a failed trash left %d info files behind: %v", len(entries), names)
	}

	// And a trash that never claimed a name leaves nothing either.
	if _, err := trashPaths([]string{filepath.Join(dir, "missing.txt")}, time.Now()); err == nil {
		t.Error("trashing a missing file was allowed")
	}
}

// TestTrashOnAnotherDiskNamesTheWayRound is the cross-filesystem gap, and it is
// the one thing the home trash cannot do. It needs two real filesystems, so it
// skips where there is only one.
func TestTrashOnAnotherDiskNamesTheWayRound(t *testing.T) {
	trash := tempTrash(t)
	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil {
		t.Fatal(err)
	}

	other, err := os.MkdirTemp("/dev/shm", "tuios-trash-")
	if err != nil {
		t.Skipf("no second filesystem to test against: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	if sameDevice(t, trash, other) {
		t.Skip("the trash and /dev/shm are on one filesystem here")
	}

	path := filepath.Join(other, "elsewhere.txt")
	mustWrite(t, path, "body")
	done, err := trashPaths([]string{path}, time.Now())
	if done != 0 || err == nil {
		t.Fatalf("trashing across filesystems returned %d, %v; it cannot work", done, err)
	}
	if !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("the error was %v, want EXDEV", err)
	}
	got := trashError(err)
	if !strings.Contains(got, "another disk") || !strings.Contains(got, "permanent delete") {
		t.Errorf("the message is %q; it must name the disk and the way round it", got)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the file was lost by a trash that could not work: %v", err)
	}
}

// sameDevice reports whether two paths sit on one filesystem.
func sameDevice(t *testing.T, a, b string) bool {
	t.Helper()
	sa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	ra, ok1 := sa.Sys().(*syscall.Stat_t)
	rb, ok2 := sb.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		t.Skip("this system does not report device numbers")
	}
	return ra.Dev == rb.Dev
}

// TestTheHomeTrashIsTheSpecPath checks where the default trash resolves to
// without writing anything into it.
func TestTheHomeTrashIsTheSpecPath(t *testing.T) {
	if !trashAvailable() {
		t.Skip("no trash on this system")
	}
	dir, err := homeTrashDir()
	if err != nil {
		t.Fatalf("the home trash did not resolve: %v", err)
	}
	if filepath.Base(dir) != "Trash" {
		t.Errorf("the home trash is %q; the spec calls the folder Trash", dir)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("the home trash %q is not an absolute path", dir)
	}
}
