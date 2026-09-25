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

// TestMergeKeepsTheSupersededBinaryReachable is the one way this merge could
// make something worse rather than better. Supersession deletes the $PATH row,
// so unless the row that replaced it answers to the binary name, the single
// name the user has always typed stops finding the program. On a real desktop
// this cost 13 of 39 superseded programs their own name, zed among them.
func TestMergeKeepsTheSupersededBinaryReachable(t *testing.T) {
	path := []Entry{pathEntry("zeditor", "/usr/bin")}
	desktop := []DesktopEntry{desktopEntry("dev.zed.Zed.desktop", "Zed", "zeditor")}

	got := Merge(path, desktop)
	if len(got) != 1 {
		t.Fatalf("Merge = %v, want the one superseded row", names(got))
	}
	e := got[0]
	if e.Binary != "zeditor" {
		t.Fatalf("Binary = %q, want the program it replaced", e.Binary)
	}
	if !slices.Contains(e.Aliases(), "zeditor") {
		t.Fatalf("Aliases = %v, want the binary name among them", e.Aliases())
	}
}

// TestMergeWillNotSupersedeAGenericLauncher keeps a wrapper's own program in
// the list. An Exec of "sh -c ..." does not make the entry a replacement for
// /bin/sh, and "xdg-open some://url" does not make it a replacement for
// xdg-open; both really happened, and typing either name then found neither the
// tool nor anything sensible.
func TestMergeWillNotSupersedeAGenericLauncher(t *testing.T) {
	path := []Entry{pathEntry("sh", "/usr/bin"), pathEntry("xdg-open", "/usr/bin")}
	desktop := []DesktopEntry{
		desktopEntry("emacsclient.desktop", "Emacs (Client)", "sh", "-c", "exec emacsclient"),
		desktopEntry("rocket-league.desktop", "Rocket League", "xdg-open", "heroic://launch"),
	}

	got := Merge(path, desktop)
	for _, want := range []string{"sh", "xdg-open"} {
		if !slices.Contains(names(got), want) {
			t.Errorf("Merge = %v, want %q kept", names(got), want)
		}
	}
	for _, e := range got {
		if e.Binary != "" {
			t.Errorf("%q claims to stand in for %q, but a wrapper is not the program it runs", e.Label(), e.Binary)
		}
	}
}
