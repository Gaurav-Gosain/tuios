package shimlink

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The ways InstallFile can fail, which these tests hold it to:
//
//   - tuios update replaced the binary, and a hardlink to the old one is
//     taken as current, so herdr runs the old tuios;
//   - the link is on another volume, the hardlink fails, and nothing is made;
//   - a copy of an older tuios is taken as current;
//   - a current copy is written again on every start;
//   - a symbolic link is made although a copy would have worked;
//   - Windows refuses to replace the running link, and the start fails;
//   - a failed attempt leaves its half-made file, or the moved-aside file,
//     behind for good.

// faultFS is the real filesystem with operations that can be made to fail,
// as a link across volumes or a running program on Windows fails.
type faultFS struct {
	OSFS
	link, copy, symlink error
	// renameOnto fails a rename onto this path, once per entry in the
	// count, as Windows fails one onto a running program.
	renameOnto string
	renameErrs int
	calls      []string
}

func (f *faultFS) Link(o, n string) error {
	f.calls = append(f.calls, "link")
	if f.link != nil {
		return f.link
	}
	return f.OSFS.Link(o, n)
}

func (f *faultFS) Copy(s, d string) error {
	f.calls = append(f.calls, "copy")
	if f.copy != nil {
		return f.copy
	}
	return f.OSFS.Copy(s, d)
}

func (f *faultFS) Symlink(o, n string) error {
	f.calls = append(f.calls, "symlink")
	if f.symlink != nil {
		return f.symlink
	}
	return f.OSFS.Symlink(o, n)
}

func (f *faultFS) Rename(o, n string) error {
	if n == f.renameOnto && f.renameErrs > 0 && strings.Contains(o, ".new-") {
		f.renameErrs--
		return errors.New("access is denied")
	}
	return f.OSFS.Rename(o, n)
}

var errXDev = errors.New("not the same device")

func setup(t *testing.T) (exe, link string) {
	t.Helper()
	dir := t.TempDir()
	exe = filepath.Join(dir, "tuios.exe")
	writeExe(t, exe, "tuios v1")
	return exe, filepath.Join(dir, "bin", "herdr.exe")
}

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	// Written beside and renamed, as tuios update does, so the path gets a
	// new file and not new bytes in the old one.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(filepath.Dir(path), "bin"), 0o700)
}

func body(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func install(t *testing.T, f FS, link, exe string) Method {
	t.Helper()
	m, err := InstallFile(f, link, exe, "1")
	if err != nil {
		t.Fatalf("InstallFile: %v", err)
	}
	return m
}

func leftovers(t *testing.T, link string) []string {
	t.Helper()
	var out []string
	for _, s := range []string{".new-*", ".old-*"} {
		m, _ := filepath.Glob(link + s)
		out = append(out, m...)
	}
	return out
}

func TestInstallFileHardlinkFirst(t *testing.T) {
	exe, link := setup(t)
	f := &faultFS{}
	if m := install(t, f, link, exe); m != MethodHardlink {
		t.Fatalf("method = %s, want hardlink", m)
	}
	a, _ := os.Stat(link)
	b, _ := os.Stat(exe)
	if !os.SameFile(a, b) {
		t.Fatal("the link is not the binary")
	}
	f.calls = nil
	if m := install(t, f, link, exe); m != MethodHardlink || len(f.calls) != 0 {
		t.Fatalf("a current hardlink was made again: method %s, calls %v", m, f.calls)
	}
}

func TestInstallFileRelinksAfterUpdate(t *testing.T) {
	exe, link := setup(t)
	install(t, &faultFS{}, link, exe)
	writeExe(t, exe, "tuios v2")
	if got := body(t, link); got != "tuios v1" {
		t.Fatalf("setup: link reads %q before the new install", got)
	}
	install(t, &faultFS{}, link, exe)
	if got := body(t, link); got != "tuios v2" {
		t.Fatalf("after an update the link runs %q, want tuios v2", got)
	}
}

func TestInstallFileCopiesAcrossVolumes(t *testing.T) {
	exe, link := setup(t)
	f := &faultFS{link: errXDev}
	if m := install(t, f, link, exe); m != MethodCopy {
		t.Fatalf("method = %s, want copy", m)
	}
	if got := body(t, link); got != "tuios v1" {
		t.Fatalf("copy reads %q", got)
	}
	a, _ := os.Stat(link)
	b, _ := os.Stat(exe)
	if os.SameFile(a, b) || !a.ModTime().Equal(b.ModTime()) {
		t.Fatalf("the copy is not a separate file with the binary's time: same %v, times %v %v", os.SameFile(a, b), a.ModTime(), b.ModTime())
	}

	// A current copy is kept as it is.
	f.calls = nil
	if m := install(t, f, link, exe); m != MethodCopy || len(f.calls) != 0 {
		t.Fatalf("a current copy was made again: method %s, calls %v", m, f.calls)
	}

	// A new binary of the same size is still a new binary.
	time.Sleep(10 * time.Millisecond)
	writeExe(t, exe, "tuios v2")
	if err := os.Chtimes(exe, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	install(t, f, link, exe)
	if got := body(t, link); got != "tuios v2" {
		t.Fatalf("a stale copy was kept: it reads %q", got)
	}
}

func TestInstallFileReplacesAStaleCopy(t *testing.T) {
	exe, link := setup(t)
	// A copy left by an older tuios, or made by hand: other bytes, other
	// time.
	if err := os.WriteFile(link, []byte("tuios v0, older"), 0o700); err != nil {
		t.Fatal(err)
	}
	install(t, &faultFS{link: errXDev}, link, exe)
	if got := body(t, link); got != "tuios v1" {
		t.Fatalf("the stale copy was kept: it reads %q", got)
	}
}

func TestInstallFileSymlinkLast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a symbolic link needs a privilege the test may not have")
	}
	exe, link := setup(t)
	f := &faultFS{link: errXDev, copy: errors.New("disk full")}
	if m := install(t, f, link, exe); m != MethodSymlink {
		t.Fatalf("method = %s, want symlink", m)
	}
	if strings.Join(f.calls, ",") != "link,copy,symlink" {
		t.Fatalf("order = %v", f.calls)
	}
	if got, _ := os.Readlink(link); got != exe {
		t.Fatalf("symlink points at %q", got)
	}
	if l := leftovers(t, link); len(l) != 0 {
		t.Fatalf("left behind %v", l)
	}
}

func TestInstallFileAllFail(t *testing.T) {
	exe, link := setup(t)
	f := &faultFS{link: errXDev, copy: errors.New("disk full"), symlink: errors.New("a required privilege is not held")}
	_, err := InstallFile(f, link, exe, "1")
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"hardlink", "copy", "symlink", "privilege"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if _, serr := os.Lstat(link); !os.IsNotExist(serr) {
		t.Errorf("a link exists after every method failed")
	}
	if l := leftovers(t, link); len(l) != 0 {
		t.Fatalf("left behind %v", l)
	}
}

func TestInstallFileMovesARunningLinkAside(t *testing.T) {
	exe, link := setup(t)
	install(t, &faultFS{link: errXDev}, link, exe)
	writeExe(t, exe, "tuios v2")
	if err := os.Chtimes(exe, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f := &faultFS{renameOnto: link, renameErrs: 1}
	install(t, f, link, exe)
	if got := body(t, link); got != "tuios v2" {
		t.Fatalf("link reads %q", got)
	}
	if l := leftovers(t, link); len(l) != 0 {
		t.Fatalf("left behind %v", l)
	}
}

func TestInstallFileSweepsLeftovers(t *testing.T) {
	exe, link := setup(t)
	install(t, &faultFS{}, link, exe)
	// What a process that died part way left: a half-made file and one
	// moved aside while it ran.
	for _, n := range []string{link + ".new-77", link + ".old-77"} {
		if err := os.WriteFile(n, []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	install(t, &faultFS{}, link, exe)
	if l := leftovers(t, link); len(l) != 0 {
		t.Fatalf("left behind %v", l)
	}
}

func TestInstallFileComparesBytesWhenTimesMatch(t *testing.T) {
	exe, link := setup(t)
	install(t, &faultFS{link: errXDev}, link, exe)
	// Two builds of one size, stamped with one time, as a coarse clock or
	// an archive that keeps times can make them.
	st, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	writeExe(t, exe, "tuios v2")
	if err := os.Chtimes(exe, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	install(t, &faultFS{link: errXDev}, link, exe)
	if got := body(t, link); got != "tuios v2" {
		t.Fatalf("a copy with the same size and time but other bytes was kept: it reads %q", got)
	}
}
