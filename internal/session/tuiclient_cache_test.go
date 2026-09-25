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

	stamp := c.listingStamp() // a background poll goes out
	c.NoteSession("spawned")  // the session is created while it is in flight
	c.applySessionListing([]SessionInfo{{Name: "origin"}}, stamp)

	if names := c.AvailableSessionNames(); !slices.Contains(names, "spawned") {
		t.Fatalf("an in-flight listing dropped a session created after it was sent: %v", names)
	}

	// A listing requested after the creation is current, so a session that really
	// went away still leaves.
	c.applySessionListing([]SessionInfo{{Name: "origin"}}, c.listingStamp())
	if names := c.AvailableSessionNames(); slices.Contains(names, "spawned") {
		t.Fatalf("a current listing failed to drop a gone session: %v", names)
	}
}

// TestUnchangedListingHoldsCacheGen keeps the sidebar's render cache useful: the
// poll runs every few seconds and almost always brings back the same listing, so
// only a listing that would draw differently may bump the generation the rail
// keys its cache on.
func TestUnchangedListingHoldsCacheGen(t *testing.T) {
	c := NewTUIClient()
	listing := []SessionInfo{
		{Name: "one", WindowCount: 1, Windows: []WindowSummary{{ID: "w1", Title: "vim"}}},
		{Name: "two"},
	}
	c.UpdateSessionCache(listing)
	gen := c.CacheGen()

	c.UpdateSessionCache([]SessionInfo{
		{Name: "one", WindowCount: 1, Windows: []WindowSummary{{ID: "w1", Title: "vim"}}},
		{Name: "two"},
	})
	if c.CacheGen() != gen {
		t.Fatal("an identical listing bumped the cache generation, forcing a rail rebuild")
	}

	c.UpdateSessionCache([]SessionInfo{
		{Name: "one", WindowCount: 1, Windows: []WindowSummary{{ID: "w1", Title: "htop"}}},
		{Name: "two"},
	})
	if c.CacheGen() == gen {
		t.Fatal("a changed window title left the cache generation alone, so the rail would not redraw")
	}
}
