//go:build unix

package applist

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
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
