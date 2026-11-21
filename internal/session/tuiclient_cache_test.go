package session

import (
	"slices"
	"testing"
)

// TestListingCannotDropAJustCreatedSession pins the no-regression rule: a
// listing answered from a snapshot taken before a session was created is an
// older picture than the cache holds, so it may not remove that session. A
// listing requested afterwards is authoritative and may.
func TestListingCannotDropAJustCreatedSession(t *testing.T) {
	c := NewTUIClient()
	c.UpdateSessionCache([]SessionInfo{{Name: "origin"}})

	stamp := c.listingStamp()
	c.NoteSession("spawned")
	c.applySessionListing([]SessionInfo{{Name: "origin"}}, stamp)

	if names := c.AvailableSessionNames(); !slices.Contains(names, "spawned") {
		t.Fatalf("an in-flight listing dropped a session created after it was sent: %v", names)
	}

	c.applySessionListing([]SessionInfo{{Name: "origin"}}, c.listingStamp())
	if names := c.AvailableSessionNames(); slices.Contains(names, "spawned") {
		t.Fatalf("a current listing failed to drop a gone session: %v", names)
	}
}
