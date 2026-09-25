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
//
// Negative control: dropping the Pinned term makes the hand-picked case fail,
// which is the behaviour that was on screen.
func TestWhichListingsAreJudgedAgainstThePane(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload ReadDirPayload
		want    bool
	}{
		{"a folder the user walked into", ReadDirPayload{WindowID: "w1", Dir: "/somewhere/else", Pinned: true}, false},
		{"the pane's own folder", ReadDirPayload{WindowID: "w1", Dir: "/src"}, true},
		{"a listing about no pane", ReadDirPayload{Dir: "/src"}, false},
	} {
		if got := spoofCheckWanted(tc.payload); got != tc.want {
			t.Errorf("%s: spoof check wanted = %v, want %v", tc.name, got, tc.want)
		}
	}
}
