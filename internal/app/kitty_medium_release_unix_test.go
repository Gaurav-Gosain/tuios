//go:build unix

package app

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

// TestKittyMediumReleaseChecksTheFrame holds the passthrough to deleting only
// the frame a command describes. The name is text a pane printed, so it can
// name an object another program still uses. The ways it could fail:
//   - an object of another size is deleted after tuios reads it inline;
//   - an object is deleted while graphics are off, when tuios never looked at
//     the frame;
//   - the right frame is kept after tuios reads it, which is the leak the
//     delete is for. This is the positive half of both cases above.
func TestKittyMediumReleaseChecksTheFrame(t *testing.T) {
	const w, h = 8, 4
	const winID = "window-0000-0000-0000-000000000000"
	object := func(t *testing.T, size int) (string, string) {
		t.Helper()
		name := fmt.Sprintf("tuios-release-%d-%s", os.Getpid(), t.Name()[len("TestKittyMediumReleaseChecksTheFrame/"):])
		path := "/dev/shm/" + name
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Skipf("cannot write /dev/shm object: %v", err)
		}
		t.Cleanup(func() { _ = os.Remove(path) })
		return name, path
	}
	exists := func(path string) bool {
		_, err := os.Lstat(path)
		return err == nil
	}
	remote := func() *KittyPassthrough {
		return NewKittyPassthroughWithOptions(KittyPassthroughOptions{
			Output:       &bytes.Buffer{},
			RemoteClient: true,
			Caps:         &HostCapabilities{KittyGraphics: true, TerminalName: "kitty", CellWidth: 10, CellHeight: 20},
		})
	}

	t.Run("right-size", func(t *testing.T) {
		name, path := object(t, w*h*4)
		cmd, raw := synthShmTransmitPlace(name, w, h)
		remote().ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
		if exists(path) {
			t.Fatal("tuios read the frame inline and left it in /dev/shm")
		}
	})

	t.Run("wrong-size", func(t *testing.T) {
		name, path := object(t, w*h*4+1)
		cmd, raw := synthShmTransmitPlace(name, w, h)
		remote().ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
		if !exists(path) {
			t.Fatal("tuios deleted an object whose size is not the frame's")
		}
	})

	t.Run("graphics-off", func(t *testing.T) {
		name, path := object(t, w*h*4)
		cmd, raw := synthShmTransmitPlace(name, w, h)
		kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
			Output: &bytes.Buffer{},
			Caps:   &HostCapabilities{TerminalName: "xterm"},
		})
		if kp.IsEnabled() {
			t.Fatal("graphics are on; this case needs them off")
		}
		kp.ForwardCommand(cmd, raw, winID, 0, 0, 181, 40, 1, 1, 0, 0, 0, false, func([]byte) {})
		if !exists(path) {
			t.Fatal("tuios deleted an object while graphics are off")
		}
	})
}
