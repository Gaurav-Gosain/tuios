package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/kittyshm"
	"tuios.dev/vt"
)

const kittyShmObjectPrefix = "shm-object:"

func kittyShmObject(path string) (string, bool) {
	return strings.CutPrefix(path, kittyShmObjectPrefix)
}

type kittyObject interface {
	io.ReadSeekCloser
	Stat() (os.FileInfo, error)
}

type shmKittyObject struct{ *kittyshm.Object }

func (o shmKittyObject) Stat() (os.FileInfo, error) { return o.Object.Stat(), nil }

func openKittyObject(path string, flags int) (kittyObject, error) {
	if name, ok := kittyShmObject(path); ok {
		o, err := kittyshm.Open(name, maxPassthroughTransmitBytes)
		if err != nil {
			return nil, err
		}
		return shmKittyObject{o}, nil
	}
	return os.OpenFile(path, os.O_RDONLY|flags, 0) // #nosec G304 - checked by kittyMediumPath
}

func kittyShmReadable() bool { return kittyShmHasPath() || kittyshm.Available() }

func (kp *KittyPassthrough) kittyMediumPath(cmd *vt.KittyCommand) (string, bool) {
	name := cmd.FilePath
	if name == "" || strings.ContainsRune(name, 0) {
		return "", false
	}
	var path string
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		base := strings.TrimPrefix(name, "/")
		if base == "" || base == "." || base == ".." || strings.ContainsRune(base, '/') {
			return "", false
		}
		if kittyshm.Available() {
			info, err := kittyshm.Stat(name)
			if err != nil || !info.Mode().IsRegular() {
				return "", false
			}
			return kittyShmObjectPrefix + name, true
		}
		if !kittyShmHasPath() {
			return "", kp.hostReadsFiles()
		}
		path = "/dev/shm/" + base
	case vt.KittyMediumFile, vt.KittyMediumTempFile:
		if !filepath.IsAbs(name) {
			return "", false
		}
		path = filepath.Clean(name)
	default:
		return "", false
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	if cmd.Medium == vt.KittyMediumSharedMemory {
		if filepath.Dir(resolved) != "/dev/shm" {
			return "", false
		}
	} else if vt.KittySensitivePath(resolved) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if cmd.Medium != vt.KittyMediumSharedMemory {
		if !ownedByMe(info) {
			return "", false
		}
		if cmd.Medium == vt.KittyMediumTempFile && !strings.Contains(filepath.Base(resolved), vt.KittyTempMarker) {
			return "", false
		}
	}

	if !kp.hostReadsFiles() && cmd.Medium != vt.KittyMediumSharedMemory && !kittyInTempDir(resolved) {
		return "", false
	}
	return resolved, true
}

func kittyShmHasPath() bool {
	info, err := os.Stat("/dev/shm")
	return err == nil && info.IsDir()
}

var kittyInTempDir = vt.KittyInTempDir

// DropRemoteFileMedium drops a file medium command from a pane whose process
// runs on another machine, with the later chunks of a chunked one.
func (kp *KittyPassthrough) DropRemoteFileMedium(windowID string, cmd *vt.KittyCommand) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	kittyPassthroughLog("DropRemoteFileMedium: action=%c medium=%c from a pane on another machine", cmd.Action, cmd.Medium)
	if cmd.More && (cmd.Action == vt.KittyActionTransmit || cmd.Action == vt.KittyActionTransmitPlace) {
		if kp.discardingChunks == nil {
			kp.discardingChunks = make(map[string]bool)
		}
		kp.discardingChunks[windowID] = true
	}
}
