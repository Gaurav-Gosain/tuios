package session

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/kittyshm"
	"tuios.dev/vt"
)

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
	if kittyshm.Available() {
		kittyshm.ReleaseFrame(cmd)
		return
	}
	path := filepath.Join("/dev/shm", name)
	info, err := os.Lstat(path)
	if err != nil || !vt.KittyMediumIsFrame(cmd, info) {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		debugLog("[DEBUG] releaseUnreadKittyMedium: remove %s: %v", path, err)
	}
}
