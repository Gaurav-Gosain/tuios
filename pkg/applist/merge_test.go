//go:build unix

package applist

import (
	"slices"
	"testing"
)

// pathEntry is a $PATH row as Scan would have produced it.
func pathEntry(name, dir string) Entry {
	return Entry{Name: name, Path: dir + "/" + name, Dir: dir, Source: SourcePath}
}

// desktopEntry is an application row as ScanDesktop would have produced it.
func desktopEntry(id, name string, argv ...string) DesktopEntry {
	return DesktopEntry{
		ID:     id,
		FileID: id,
		Path:   "/usr/share/applications/" + id,
		Name:   name,
		Argv:   argv,
	}
}

// TestMergeSupersedesPathRow is the rule the merged list exists for: one
// program must not appear twice under two names, and the row that survives is
// the one carrying a name a person recognises.
func TestMergeSupersedesPathRow(t *testing.T) {
	path := []Entry{pathEntry("firefox", "/usr/bin"), pathEntry("htop", "/usr/bin")}
	desktop := []DesktopEntry{desktopEntry("org.mozilla.firefox.desktop", "Firefox Web Browser", "firefox", "--new-window")}

	got := Merge(path, desktop)
	want := []string{"org.mozilla.firefox", "htop"}
	if !slices.Equal(names(got), want) {
		t.Fatalf("Merge = %v, want %v", names(got), want)
	}
	e := got[0]
	if e.Source != SourceDesktop {
		t.Errorf("Source = %q, want %q", e.Source, SourceDesktop)
	}
	if e.Label() != "Firefox Web Browser" {
		t.Errorf("Label = %q, want the desktop entry's name", e.Label())
	}
	if !slices.Equal(e.Exec, []string{"firefox", "--new-window"}) {
		t.Errorf("Exec = %v, want the desktop entry's argv", e.Exec)
	}
}

// TestMergeSupersedesOnAbsoluteExec covers the other half of the match: an
// absolute Exec supersedes only the very file Scan listed.
func TestMergeSupersedesOnAbsoluteExec(t *testing.T) {
	path := []Entry{pathEntry("code", "/usr/bin")}
	desktop := []DesktopEntry{desktopEntry("code.desktop", "Visual Studio Code", "/usr/bin/code", "%F")}

	if got := names(Merge(path, desktop)); !slices.Equal(got, []string{"code"}) {
		t.Fatalf("Merge = %v, want the desktop entry alone", got)
	}
}

// TestMergeKeepsPathRowOnMismatch is the guard against supersession losing a
// program: sharing a basename is not being the same file, and a desktop entry
// naming a program that is not on $PATH hides nothing.
func TestMergeKeepsPathRowOnMismatch(t *testing.T) {
	path := []Entry{pathEntry("code", "/usr/bin")}
	desktop := []DesktopEntry{
		desktopEntry("vendor-code.desktop", "Vendor Code", "/opt/vendor/bin/code"),
		desktopEntry("elsewhere.desktop", "Elsewhere", "not-installed"),
	}

	got := names(Merge(path, desktop))
	want := []string{"vendor-code", "elsewhere", "code"}
	if !slices.Equal(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
}

// TestMergeActionNeverSupersedes: an action is an extra verb on an
// application, so hiding the plain program because one of its actions names it
// would lose the ordinary way to start it.
func TestMergeActionNeverSupersedes(t *testing.T) {
	path := []Entry{pathEntry("htop", "/usr/bin")}
	action := desktopEntry("org.x.monitor.desktop:new-window", "Monitor - New Window", "htop")
	action.Action = "new-window"
	action.FileID = "org.x.monitor.desktop"

	got := names(Merge(path, []DesktopEntry{action}))
	want := []string{"org.x.monitor:new-window", "htop"}
	if !slices.Equal(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
}

// TestMergeDedupsByName holds the list to one row per name, the same rule Scan
// applies within $PATH, with the first occurrence winning.
func TestMergeDedupsByName(t *testing.T) {
	path := []Entry{pathEntry("tool", "/usr/bin")}
	first := desktopEntry("tool.desktop", "The Tool", "run-tool")
	second := desktopEntry("tool.desktop", "Another Tool", "other-tool")
	second.Path = "/usr/local/share/applications/tool.desktop"

	got := Merge(path, []DesktopEntry{first, second})
	if !slices.Equal(names(got), []string{"tool"}) {
		t.Fatalf("Merge = %v, want one row per name", names(got))
	}
	if got[0].Display != "The Tool" {
		t.Errorf("Display = %q, want the first entry to win", got[0].Display)
	}
}

// TestMergeOrdersDesktopFirst pins the answer to "what does the launcher show
// before anything is typed": the applications a person recognises, not the
// several thousand $PATH names behind them.
func TestMergeOrdersDesktopFirst(t *testing.T) {
	path := []Entry{pathEntry("awk", "/usr/bin"), pathEntry("zip", "/usr/bin")}
	desktop := []DesktopEntry{
		desktopEntry("archiver.desktop", "Archiver", "file-roller"),
		desktopEntry("browser.desktop", "Browser", "epiphany"),
	}

	got := names(Merge(path, desktop))
	want := []string{"archiver", "browser", "awk", "zip"}
	if !slices.Equal(got, want) {
		t.Fatalf("Merge = %v, want %v", got, want)
	}
}

// TestMergeDropsEntryWithoutExec: such an entry has nothing to run, since its
// Path is the .desktop file rather than a program, so it is dropped. A row
// that fails when activated is worse than a row that is not there.
func TestMergeDropsEntryWithoutExec(t *testing.T) {
	desktop := []DesktopEntry{
		desktopEntry("broken.desktop", "Broken"),
		desktopEntry("fine.desktop", "Fine", "fine"),
	}

	if got := names(Merge(nil, desktop)); !slices.Equal(got, []string{"fine"}) {
		t.Fatalf("Merge = %v, want the entry with no Exec dropped", got)
	}
}

// TestMergeCarriesDesktopFields checks the fields a launcher draws a row from,
// including Detail falling back to Comment where there is no GenericName.
func TestMergeCarriesDesktopFields(t *testing.T) {
	generic := desktopEntry("files.desktop", "Files", "nautilus")
	generic.Generic = "File Manager"
	generic.Comment = "Access and organize files"
	generic.Icon = "org.gnome.Nautilus"
	generic.WorkDir = "/home/user"
	generic.Terminal = true
	generic.Keywords = []string{"folder", "manager"}

	commented := desktopEntry("notes.desktop", "Notes", "notes")
	commented.Comment = "Write things down"

	got := Merge(nil, []DesktopEntry{generic, commented})
	if len(got) != 2 {
		t.Fatalf("Merge = %v, want 2 entries", names(got))
	}
	e := got[0]
	if e.Detail != "File Manager" {
		t.Errorf("Detail = %q, want GenericName", e.Detail)
	}
	if e.Icon != "org.gnome.Nautilus" || e.Cwd != "/home/user" || !e.Terminal {
		t.Errorf("Icon/Cwd/Terminal = %q/%q/%v, want the desktop entry's own", e.Icon, e.Cwd, e.Terminal)
	}
	if e.Dir != "/usr/share/applications" {
		t.Errorf("Dir = %q, want the directory the .desktop file was found in", e.Dir)
	}
	if want := []string{"files", "folder", "manager"}; !slices.Equal(e.Aliases(), want) {
		t.Errorf("Aliases = %v, want %v", e.Aliases(), want)
	}
	if got[1].Detail != "Write things down" {
		t.Errorf("Detail = %q, want Comment where there is no GenericName", got[1].Detail)
	}
}
