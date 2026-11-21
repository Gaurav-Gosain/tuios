package vt_test

import (
	"sync"
	"testing"

	"tuios.dev/vt"
)

func TestModeMapReadersUnderRestore(t *testing.T) {
	emu := vt.NewEmulator(80, 24)

	const iterations = 3000
	var wg sync.WaitGroup

	snapshots := []map[int]bool{
		{7: true, 6: true, 1002: true, 1049: false},
		{7: false, 6: false, 1002: false, 1049: true},
	}
	wg.Go(func() {
		for i := range iterations {
			emu.RestoreModes(snapshots[i%2])
		}
	})

	reads := []string{
		"\x1b[?7$p",
		"\x1b[3;4H",
		"\r",
		"\x1b[?1h\x1b[?1l",
		"\x1b[?6h\x1b[?6l",
	}
	wg.Go(func() {
		for range iterations {
			for _, in := range reads {
				_, _ = emu.WriteString(in)
			}
		}
	})

	wg.Wait()
}
