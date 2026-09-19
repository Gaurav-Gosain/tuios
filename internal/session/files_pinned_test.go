package session

import "testing"

// Which listings are judged against the pane's own shell.
//
// The rail's file section can be walked: click a folder and it lists that
// folder, click ".." and it goes up. The spoof check asks whether the pane
// announced a directory its shell is not in, and running it on a folder the
// user walked into asks that question about a directory the pane never
// claimed. Every step away from the pane's own folder came back marked "read
// only: wrong folder".

// TestAHandPickedFolderIsNotJudgedAgainstThePane is the report.
//
// Negative control: dropping the Pinned term makes this fail, which is the
// behaviour that was on screen.
func TestAHandPickedFolderIsNotJudgedAgainstThePane(t *testing.T) {
	if spoofCheckWanted(ReadDirPayload{WindowID: "w1", Dir: "/somewhere/else", Pinned: true}) {
		t.Error("a folder the user walked into was judged against the pane's shell")
	}
}

// TestAPaneSteeredListingIsStillJudged. The check is the whole reason the
// daemon answers this question, and it has to survive the fix.
func TestAPaneSteeredListingIsStillJudged(t *testing.T) {
	if !spoofCheckWanted(ReadDirPayload{WindowID: "w1", Dir: "/src"}) {
		t.Error("the pane's own folder is no longer checked")
	}
}

// TestAListingAboutNoPaneIsNotJudged. Without a pane there is no claim to
// compare against, which is what the payload's own comment has always said.
func TestAListingAboutNoPaneIsNotJudged(t *testing.T) {
	if spoofCheckWanted(ReadDirPayload{Dir: "/src"}) {
		t.Error("a listing about no pane was judged against one")
	}
}

// TestAnOlderClientStillGetsTheCheck. Pinned is a new field, and a client
// built before it sends the zero value. False has to mean "the pane steered
// this", so the check keeps working for every client that does not know to ask
// for it.
func TestAnOlderClientStillGetsTheCheck(t *testing.T) {
	old := ReadDirPayload{WindowID: "w1", Dir: "/src"} // no Pinned field set
	if !spoofCheckWanted(old) {
		t.Error("a client that does not set Pinned lost the check")
	}
}
