package session

import (
	"bytes"
	"encoding/gob"
	"testing"
)

func fingerprintFixture() *SessionState {
	return &SessionState{
		Name:             "work",
		DisplayName:      "Work",
		CurrentWorkspace: 2,
		MasterRatio:      0.55,
		AutoTiling:       true,
		Width:            120,
		Height:           40,
		FocusedWindowID:  "win-2",
		Windows: []WindowState{
			{ID: "win-1", Title: "sh", X: 0, Y: 0, Width: 60, Height: 38, Workspace: 1, PTYID: "pty-1"},
			{ID: "win-2", Title: "vim", X: 60, Y: 0, Width: 60, Height: 38, Workspace: 1, PTYID: "pty-2"},
		},
		WorkspaceFocus: map[int]string{1: "win-1", 2: "win-2", 3: "win-1", 4: "win-2", 5: "win-1"},
		WorkspaceNames: map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five"},
		WindowToBSPID:  map[string]int{"win-1": 1, "win-2": 2, "win-3": 3, "win-4": 4, "win-5": 5},
		WorkspaceTrees: map[int]*SerializedBSPTree{
			1: {Root: &SerializedBSPNode{SplitType: 1, SplitRatio: 0.5,
				Left:  &SerializedBSPNode{WindowID: 1},
				Right: &SerializedBSPNode{WindowID: 2}}},
			2: nil,
		},
		Options: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"},
		Version: 7,
	}
}

// TestStateFingerprintIsStableAcrossPasses is the property the whole suppression
// rests on: the same state must fingerprint the same every time it is asked.
//
// It is not free. The obvious implementation - hash an encoding of the state -
// does not have it, because gob and JSON both walk a Go map in the map's own
// iteration order, and Go randomises that. This test builds a state carrying
// five multi-entry maps and asks many times, so a fingerprint that depended on
// map order would almost certainly disagree with itself here.
func TestStateFingerprintIsStableAcrossPasses(t *testing.T) {
	want := StateFingerprint(fingerprintFixture())
	for i := range 200 {
		if got := StateFingerprint(fingerprintFixture()); got != want {
			t.Fatalf("pass %d fingerprinted %016x, the first pass %016x: "+
				"an unstable fingerprint would let every unchanged sync through, "+
				"which is the whole cost this exists to remove", i, got, want)
		}
	}
}

// TestGobEncodingIsNotStable is the discriminating control for the test above,
// and it is written deliberately as one. It passes whether or not
// StateFingerprint exists: its job is to show that the cheaper implementation
// really is unusable, so the hand-written one is not cargo.
//
// If this ever fails, Go has started ordering map iteration and the fingerprint
// could be simplified.
func TestGobEncodingIsNotStable(t *testing.T) {
	enc := func() []byte {
		st := fingerprintFixture()
		// gob refuses a nil map value, so the nil tree the fingerprint fixture
		// carries on purpose is dropped for this one. It is not what is under
		// test here, and it is a second reason an encoding is the wrong basis.
		delete(st.WorkspaceTrees, 2)
		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(st); err != nil {
			t.Fatalf("gob encode: %v", err)
		}
		return buf.Bytes()
	}
	first := enc()
	for range 200 {
		if !bytes.Equal(first, enc()) {
			return // as expected: the encoding is not stable
		}
	}
	t.Skip("gob encoded this state identically 200 times; map iteration may have " +
		"become ordered, in which case StateFingerprint could hash an encoding instead")
}

// TestStateFingerprintNoticesEveryChange keeps the fingerprint honest in the
// other direction. A fingerprint that ignored a field would suppress a sync
// that had something to say, and the peer would never learn about it.
func TestStateFingerprintNoticesEveryChange(t *testing.T) {
	base := StateFingerprint(fingerprintFixture())

	cases := map[string]func(*SessionState){
		"name":              func(s *SessionState) { s.Name = "other" },
		"display name":      func(s *SessionState) { s.DisplayName = "Other" },
		"accent":            func(s *SessionState) { s.Accent = "red" },
		"restored mark":     func(s *SessionState) { s.Restored = true },
		"focused window":    func(s *SessionState) { s.FocusedWindowID = "win-1" },
		"current workspace": func(s *SessionState) { s.CurrentWorkspace = 3 },
		"master ratio":      func(s *SessionState) { s.MasterRatio = 0.56 },
		"auto tiling":       func(s *SessionState) { s.AutoTiling = false },
		"width":             func(s *SessionState) { s.Width = 121 },
		"height":            func(s *SessionState) { s.Height = 41 },
		"next bsp id":       func(s *SessionState) { s.NextBSPWindowID = 9 },
		"tiling scheme":     func(s *SessionState) { s.TilingScheme = 2 },
		"layout mode":       func(s *SessionState) { s.LayoutMode = "scrolling" },
		"workspace count":   func(s *SessionState) { s.NumWorkspaces = 4 },
		"version":           func(s *SessionState) { s.Version = 8 },
		"base version":      func(s *SessionState) { s.BaseVersion = 3 },
		"window x":          func(s *SessionState) { s.Windows[0].X = 1 },
		"window width":      func(s *SessionState) { s.Windows[0].Width = 59 },
		"window z":          func(s *SessionState) { s.Windows[0].Z = 5 },
		"window title":      func(s *SessionState) { s.Windows[0].Title = "other" },
		"window minimized":  func(s *SessionState) { s.Windows[0].Minimized = true },
		"window workspace":  func(s *SessionState) { s.Windows[0].Workspace = 2 },
		"window alt screen": func(s *SessionState) { s.Windows[0].IsAltScreen = true },
		"window unplaced":   func(s *SessionState) { s.Windows[0].Unplaced = true },
		"agent state":       func(s *SessionState) { s.Windows[0].AgentState = AgentStateWorking },
		"foreground cmd":    func(s *SessionState) { s.Windows[0].ForegroundCmd = "nvim" },
		"window dropped":    func(s *SessionState) { s.Windows = s.Windows[:1] },
		"window order":      func(s *SessionState) { s.Windows[0], s.Windows[1] = s.Windows[1], s.Windows[0] },
		"workspace focus":   func(s *SessionState) { s.WorkspaceFocus[1] = "win-2" },
		"workspace name":    func(s *SessionState) { s.WorkspaceNames[1] = "uno" },
		"workspace order":   func(s *SessionState) { s.WorkspaceOrder = []int{2, 1} },
		"bsp id map":        func(s *SessionState) { s.WindowToBSPID["win-1"] = 9 },
		"tree ratio":        func(s *SessionState) { s.WorkspaceTrees[1].Root.SplitRatio = 0.6 },
		"tree shape":        func(s *SessionState) { s.WorkspaceTrees[1].Root.Left = nil },
		"tree added":        func(s *SessionState) { s.WorkspaceTrees[3] = &SerializedBSPTree{} },
		"option value":      func(s *SessionState) { s.Options["a"] = "2" },
		"option added":      func(s *SessionState) { s.Options["f"] = "6" },
	}

	for what, mutate := range cases {
		t.Run(what, func(t *testing.T) {
			s := fingerprintFixture()
			mutate(s)
			if got := StateFingerprint(s); got == base {
				t.Errorf("changing the %s did not change the fingerprint, so a sync "+
					"carrying that change would be suppressed and the peer would never see it", what)
			}
		})
	}
}

// TestStateFingerprintOfNil pins the degenerate case rather than leaving it to
// a panic at a call site.
func TestStateFingerprintOfNil(t *testing.T) {
	if StateFingerprint(nil) != 0 {
		t.Error("a nil state should fingerprint as zero")
	}
}
