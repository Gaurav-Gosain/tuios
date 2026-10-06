package tuie2e

import (
	"testing"
	"time"
)

// TestBareAttachLandsOnTheMostRecentSession is issue #486. The help says a
// bare `tuios attach` attaches to the most recent session. The daemon took the
// first session a range over its session map gave, and Go randomises map
// order, so with several sessions the client landed on any of them.
//
// Four sessions are made, then each round opens a window in one of them, which
// makes it the most recently active, and runs a bare attach. The rounds visit
// the oldest session, the newest, and the ones between, more than once and not
// in creation order, so neither map order nor creation order can pass all of
// them by luck: with four sessions a random pick passes eight rounds about
// once in 65,000 runs.
//
// How this could pass wrongly: the listing could show a client on the target
// that was left over from the round before. Each round waits for no session to
// have a client before it opens the window, so the only client is the new one.
func TestBareAttachLandsOnTheMostRecentSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	names := []string{"recent-a", "recent-b", "recent-c", "recent-d"}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	rounds := []string{"recent-c", "recent-a", "recent-d", "recent-b", "recent-a", "recent-c", "recent-b", "recent-d"}
	for i, target := range rounds {
		rows := waitForRows(t, base, func(rows []lsRow) bool {
			for _, r := range rows {
				if r.Attached {
					return false
				}
			}
			return len(rows) == len(names)
		})
		for _, r := range rows {
			if r.Attached {
				t.Fatalf("round %d: %s still has a client before the attach: %+v", i+1, r.Name, rows)
			}
		}

		// Opening a window is activity in the session, and it is what makes
		// this one the most recent.
		if out, err := tuiosCLI(t, base, "new-window", "-s", target); err != nil {
			t.Fatalf("round %d: new window in %s: %v: %s", i+1, target, err, out)
		}

		client := startIn(t, base, startOpts{args: []string{"attach"}})
		rows = waitForRows(t, base, func(rows []lsRow) bool {
			return attachedNames(rows) != ""
		})
		got := attachedNames(rows)
		if got != target {
			t.Fatalf("round %d: a bare attach landed on %q, want %q, the session with the latest activity\nrows: %+v\n%s",
				i+1, got, target, rows, client.Snapshot())
		}
		if err := client.Close(); err != nil {
			t.Logf("round %d: close the client: %v", i+1, err)
		}
		// Two activities in the same instant would tie, so the next round
		// starts on a later clock reading.
		time.Sleep(20 * time.Millisecond)
	}
}

// attachedNames is the name of the one session with a client, or "" when no
// session or more than one has one. Two attached sessions read as "", so the
// wait above keeps going instead of taking a half-finished listing.
func attachedNames(rows []lsRow) string {
	name := ""
	for _, r := range rows {
		if !r.Attached {
			continue
		}
		if name != "" {
			return ""
		}
		name = r.Name
	}
	return name
}
