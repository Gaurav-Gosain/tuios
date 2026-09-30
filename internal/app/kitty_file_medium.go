package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A kitty graphics command can name a file instead of carrying the image:
// t=f a file, t=t a temporary file, t=s a POSIX shared memory object. The name
// is text the pane printed, so any program's output can name any file, and a
// file cat'ed in a pane is enough. The kitty spec says what a terminal must
// check before it reads one: only regular files, symlinks followed, and it may
// refuse sensitive places such as /proc, /sys and /dev. The checks are made on
// the path before the file is opened, because opening a file can have side
// effects.
//
// Where the host terminal reads files on this machine, tuios forwards the name
// and the host applies its own checks as well. Where it cannot (a browser, or
// a terminal at the far end of ssh), tuios reads the file and sends the bytes.
// That turns a name into the file's contents on another machine, so the rules
// there are narrower: t=f and t=t must name a file in a temporary directory,
// where programs that do not ask first (youterm, mpv) write their frames. A
// file in the home directory, such as a key, is refused. A client that asks
// first (a=q) is told file media are not supported there and sends the bytes
// itself, so this only affects clients that did not ask.
//
// tuios and the programs in its panes run as the same user, so a file tuios
// can open is one the pane could read itself.
//
// A t=s name is a path only on Linux; see kittyMediumPath.

// sensitiveDirs are the places no file medium may name. /dev/shm is the one
// part of /dev that holds image data, and it is allowed.
var sensitiveDirs = []string{"/proc", "/sys", "/dev"}

// kittyMediumPath returns the path on this machine that cmd's file medium
// names, or false when it must not be read or forwarded.
func (kp *KittyPassthrough) kittyMediumPath(cmd *vt.KittyCommand) (string, bool) {
	name := cmd.FilePath
	if name == "" || strings.ContainsRune(name, 0) {
		return "", false
	}
	var path string
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		// A shared memory name may start with one slash, as shm_open takes
		// it. Past that it is a single name in /dev/shm: no directories and
		// no way out.
		base := strings.TrimPrefix(name, "/")
		if base == "" || base == "." || base == ".." || strings.ContainsRune(base, '/') {
			return "", false
		}
		// On macOS shared memory has no path: it is opened by name with
		// shm_open. A local host opens the name itself, so it is forwarded
		// and the empty path says there is nothing for tuios to read. A
		// remote host needs the bytes, which tuios cannot read, so the
		// frame is refused.
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

	// Symlinks are followed, as the spec requires, and the checks apply to
	// where they lead.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	if cmd.Medium == vt.KittyMediumSharedMemory {
		if filepath.Dir(resolved) != "/dev/shm" {
			return "", false
		}
	} else if kittySensitivePath(resolved) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if cmd.Medium != vt.KittyMediumSharedMemory {
		// A file another user owns is not the pane's to show, even where the
		// permissions let tuios read it.
		if !ownedByMe(info) {
			return "", false
		}
		// The kitty spec asks a t=t path to carry this text. Its clients
		// always name their temporary files so.
		if cmd.Medium == vt.KittyMediumTempFile && !strings.Contains(filepath.Base(resolved), kittyTempMarker) {
			return "", false
		}
	}

	if !kp.hostReadsFiles() && cmd.Medium != vt.KittyMediumSharedMemory && !kittyInTempDir(resolved) {
		return "", false
	}
	return resolved, true
}

// kittyShmHasPath reports whether shared memory objects are files under
// /dev/shm, as on Linux.
func kittyShmHasPath() bool {
	info, err := os.Stat("/dev/shm")
	return err == nil && info.IsDir()
}

// kittySensitivePath reports whether path lies in a place no file medium may
// name.
func kittySensitivePath(path string) bool {
	if path == "/dev/shm" || strings.HasPrefix(path, "/dev/shm/") {
		return false
	}
	for _, dir := range sensitiveDirs {
		if path == dir || strings.HasPrefix(path, dir+"/") {
			return true
		}
	}
	return false
}

// kittyTempMarker is the text the kitty spec asks for in a t=t file name.
const kittyTempMarker = "tty-graphics-protocol"

// kittyFixedTempDirs are the temporary directories on every unix. A variable
// so a test, whose own home is under /tmp, can take them away.
var kittyFixedTempDirs = []string{"/tmp", "/dev/shm", "/var/tmp"}

// kittyTempDirs lists the temporary directories: the fixed ones and TMPDIR.
// TMPDIR is ignored when it is /, the home directory or a parent of it, since
// that would make every file of the user a temporary one. Inside the home
// directory it counts only under ~/tmp or ~/.tmp.
func kittyTempDirs() []string {
	dirs := append([]string(nil), kittyFixedTempDirs...)
	tmp := os.Getenv("TMPDIR")
	if tmp == "" || !filepath.IsAbs(tmp) {
		return dirs
	}
	tmp = filepath.Clean(tmp)
	if tmp == "/" {
		return dirs
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		home = filepath.Clean(home)
		if r, err := filepath.EvalSymlinks(home); err == nil {
			home = r
		}
		t := tmp
		if r, err := filepath.EvalSymlinks(t); err == nil {
			t = r
		}
		if t == home || strings.HasPrefix(home, strings.TrimSuffix(t, "/")+"/") {
			return dirs
		}
		// Inside the home directory, only ~/tmp and ~/.tmp count. Anywhere
		// else there (~/.ssh, ~/.config) holds the user's own files.
		if rel, err := filepath.Rel(home, t); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			first, _, _ := strings.Cut(rel, "/")
			if first != "tmp" && first != ".tmp" {
				return dirs
			}
		}
	}
	return append(dirs, tmp)
}

// kittyInTempDir reports whether path lies in a temporary directory: /tmp,
// /var/tmp, /dev/shm, or the one TMPDIR names.
func kittyInTempDir(path string) bool {
	for _, dir := range kittyTempDirs() {
		if dir == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
		if strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/") {
			return true
		}
	}
	return false
}
