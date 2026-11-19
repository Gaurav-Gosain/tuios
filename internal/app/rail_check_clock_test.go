package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// TestRailCheckOneClockPerFrame: a machine that is down says "seen 3s ago" on
// its header, and the rail key folds that label. The key, the draw and the
// rail check each read the clock, so a second that turned between the key and
// the check's rebuild made the check report a stale rail ("seen 3s ago"
// cached, "seen 4s ago" built). It failed TestASilentLinkIsFoundAndTheRail-
// StillOpensItsSessions now and then. Now a frame reads the clock once.
//
// The clock here gives the frame's first read the second the cached rail was
// built in, and every later read the next second: the turn lands right after
// the key.
//
// Negative control, confirmed red: with the key, the header and the rebuild
// each reading railTimeNow, the check reports row 3, "seen 3s ago" against
// "seen 4s ago", the report CI showed.
func TestRailCheckOneClockPerFrame(t *testing.T) {
	m, _ := shippedRailOS(t, federation.StatusUnreachable, "idle")
	t0 := time.Unix(1_800_000_000, 0)
	for i := range m.FederationHosts {
		m.FederationHosts[i].LastOK = t0.Add(-3 * time.Second).Unix()
	}
	m.federationGen++

	check := filepath.Join(t.TempDir(), "rail-check.log")
	prevPath, prevNow := railCheckPath, railTimeNow
	t.Cleanup(func() { railCheckPath, railTimeNow = prevPath, prevNow })
	railTimeNow = func() time.Time { return t0 }
	m.sidebarPanelLines() // the cached rail, in the second t0

	reads := 0
	railTimeNow = func() time.Time {
		reads++
		if reads == 1 {
			return t0
		}
		return t0.Add(time.Second)
	}
	railCheckPath = check
	builds := m.tickStats.Rail
	m.sidebarPanelLines() // served from the cache, and checked
	if m.tickStats.Rail != builds {
		t.Fatal("the second frame built the rail again, so it tested no cache hit")
	}
	if b, err := os.ReadFile(check); err == nil && len(b) > 0 {
		t.Fatalf("ASSERTION: the rail check reported a stale rail when the clock turned during the frame:\n%s", b)
	}
	if reads != 1 {
		t.Errorf("the frame read the clock %d times, want once", reads)
	}
}
