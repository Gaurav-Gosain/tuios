package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/kittyshm"
	"tuios.dev/vt"
)

func releaseKittyMedium(cmd *vt.KittyCommand, path string) {
	if path == "" {
		return
	}
	if _, isObject := kittyShmObject(path); isObject {
		kittyshm.ReleaseFrame(cmd)
		return
	}
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		if filepath.Dir(path) != "/dev/shm" {
			return
		}
	case vt.KittyMediumTempFile:
		if !strings.Contains(path, vt.KittyTempMarker) || !kittyInTempDir(path) {
			return
		}
	default:
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !vt.KittyMediumIsFrame(cmd, info) {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		kittyPassthroughLog("releaseKittyMedium: remove %s: %v", path, err)
	}
}
