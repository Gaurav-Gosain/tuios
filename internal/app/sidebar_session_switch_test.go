package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSidebarKeepsSessionCreatedFromInside walks the exact sequence a user
// reported: one session running, create a second from inside it, switch to it,
// then switch back. The rail is rebuilt from the client's session cache on every
// frame, so a session the cache never learned about survived only while it was
// the attached one and disappeared on the way back.
//
// The cache moves here through the same calls the real switch makes
// (NoteSession on attach, a listing on refresh), not by hand-editing rows.
func TestSidebarKeepsSessionCreatedFromInside(t *testing.T) {
	withSidebar(t, true, "left", config.SidebarDefaultWidth)

	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{{Name: "origin"}})
	m := &OS{
		Windows:      []*terminal.Window{{ID: "w1"}},
		SessionName:  "origin",
		Width:        120,
		DaemonClient: client,
	}

	if got := len(m.BuildSessionTree().Sessions); got != 1 {
		t.Fatalf("precondition: one session expected, got %d", got)
	}

	// Create "spawned" by attaching to it, then land on it.
	client.NoteSession("spawned")
	m.SessionName = "spawned"
	tree := m.BuildSessionTree()
	if !hasSession(tree.Sessions, "spawned") || !hasSession(tree.Sessions, "origin") {
		t.Fatalf("after creating a session the rail must show both, got %v", sessionIDs(tree.Sessions))
	}

	// Switch back to the original.
	m.SessionName = "origin"
	tree = m.BuildSessionTree()
	if !hasSession(tree.Sessions, "spawned") {
		t.Fatalf("the created session vanished from the rail after switching back: %v", sessionIDs(tree.Sessions))
	}

	// A poll that raced the creation must not take the row away either.
	client.UpdateSessionCache([]session.SessionInfo{{Name: "origin"}, {Name: "spawned"}})
	if !hasSession(m.BuildSessionTree().Sessions, "spawned") {
		t.Fatal("a refresh listing both sessions dropped one from the rail")
	}
}

func sessionIDs(sessions []sessiontree.Node) []string {
	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.ID)
	}
	return names
}
