package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// The kitty spec gives a shared memory object (t=s) and a temporary file (t=t)
// to the terminal: the terminal reads it and then deletes it. A guest that
// streams frames this way creates a fresh object per frame and never removes
// one itself, because it cannot know when the terminal has finished reading.
//
// When tuios forwards the name, the host terminal is the reader and deletes
// it. Every other outcome makes tuios the last reader: it read the bytes
// itself and sent them inline, or it decided not to send the frame at all (a
// repeat of the frame on screen, a pane that is hidden, a host that is behind,
// graphics turned off). Each of those has to delete the object, or it stays in
// /dev/shm, which is memory, for as long as the machine is up. A video stream
// left this way leaked 3,863 frames, 5.3 GB, in one session.
//
// A file named by t=f is the guest's own and is never deleted.

// releaseKittyMedium deletes the object a t=s or t=t transmission named, once
// tuios is its last reader. path is what kittyMediumPath resolved, so it has
// passed that function's checks: a shared memory object directly in /dev/shm,
// or a temporary file owned by this user whose name carries the spec's marker.
// The checks the spec asks for before a delete are repeated here, so a path
// that reaches this function by another route is still never removed outside
// a temporary directory.
func releaseKittyMedium(medium vt.KittyGraphicsMedium, path string) {
	if path == "" {
		// A shared memory object on macOS has no path. Deleting it takes
		// shm_unlink, which Go reaches only through cgo.
		return
	}
	switch medium {
	case vt.KittyMediumSharedMemory:
		if filepath.Dir(path) != "/dev/shm" {
			return
		}
	case vt.KittyMediumTempFile:
		if !strings.Contains(path, kittyTempMarker) || !kittyInTempDir(path) {
			return
		}
	default:
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		kittyPassthroughLog("releaseKittyMedium: remove %s: %v", path, err)
	}
}

// releaseDroppedKittyMedium deletes the object a file-medium transmission
// named when tuios takes the command off the stream without reading it and
// without forwarding it. Callers hold kp.mu.
func (kp *KittyPassthrough) releaseDroppedKittyMedium(cmd *vt.KittyCommand) {
	if cmd.Medium != vt.KittyMediumSharedMemory && cmd.Medium != vt.KittyMediumTempFile {
		return
	}
	if cmd.Action != vt.KittyActionTransmit && cmd.Action != vt.KittyActionTransmitPlace {
		return
	}
	if path, ok := kp.kittyMediumPath(cmd); ok {
		releaseKittyMedium(cmd.Medium, path)
	}
}
