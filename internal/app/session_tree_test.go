package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestBuildSessionTreeSkipsNilWindows guards against a nil entry in m.Windows
// (a torn-down window mid-close) crashing the tree build instead of just being
// skipped, mirroring currentSessionInput's nil check.
func TestBuildSessionTreeSkipsNilWindows(t *testing.T) {
	win := &terminal.Window{ID: "w1"}
	m := &OS{Settings: config.Global, Windows: []*terminal.Window{win, nil}, FocusedWindow: 0}

	tree := m.BuildSessionTree()
	if len(tree.Sessions[0].Children) != 1 {
		t.Fatalf("Children = %d, want 1 (nil window skipped)", len(tree.Sessions[0].Children))
	}
}
