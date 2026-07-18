package vt_test

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestModeConcurrentAccess exercises the mode map from a writer goroutine
// (guest output toggling DEC modes, which runs setMode) against reader
// goroutines calling the public mode accessors, mirroring the PTY reader vs
// input/render goroutine split in the real app. Before the modesMu guard this
// tripped the runtime's concurrent map read/write detector; run with -race.
func TestModeConcurrentAccess(t *testing.T) {
	emu := vt.NewEmulator(80, 24)

	const iterations = 2000
	var wg sync.WaitGroup

	// Writer: toggle mouse, cursor-keys, focus and bracketed-paste modes.
	toggles := [][]byte{
		[]byte("\x1b[?1002h\x1b[?1003h\x1b[?1h\x1b[?1004h\x1b[?2004h"),
		[]byte("\x1b[?1002l\x1b[?1003l\x1b[?1l\x1b[?1004l\x1b[?2004l"),
	}
	wg.Go(func() {
		for i := range iterations {
			_, _ = emu.Write(toggles[i%2])
		}
	})

	// Readers: hit every accessor that reads the mode map.
	readers := []func(){
		func() { _ = emu.SupportsMotionEvents() },
		func() { _ = emu.ApplicationCursorKeys() },
		func() {
			_ = emu.EncodeMouseEvent(uv.MouseWheelEvent{X: 1, Y: 1, Button: uv.MouseWheelUp})
		},
	}
	for _, read := range readers {
		wg.Go(func() {
			for range iterations {
				read()
			}
		})
	}

	wg.Wait()
}
