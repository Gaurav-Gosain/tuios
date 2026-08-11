package session

import (
	"slices"
	"testing"
)

// TestCreatedSessionSurvivesSwitchingBack is the regression test for a session
// disappearing from the sidebar. Creating a session from inside another one is
// an attach to a name the daemon does not have yet; the cache the sidebar builds
// its rows from never heard about it, so the row lived only as long as the
// client stayed attached and was gone the moment it switched back.
func TestCreatedSessionSurvivesSwitchingBack(t *testing.T) {
	d, _ := startTestDaemon(t)
	makeSessionWithWindow(t, d, "origin")

	client := attachTestClient(t, "origin")

	if _, err := client.SwitchSession("spawned", 80, 24); err != nil {
		t.Fatalf("switch to a new session: %v", err)
	}
	if names := client.AvailableSessionNames(); !slices.Contains(names, "spawned") {
		t.Fatalf("a session created by attaching to it is missing from the cache: %v", names)
	}

	if _, err := client.SwitchSession("origin", 80, 24); err != nil {
		t.Fatalf("switch back: %v", err)
	}
	names := client.AvailableSessionNames()
	if !slices.Contains(names, "spawned") {
		t.Fatalf("the created session vanished after switching back: %v", names)
	}
	if !slices.Contains(names, "origin") {
		t.Fatalf("the original session is missing after switching back: %v", names)
	}
}

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
