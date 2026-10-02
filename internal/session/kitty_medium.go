package session

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// releaseUnreadKittyMedium deletes the shared memory object a kitty t=s
// transmission names, for a pane no client is attached to.
//
// The kitty spec makes the terminal delete the object after it reads it, and
// a guest streaming frames this way never deletes one itself. In a daemon pane
// the reader is the client: its passthrough forwards the name to the host,
// which deletes it, or reads and deletes it itself. With no client attached
// nobody reads the frame, so the daemon is the last reader. A video stream in
// a detached pane otherwise fills /dev/shm, which is memory, at the stream's
// own rate.
//
// Only a single name directly in /dev/shm that is a regular file is deleted.
// A temporary file (t=t) is left to the client: no stream seen so far uses
// one, and the checks the spec asks for before deleting one are the client's.
func releaseUnreadKittyMedium(cmd *vt.KittyCommand) {
	if cmd.Medium != vt.KittyMediumSharedMemory {
		return
	}
	if cmd.Action != vt.KittyActionTransmit && cmd.Action != vt.KittyActionTransmitPlace {
		return
	}
	name := strings.TrimPrefix(cmd.FilePath, "/")
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, '/') || strings.ContainsRune(name, 0) {
		return
	}
	path := filepath.Join("/dev/shm", name)
	// Lstat, not Stat: a symlink in /dev/shm is not a shared memory object,
	// and what it points at is not this function's to delete.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		debugLog("[DEBUG] releaseUnreadKittyMedium: remove %s: %v", path, err)
	}
}
