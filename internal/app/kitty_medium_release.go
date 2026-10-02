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
// repeat of the frame on screen, a pane that is hidden, a host that is behind).
// Each of those has to delete the object, or it stays in /dev/shm, which is
// memory, for as long as the machine is up. A video stream left this way
// leaked 3,863 frames, 5.3 GB, in one session.
//
// With graphics off, tuios deletes nothing: it never looks at the frame, so
// it has no grounds to believe the name.
//
// The name is text the pane printed, and any program in the pane can print the
// name of an object another program still uses. So an object is deleted only
// when this user owns it and its size is the one the command describes. See
// vt.KittyMediumIsFrame.
//
// A file named by t=f is the guest's own and is never deleted.

// releaseKittyMedium deletes the object a t=s or t=t transmission named, once
// tuios is its last reader. path is what kittyMediumPath resolved, so it has
// passed that function's checks: a shared memory object directly in /dev/shm,
// or a temporary file owned by this user whose name carries the spec's marker.
// The checks the spec asks for before a delete are repeated here, so a path
// that reaches this function by another route is still never removed outside
// a temporary directory.
func releaseKittyMedium(cmd *vt.KittyCommand, path string) {
	if path == "" {
		// A shared memory object on macOS has no path. Deleting it takes
		// shm_unlink, which Go reaches only through cgo.
		return
	}
	switch cmd.Medium {
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
	// Lstat, not Stat: a symlink is not the frame, and what it points at is
	// not this function's to delete.
	info, err := os.Lstat(path)
	if err != nil || !vt.KittyMediumIsFrame(cmd, info) {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		kittyPassthroughLog("releaseKittyMedium: remove %s: %v", path, err)
	}
}
