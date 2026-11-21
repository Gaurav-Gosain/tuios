package app

import "testing"

func navWithFiles(entries ...string) []sidebarNavRow {
	nav := []sidebarNavRow{
		{Kind: sidebarRowSession, SessionID: "s1"},
		{Kind: sidebarRowSession, SessionID: "s2"},
		{Kind: sidebarRowFileUp, WindowID: ".."},
	}
	for _, e := range entries {
		nav = append(nav, sidebarNavRow{Kind: sidebarRowFileEntry, WindowID: e})
	}
	return nav
}

// TestWalkingIntoAFolderKeepsTheCursorInTheListing pins that entering a folder
// leaves the cursor on the new listing rather than at the top of the rail.
func TestWalkingIntoAFolderKeepsTheCursorInTheListing(t *testing.T) {
	m := &OS{}
	m.filesView.Gen = 7
	m.followFileRow("")

	gone := sidebarNavRow{Kind: sidebarRowFileEntry, WindowID: "src"}
	nav := navWithFiles("main.go", "README.md")
	m.sidebarPublishNav(nav, gone, true)

	if m.SidebarCursor == 0 {
		t.Fatal("the cursor fell back to the top of the rail")
	}
	row := nav[m.SidebarCursor]
	if row.Kind != sidebarRowFileUp {
		t.Errorf("the cursor landed on %v, want the listing's first row", row)
	}
	if m.sidebarFollowFile {
		t.Error("the follow request was not consumed, so it would fire again on the next frame")
	}
}

// TestAFollowWaitsForItsOwnListing pins that the cursor does not move on a frame
// drawn while the new listing is still being read, when the rows on screen are
// still the old ones.
func TestAFollowWaitsForItsOwnListing(t *testing.T) {
	m := &OS{}
	m.filesView.Gen = 4
	m.followFileRow("")
	m.filesView.Loading = true

	nav := navWithFiles("a", "b")
	held := sidebarNavRow{Kind: sidebarRowFileEntry, WindowID: "b"}
	m.sidebarPublishNav(nav, held, true)

	if got := nav[m.SidebarCursor]; got != held {
		t.Errorf("the cursor moved to %v while the listing was loading, want it held on %v", got, held)
	}
	if !m.sidebarFollowFile {
		t.Error("the follow request was dropped before its listing arrived")
	}
}
