//go:build unix

package applist

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// mkTree builds a throwaway XDG data hierarchy, points the environment at it,
// and returns the application directories to scan. Paths in files are relative
// to the tree root, so "home/applications/x.desktop" lands in $XDG_DATA_HOME
// and "sys1/..." in the first $XDG_DATA_DIRS entry.
func mkTree(t *testing.T, files map[string]string) []string {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	sys1 := filepath.Join(root, "sys1")
	sys2 := filepath.Join(root, "sys2")
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_DATA_DIRS", sys1+":"+sys2)
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "C")
	return DesktopDirs()
}

func byID(entries []DesktopEntry, id string) *DesktopEntry {
	for i := range entries {
		if entries[i].ID == id {
			return &entries[i]
		}
	}
	return nil
}

func ids(entries []DesktopEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

// TestDesktopDirsPrecedence pins the order the override mechanism depends on:
// $XDG_DATA_HOME first, then $XDG_DATA_DIRS as written.
func TestDesktopDirsPrecedence(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/h")
	t.Setenv("XDG_DATA_DIRS", "/a::/b")
	want := []string{"/h/applications", "/a/applications", "/b/applications"}
	if got := DesktopDirs(); !slices.Equal(got, want) {
		t.Fatalf("DesktopDirs = %v, want %v", got, want)
	}
}

// TestDesktopFileIDFirstWins is the whole mechanism by which a user overrides a
// system entry, so it is not optional.
func TestDesktopFileIDFirstWins(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"home/applications/thing.desktop": "[Desktop Entry]\nType=Application\nName=Mine\nExec=mine\n",
		"sys1/applications/thing.desktop": "[Desktop Entry]\nType=Application\nName=Theirs\nExec=theirs\n",
	})
	got := ScanDesktop(dirs)
	if len(got) != 1 {
		t.Fatalf("ScanDesktop = %v, want the system copy shadowed", ids(got))
	}
	if e := byID(got, "thing.desktop"); e == nil || e.Name != "Mine" {
		t.Fatalf("user entry should shadow the system one, got %+v", e)
	}
}

// TestDesktopFileIDFromSubdirectory: a subdirectory contributes its path to the
// ID with '/' replaced by '-'.
func TestDesktopFileIDFromSubdirectory(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/kde/kate.desktop": "[Desktop Entry]\nType=Application\nName=Kate\nExec=kate\n",
	})
	if e := byID(ScanDesktop(dirs), "kde-kate.desktop"); e == nil {
		t.Fatal("expected desktop file ID kde-kate.desktop")
	}
}

// TestNameFallsBackToBaseName: an entry with no Name is named after its file,
// and the file's directory is not part of a name.
func TestNameFallsBackToBaseName(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/kde/kate.desktop": "[Desktop Entry]\nType=Application\nExec=kate\n",
	})
	e := byID(ScanDesktop(dirs), "kde-kate.desktop")
	if e == nil || e.Name != "kate" {
		t.Fatalf("name = %+v, want %q", e, "kate")
	}
}

// TestHiddenVersusNoDisplay: both keep an entry out of a menu, but they are
// different keys with different meanings and are read separately.
func TestHiddenVersusNoDisplay(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/h.desktop": "[Desktop Entry]\nType=Application\nName=H\nExec=h\nHidden=true\n",
		"sys1/applications/n.desktop": "[Desktop Entry]\nType=Application\nName=N\nExec=n\nNoDisplay=true\n",
		"sys1/applications/k.desktop": "[Desktop Entry]\nType=Application\nName=K\nExec=k\n",
	})
	got := ScanDesktop(dirs)
	if len(got) != 1 || got[0].Name != "K" {
		t.Fatalf("ScanDesktop = %v, want only K", ids(got))
	}
}

// TestHiddenOverrideDoesNotFallThrough: a Hidden user entry must not fall
// through to the system copy, because the override is the point.
func TestHiddenOverrideDoesNotFallThrough(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"home/applications/x.desktop": "[Desktop Entry]\nType=Application\nName=X\nExec=x\nHidden=true\n",
		"sys1/applications/x.desktop": "[Desktop Entry]\nType=Application\nName=X\nExec=x\n",
	})
	if got := ScanDesktop(dirs); len(got) != 0 {
		t.Fatalf("ScanDesktop = %v, want a user Hidden=true entry to suppress the system one", ids(got))
	}
}

// TestShowInIsAList: OnlyShowIn and NotShowIn match against the colon-separated
// $XDG_CURRENT_DESKTOP, not against a single name.
func TestShowInIsAList(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/only.desktop": "[Desktop Entry]\nType=Application\nName=O\nExec=o\nOnlyShowIn=KDE;GNOME;\n",
		"sys1/applications/not.desktop":  "[Desktop Entry]\nType=Application\nName=N\nExec=n\nNotShowIn=GNOME;\n",
	})
	t.Setenv("XDG_CURRENT_DESKTOP", "wlroots:GNOME")
	got := ScanDesktop(dirs)
	if byID(got, "only.desktop") == nil {
		t.Error("OnlyShowIn=GNOME should show when GNOME is anywhere in the colon list")
	}
	if byID(got, "not.desktop") != nil {
		t.Error("NotShowIn=GNOME should hide when GNOME is anywhere in the colon list")
	}

	t.Setenv("XDG_CURRENT_DESKTOP", "XFCE")
	got = ScanDesktop(dirs)
	if byID(got, "only.desktop") != nil {
		t.Error("OnlyShowIn=KDE;GNOME must hide under XFCE")
	}
	if byID(got, "not.desktop") == nil {
		t.Error("NotShowIn=GNOME must show under XFCE")
	}
}

// TestTryExec: TryExec names a binary that must resolve, otherwise the
// application is not installed and must not be offered.
func TestTryExec(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/ok.desktop":  "[Desktop Entry]\nType=Application\nName=Ok\nExec=x\nTryExec=sh\n",
		"sys1/applications/bad.desktop": "[Desktop Entry]\nType=Application\nName=Bad\nExec=x\nTryExec=definitely-not-a-real-binary-9f3a\n",
	})
	got := ScanDesktop(dirs)
	if byID(got, "ok.desktop") == nil {
		t.Error("TryExec=sh should resolve")
	}
	if byID(got, "bad.desktop") != nil {
		t.Error("an unresolvable TryExec means the entry is not installed")
	}
}

func TestLocalizedName(t *testing.T) {
	body := "[Desktop Entry]\nType=Application\nExec=x\n" +
		"Name=Base\nName[de]=Deutsch\nName[de_AT]=Oesterreich\nName[fr]=Francais\n"
	dirs := mkTree(t, map[string]string{"sys1/applications/l.desktop": body})

	for _, c := range []struct{ lang, want string }{
		{"C", "Base"},
		{"POSIX", "Base"},
		{"de_DE.UTF-8", "Deutsch"},
		{"de_AT.UTF-8", "Oesterreich"},
		{"fr_FR.UTF-8", "Francais"},
		{"es_ES.UTF-8", "Base"},
	} {
		t.Setenv("LANG", c.lang)
		e := byID(ScanDesktop(dirs), "l.desktop")
		if e == nil || e.Name != c.want {
			t.Errorf("LANG=%s gave %v, want %q", c.lang, e, c.want)
		}
	}
}

// TestLocalePrecedence walks the four [suffix] forms in the order the spec
// gives them, most specific first.
func TestLocalePrecedence(t *testing.T) {
	loc := locale{lang: "sr", country: "RS", modifier: "latin"}
	want := []string{"sr_RS@latin", "sr_RS", "sr@latin", "sr"}
	if got := loc.candidates(); !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	if got := (locale{}).candidates(); got != nil {
		t.Fatalf("the unlocalized locale must match no [suffix] key, got %v", got)
	}
}

func TestValueEscapes(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/e.desktop": "[Desktop Entry]\nType=Application\n" +
			"Name=a\\sb\\tc\n Exec = x \nKeywords=one;t\\;wo;three;\n",
	})
	e := byID(ScanDesktop(dirs), "e.desktop")
	if e == nil {
		t.Fatal("missing entry")
	}
	if e.Name != "a b\tc" {
		t.Errorf("name = %q, want %q", e.Name, "a b\tc")
	}
	// "Space before and after the equals sign should be ignored".
	if !slices.Equal(e.Argv, []string{"x"}) {
		t.Errorf("argv = %q, want %q", e.Argv, []string{"x"})
	}
	if want := []string{"one", "t;wo", "three"}; !slices.Equal(e.Keywords, want) {
		t.Errorf("keywords = %q, want an escaped semicolon inside one element (%q)", e.Keywords, want)
	}
}

// TestDuplicateKeyFirstWins: a file that repeats a key inside a group is
// malformed but common enough that rejecting it would lose real applications.
func TestDuplicateKeyFirstWins(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/d.desktop": "[Desktop Entry]\nType=Application\nName=First\nName=Second\nExec=x\n" +
			"# a comment\n[Desktop Entry]\nComment=Reopened\n",
	})
	e := byID(ScanDesktop(dirs), "d.desktop")
	if e == nil || e.Name != "First" {
		t.Fatalf("entry = %+v, want the first Name to win", e)
	}
	if e.Comment != "Reopened" {
		t.Errorf("comment = %q, want a re-entered group to add to the group it names", e.Comment)
	}
}

// TestActions: actions become entries of their own, and only the names listed
// in Actions= with a matching group are valid.
func TestActions(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/b.desktop": "[Desktop Entry]\nType=Application\nName=Browser\nExec=b\n" +
			"GenericName=Web Browser\nIcon=browser\nActions=priv;ghost;empty;\n" +
			"[Desktop Action priv]\nName=New Private Window\nExec=b --private\n" +
			"[Desktop Action empty]\nName=Nothing\nExec=\n",
	})
	got := ScanDesktop(dirs)
	var actions int
	for _, e := range got {
		if e.Action != "" {
			actions++
		}
	}
	if actions != 1 {
		t.Fatalf("ScanDesktop = %v, want one action (ghost has no group, empty has no Exec)", ids(got))
	}
	a := byID(got, "b.desktop:priv")
	if a == nil {
		t.Fatal("missing action entry")
	}
	if a.Name != "Browser - New Private Window" {
		t.Errorf("action name = %q", a.Name)
	}
	if a.FileID != "b.desktop" || a.Action != "priv" {
		t.Errorf("action identity = %q/%q, want b.desktop/priv", a.FileID, a.Action)
	}
	if !slices.Equal(a.Argv, []string{"b", "--private"}) {
		t.Errorf("action argv = %q", a.Argv)
	}
	if a.Generic != "Web Browser" {
		t.Errorf("action generic = %q, want the parent's GenericName", a.Generic)
	}
	// The spec gives an action group its own Icon key, so an action that does
	// not set one has no icon of its own.
	if a.Icon != "" {
		t.Errorf("action icon = %q, want no inherited icon", a.Icon)
	}
}

// TestActionWithoutName falls back to the action identifier, the only thing
// left to show.
func TestActionWithoutName(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/b.desktop": "[Desktop Entry]\nType=Application\nName=B\nExec=b\nActions=raw;\n" +
			"[Desktop Action raw]\nExec=b --raw\nIcon=own\n",
	})
	a := byID(ScanDesktop(dirs), "b.desktop:raw")
	if a == nil || a.Name != "B - raw" {
		t.Fatalf("action = %+v, want the identifier as the verb", a)
	}
	if a.Icon != "own" {
		t.Errorf("action icon = %q, want its own", a.Icon)
	}
}

func TestNonApplicationTypesSkipped(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/link.desktop": "[Desktop Entry]\nType=Link\nName=L\nURL=http://x\n",
		"sys1/applications/dir.desktop":  "[Desktop Entry]\nType=Directory\nName=D\n",
		"sys1/applications/none.desktop": "[Desktop Entry]\nName=N\nExec=n\n",
	})
	if got := ScanDesktop(dirs); len(got) != 0 {
		t.Fatalf("ScanDesktop = %v, want nothing that is not Type=Application", ids(got))
	}
}

// TestUnrunnableExecSkipped: a row that fails when activated is worse than a
// row that is not there.
func TestUnrunnableExecSkipped(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/blank.desktop": "[Desktop Entry]\nType=Application\nName=Blank\nExec=   \n",
		"sys1/applications/codes.desktop": "[Desktop Entry]\nType=Application\nName=Codes\nExec=%f %u\n",
		"sys1/applications/quote.desktop": "[Desktop Entry]\nType=Application\nName=Quote\nExec=app \"unclosed\n",
		"sys1/applications/act.desktop":   "[Desktop Entry]\nType=Application\nName=Act\nExec=\nActions=go;\n[Desktop Action go]\nName=Go\nExec=go\n",
	})
	if got := ScanDesktop(dirs); len(got) != 0 {
		t.Fatalf("ScanDesktop = %v, want nothing with an unrunnable Exec", ids(got))
	}
}

func TestTerminalTrue(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/t.desktop": "[Desktop Entry]\nType=Application\nName=T\nExec=htop\nTerminal=true\n",
		"sys1/applications/f.desktop": "[Desktop Entry]\nType=Application\nName=F\nExec=gui\nTerminal=false\n",
		"sys1/applications/j.desktop": "[Desktop Entry]\nType=Application\nName=J\nExec=gui\nTerminal=yes\n",
	})
	got := ScanDesktop(dirs)
	if !byID(got, "t.desktop").Terminal {
		t.Error("Terminal=true not honoured")
	}
	if byID(got, "f.desktop").Terminal {
		t.Error("Terminal=false read as true")
	}
	// The spec's boolean type is exactly "true" or "false"; anything else is
	// invalid and must not be guessed into a yes.
	if byID(got, "j.desktop").Terminal {
		t.Error("Terminal=yes is not a valid boolean and must not mean true")
	}
}

// TestScanSortsByName keeps the order a menu reads in, independent of the order
// the directories happened to be walked in.
func TestScanSortsByName(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/z.desktop": "[Desktop Entry]\nType=Application\nName=alpha\nExec=a\n",
		"sys1/applications/a.desktop": "[Desktop Entry]\nType=Application\nName=Beta\nExec=b\n",
		"sys2/applications/m.desktop": "[Desktop Entry]\nType=Application\nName=Gamma\nExec=g\n",
	})
	want := []string{"z.desktop", "a.desktop", "m.desktop"}
	if got := ids(ScanDesktop(dirs)); !slices.Equal(got, want) {
		t.Fatalf("ScanDesktop = %v, want %v", got, want)
	}
}

// TestDesktopCacheReusesUnchangedFiles is the promise that reopening the
// launcher is instant: a file whose mtime has not moved must not be reparsed.
// The proof is a rewritten file whose recorded mtime is put back, which the
// cache is then expected to miss.
func TestDesktopCacheReusesUnchangedFiles(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/a.desktop": "[Desktop Entry]\nType=Application\nName=Before\nExec=a\n",
	})
	path := filepath.Join(dirs[1], "a.desktop")

	c := NewDesktopCache()
	got, changed := c.refresh(dirs)
	if !changed || len(got) != 1 || got[0].Name != "Before" {
		t.Fatalf("first refresh = %v changed=%v", ids(got), changed)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[Desktop Entry]\nType=Application\nName=After\nExec=a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Restore the mtime so the cache has no reason to open the file again.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	got, changed = c.refresh(dirs)
	if changed {
		t.Error("refresh reparsed a file whose mtime had not moved")
	}
	if len(got) != 1 || got[0].Name != "Before" {
		t.Fatalf("cached refresh = %v, want the earlier parse", got)
	}
	if entries := c.Entries(); len(entries) != 1 || entries[0].Name != "Before" {
		t.Fatalf("Entries = %v, want the cached list", entries)
	}
}

// TestDesktopCacheSeesEditedFile is the other half: an application installed or
// edited while tuios is running has to show up without a restart.
func TestDesktopCacheSeesEditedFile(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/a.desktop": "[Desktop Entry]\nType=Application\nName=Before\nExec=a\n",
	})
	path := filepath.Join(dirs[1], "a.desktop")

	c := NewDesktopCache()
	c.refresh(dirs)

	// Push the recorded mtime back so the rewrite is guaranteed to land on a
	// different one even where the filesystem has coarse timestamps.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	c.files[path] = cachedDesktopFile{mtime: old, entries: c.files[path].entries}
	if err := os.WriteFile(path, []byte("[Desktop Entry]\nType=Application\nName=After\nExec=a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, changed := c.refresh(dirs)
	if !changed {
		t.Fatal("refresh missed a file whose mtime moved")
	}
	if len(got) != 1 || got[0].Name != "After" {
		t.Fatalf("refresh = %v, want the edited entry", got)
	}
}

// TestDesktopCacheForgetsRemovedFile guards the case a cached parse would
// happily outlive: an uninstalled application must stop being offered.
func TestDesktopCacheForgetsRemovedFile(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/a.desktop": "[Desktop Entry]\nType=Application\nName=Doomed\nExec=a\n",
	})
	c := NewDesktopCache()
	c.refresh(dirs)

	if err := os.Remove(filepath.Join(dirs[1], "a.desktop")); err != nil {
		t.Fatal(err)
	}
	got, changed := c.refresh(dirs)
	if !changed || len(got) != 0 {
		t.Fatalf("refresh = %v changed=%v, want nothing from a file that is gone", ids(got), changed)
	}
}

// TestDesktopCacheDiscardsOnLocaleChange: the cached entries were localized
// under the old environment, so they cannot be reused under a new one.
func TestDesktopCacheDiscardsOnLocaleChange(t *testing.T) {
	dirs := mkTree(t, map[string]string{
		"sys1/applications/a.desktop": "[Desktop Entry]\nType=Application\nName=Base\nName[de]=Deutsch\nExec=a\n",
	})
	c := NewDesktopCache()
	c.refresh(dirs)

	t.Setenv("LANG", "de_DE.UTF-8")
	got, changed := c.refresh(dirs)
	if !changed || len(got) != 1 || got[0].Name != "Deutsch" {
		t.Fatalf("refresh = %v changed=%v, want the entry relocalized", got, changed)
	}
}

func TestDesktopEntriesBeforeFirstRefresh(t *testing.T) {
	if got := NewDesktopCache().Entries(); got != nil {
		t.Fatalf("Entries = %v before any scan, want nil", got)
	}
}
