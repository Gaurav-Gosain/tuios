package vt_test

// The mode map is written from one goroutine and read from another, and a Go
// map read racing a map write is not a data race the program survives: the
// runtime throws "concurrent map read and map write", which is a fatal error no
// recover can catch. The emulator runs in the daemon process, so that takes
// every pane in every session down together rather than the one pane that
// misbehaved.
//
// TestModeConcurrentAccess already covers the accessors that were guarded when
// modesMu was introduced. This covers the other side: the guest-facing
// sequences that read the map on the parser goroutine while the session layer
// restores a snapshot on its own. internal/session/session.go calls
// RestoreModes on every reattach, and internal/input reaches for the same
// entries, so the two goroutines are the real ones rather than a contrived
// pair.

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func TestModeMapReadersUnderRestore(t *testing.T) {
	emu := vt.NewEmulator(80, 24)

	const iterations = 3000
	var wg sync.WaitGroup

	// The session layer restoring a daemon snapshot, which is what happens on
	// every reattach.
	snapshots := []map[int]bool{
		{7: true, 6: true, 1002: true, 1049: false},
		{7: false, 6: false, 1002: false, 1049: true},
	}
	wg.Go(func() {
		for i := range iterations {
			emu.RestoreModes(snapshots[i%2])
		}
	})

	// Guest output that reads the map on the parser goroutine. Each of these
	// reached the map without taking the lock:
	//
	//	CSI ? 7 $ p   DECRQM, which looks the mode up to report it
	//	CSI H         CUP, which consults DECOM to decide what the row means
	//	CR            carriage return, same lookup for the left margin
	//	CSI ? 1 h     any mode set at all, which reads the old setting first
	//	              to honour a permanently-set mode
	reads := []string{
		"\x1b[?7$p",
		"\x1b[3;4H",
		"\r",
		"\x1b[?1h\x1b[?1l",
		"\x1b[?6h\x1b[?6l",
	}
	// One writer, because guest bytes arrive from one PTY reader. The race
	// under test is between that goroutine and the session layer's restore,
	// not between two writers.
	wg.Go(func() {
		for range iterations {
			for _, in := range reads {
				_, _ = emu.WriteString(in)
			}
		}
	})

	wg.Wait()
}
