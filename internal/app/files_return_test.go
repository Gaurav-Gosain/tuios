package app

import (
	"testing"
)

// paneFilesOS is a files section opened the way the sync opens it: unpinned,
// tied to the focused pane, whose cwd is the directory named. The link-style
// OpenFileView is the only real entry a test can call, so the pane-owned state
// is set by hand after it.
func paneFilesOS(t *testing.T, dir string) *OS {
	t.Helper()
	m := filesOS(t, dir, "")
	m.Windows[0].Cwd = dir
	m.filesView.Origin = m.Windows[0].ID
	m.filesView.Pinned = false
	return m
}

// TestFileViewReturnGivesTheListingBackToThePane: a listing steered into a
// subfolder is pinned; returning unpins it and asks for the pane's directory
// again, after which the sync keeps it following.
func TestFileViewReturnGivesTheListingBackToThePane(t *testing.T) {
	dir := fileViewTree(t)
	m := paneFilesOS(t, dir)

	sub := dir + "/apple"
	cmd := m.requestFileList(sub, m.filesView.Origin, true)
	if cmd == nil {
		t.Fatal("no read was scheduled for the subfolder")
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
	if m.filesView.Dir != sub {
		t.Fatalf("the walk did not move the listing: %q", m.filesView.Dir)
	}
	if !m.filesView.Pinned {
		t.Fatal("steering the listing left it unpinned")
	}

	cmd = m.FileViewReturn()
	if cmd == nil {
		t.Fatal("return scheduled no read")
	}
	if m.filesView.Pinned {
		t.Error("return left the listing pinned to the steered directory")
	}
	msg, ok = cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the return read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
	if m.filesView.Dir != dir {
		t.Fatalf("return landed on %q, want the pane's directory", m.filesView.Dir)
	}
}

// TestRailReturnTokenClicks takes the return control through the real mouse
// path: click a folder row, then click the token, and the listing is back on
// the pane's directory and following again.
func TestRailReturnTokenClicks(t *testing.T) {
	dir := fileViewTree(t)
	m := paneFilesOS(t, dir)

	var entry sidebarRowHit
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowFileEntry && h.WindowID == "apple" {
			entry = h
		}
	}
	if entry.Kind != sidebarRowFileEntry {
		t.Fatalf("the rail published no row for \"apple\": %v", entryNames(m))
	}
	if !m.SidebarClick(entry.X0+2, entry.Y0, false) {
		t.Fatal("the rail refused a press on the folder row")
	}
	cmd := m.TakeSidebarCmd()
	if cmd == nil {
		t.Fatal("entering the folder scheduled no read")
	}
	msg, ok := cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)

	railLines(t, m)
	var ret sidebarRowHit
	found := false
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowFileReturn {
			ret, found = h, true
		}
	}
	if !found {
		t.Fatalf("a pane-owned listing drew no return token (origin %q, pinned %v)",
			m.filesView.Origin, m.filesView.Pinned)
	}

	if !m.SidebarClick(ret.X0, ret.Y0, false) {
		t.Fatal("the rail refused a press on the return token")
	}
	cmd = m.TakeSidebarCmd()
	if cmd == nil {
		t.Fatal("a press on the return token scheduled no read")
	}
	msg, ok = cmd().(fileListMsg)
	if !ok {
		t.Fatalf("the return read answered with %T, not a listing", msg)
	}
	m.HandleFileList(msg)
	if m.filesView.Dir != dir {
		t.Fatalf("return landed on %q, want the pane's directory", m.filesView.Dir)
	}
	if m.filesView.Pinned {
		t.Error("return left the listing pinned")
	}
}
