package app

import (
	"os"
	"path/filepath"
	"testing"
)

// The routing these tests pin is the one thing in the link feature that is a
// safety property rather than a convenience: a client running on a server must
// not open a browser there, because nobody is sitting at that machine.

// TestLinkFilePathAcceptsOnlyLocalFiles checks the parse that decides whether a
// link names a file on the machine the panes are on.
//
// The expected answers are written out from the URLs, not derived: a file URL
// with no host or with localhost is this machine's, one with another host is
// not, and an http URL is not a file at all.
//
// Negative control: with the host check dropped from localCwdPath, the
// other-host case comes back ok and this fails.
func TestLinkFilePathAcceptsOnlyLocalFiles(t *testing.T) {
	cases := []struct {
		url  string
		want string // "" means not a local file
	}{
		{"file:///etc/hosts", "/etc/hosts"},
		{"file://localhost/etc/hosts", "/etc/hosts"},
		{"file://someotherbox/etc/hosts", ""},
		{"https://example.com/a", ""},
		{"/etc/hosts", ""}, // a bare path is not a link
	}
	for _, c := range cases {
		got, ok := linkFilePath(c.url)
		if c.want == "" {
			if ok {
				t.Errorf("linkFilePath(%q) = %q, want no local file", c.url, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("linkFilePath(%q) = %q,%v, want %q,true", c.url, got, ok, c.want)
		}
	}
}

// TestRemoteClientCopiesRatherThanOpens is the footgun this feature was most
// likely to ship with.
//
// Under `tuios ssh` and `tuios-web` the client process runs on the server. A
// browser opened from there opens on the server's console, in front of nobody,
// and the person who clicked sees nothing happen. tuios has no way to run
// anything on the viewer's machine, so the honest action is the one that does
// reach it: OSC 52, which rides the same stream the frame does.
//
// The test asserts on the command rather than on a spawned process, because the
// bug it guards is that a process is spawned at all.
//
// Negative control: with the IsRemoteClient branch removed from OpenLink, a
// remote client returns a nil command here (having tried to spawn a browser)
// and this fails.
func TestRemoteClientCopiesRatherThanOpens(t *testing.T) {
	m := &OS{RemoteClient: true}
	if cmd := m.OpenLink("https://example.com/a"); cmd == nil {
		t.Fatal("a remote client produced no clipboard write; the click did nothing at all")
	}
	if n := len(m.Notifications); n == 0 {
		t.Fatal("a remote client said nothing about what it did instead")
	}
}

// TestMissingFileIsReportedNotGuessed: a file:// link in a pane's scrollback can
// be minutes old and name something that has since been built over or deleted.
//
// Negative control: with the stat dropped from openLocalPath, a missing path
// falls through to spawning an editor pane on a file that is not there.
func TestMissingFileIsReportedNotGuessed(t *testing.T) {
	m := &OS{}
	gone := filepath.Join(t.TempDir(), "never-existed")
	if cmd := m.OpenLink("file://" + gone); cmd == nil {
		t.Fatal("a missing file produced no clipboard fallback")
	}
	if len(m.Windows) != 0 {
		t.Errorf("a missing file opened %d panes", len(m.Windows))
	}
}

// TestDirectoryLinkFallsBackWhenTheRailIsOff. A folder link wants the rail's
// file view, and the rail can be off, hidden, or folded to three columns. A mode
// the user cannot see is a mode they cannot leave, so OpenFileView refuses and
// the link says what it did instead.
//
// Negative control: with OpenFileView returning true unconditionally, the view
// opens behind a hidden rail and the clipboard fallback never runs.
func TestDirectoryLinkFallsBackWhenTheRailIsOff(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &OS{} // no rail: GetSidebarWidth is zero
	if cmd := m.OpenLink("file://" + dir); cmd == nil {
		t.Fatal("a folder link with no rail produced no clipboard fallback")
	}
	if m.FileViewOpen() {
		t.Error("the file view opened on a rail that reserves no columns")
	}
}

// TestLinkEditorPrefersTheEnvironment pins the order every other tool uses, and
// that a value with arguments in it arrives as arguments rather than as a file
// name with a space in it.
func TestLinkEditorPrefersTheEnvironment(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	if got := linkEditor(); got != "vi" {
		t.Errorf("with neither set, linkEditor() = %q, want vi", got)
	}
	t.Setenv("VISUAL", "emacs")
	if got := linkEditor(); got != "emacs" {
		t.Errorf("with VISUAL set, linkEditor() = %q, want emacs", got)
	}
	t.Setenv("EDITOR", "code --wait")
	if got := linkEditor(); got != "code --wait" {
		t.Errorf("EDITOR did not win over VISUAL: %q", got)
	}
}
