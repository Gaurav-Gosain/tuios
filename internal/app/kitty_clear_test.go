package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestKittyClearOnED2 reproduces the youterm multi-thumbnail scrolling bug.
// Apps like youterm transmit each thumbnail with ImageID=0 ("auto-assign").
// Between frames, youterm writes ESC[2J to clear, then re-transmits at new
// positions. tuios must allocate a fresh host ID per transmit (so multiple
// thumbnails coexist) AND emit explicit kitty delete commands on ED 2 (so
// stale placements from prior frames don't stack up on the host terminal).
func TestKittyClearOnED2(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		ForceEnable: true,
		Output:      devnull,
	})
	kp.enabled = true

	winID := "test-window-id-abcdef12"

	emu := vt.NewEmulator(80, 24)
	clearCB := func() { kp.ClearWindow(winID) }
	emu.KittyMainState().SetClearCallback(clearCB)
	emu.KittyAltState().SetClearCallback(clearCB)

	// youterm enters alt screen immediately. The VT has separate KittyState
	// objects for main / alt screens — if the callback is only registered on
	// the currently-active state at setup time, the alt-screen ClearPlacements
	// fires with nil callback and no deletes reach the host.
	_, _ = emu.Write([]byte("\x1b[?1049h"))
	emu.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cursorPos := emu.CursorPosition()
		kp.ForwardCommand(
			cmd, rawData, winID,
			0, 0, 80, 24,
			0, 0,
			cursorPos.X, cursorPos.Y,
			0,
			false,
			nil,
		)
	})

	tmpFile, err := os.CreateTemp("", "youterm-thumb-test-")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tmpFile.Write(bytes.Repeat([]byte{0x00}, 100))
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	encodedPath := base64.StdEncoding.EncodeToString([]byte(tmpFile.Name()))

	frame := func() []byte {
		var b bytes.Buffer
		b.WriteString("\x1b[2J\x1b[H")
		for _, row := range []int{5, 11, 17} {
			fmt.Fprintf(&b, "\x1b[%d;3H", row)
			b.WriteString("\x1b_Ga=T,t=f,f=24,s=400,v=300,c=20,r=5,C=1,q=2;")
			b.WriteString(encodedPath)
			b.WriteString("\x1b\\")
		}
		return b.Bytes()
	}

	_, _ = emu.Write(frame())

	kp.mu.Lock()
	frame1Count := len(kp.placements[winID])
	frame1HostIDs := make([]uint32, 0, frame1Count)
	for hid := range kp.placements[winID] {
		frame1HostIDs = append(frame1HostIDs, hid)
	}
	kp.mu.Unlock()
	if frame1Count != 3 {
		t.Fatalf("frame1: expected 3 placements tracked (one per thumbnail), got %d (hostIDs=%v)", frame1Count, frame1HostIDs)
	}

	_ = kp.FlushPending()

	_, _ = emu.Write(frame())

	kp.mu.Lock()
	out := string(kp.pendingOutput)
	kp.mu.Unlock()

	for _, hid := range frame1HostIDs {
		pattern := fmt.Sprintf("\x1b_Ga=d,d=i,i=%d", hid)
		if !strings.Contains(out, pattern) {
			t.Errorf("BUG: frame 2 output missing delete for frame 1 hostID=%d; previous thumbnails will persist on host", hid)
		}
	}
}

// TestKittyNoStaleAccumulation simulates repeated scroll redraws and verifies
// the placement map size stays bounded at the per-frame count (3 here) — no
// accumulation of stale placements that would eventually leak to the host.
func TestKittyNoStaleAccumulation(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		ForceEnable: true,
		Output:      devnull,
	})
	kp.enabled = true

	winID := "test-window-id-abcdef12"

	emu := vt.NewEmulator(80, 40)
	clearCB := func() { kp.ClearWindow(winID) }
	emu.KittyMainState().SetClearCallback(clearCB)
	emu.KittyAltState().SetClearCallback(clearCB)
	emu.SetKittyPassthroughFunc(func(cmd *vt.KittyCommand, rawData []byte) {
		cursorPos := emu.CursorPosition()
		kp.ForwardCommand(
			cmd, rawData, winID,
			0, 0, 80, 40,
			0, 0,
			cursorPos.X, cursorPos.Y,
			0,
			emu.IsAltScreen(),
			nil,
		)
	})

	_, _ = emu.Write([]byte("\x1b[?1049h"))

	tmpFile, err := os.CreateTemp("", "youterm-thumb-accum-test-")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tmpFile.Write(bytes.Repeat([]byte{0x00}, 100))
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	encodedPath := base64.StdEncoding.EncodeToString([]byte(tmpFile.Name()))

	frame := func() []byte {
		var b bytes.Buffer
		b.WriteString("\x1b[2J\x1b[H")
		for _, row := range []int{5, 11, 17} {
			fmt.Fprintf(&b, "\x1b[%d;3H", row)
			b.WriteString("\x1b_Ga=T,t=f,f=24,s=400,v=300,c=20,r=5,C=1,q=2;")
			b.WriteString(encodedPath)
			b.WriteString("\x1b\\")
		}
		return b.Bytes()
	}

	// Simulate 10 scroll redraws back-to-back. Drain pendingOutput between
	// frames (like the render cycle does) so each frame starts clean.
	for i := 0; i < 10; i++ {
		_, _ = emu.Write(frame())
		_ = kp.FlushPending()
	}

	kp.mu.Lock()
	count := len(kp.placements[winID])
	kp.mu.Unlock()
	if count != 3 {
		t.Errorf("after 10 scroll redraws expected 3 placements tracked (one per visible thumbnail), got %d — placements are leaking", count)
	}
}
