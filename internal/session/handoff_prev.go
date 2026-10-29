package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
)

// The binary a handoff falls back to.
//
// After the exec there is no old process to return to. If the new image
// cannot adopt the panes, it re-execs the binary this image runs, with the
// same manifest, and that binary adopts them as it wrote them. By the time
// someone upgrades, an installer has usually put the new binary where the old
// one was (install.sh renames over it, Homebrew repoints its link and may
// delete the old copy), and a file with no name left cannot be run by name.
// So the running binary is kept at daemon start. On Linux that is an open
// descriptor of /proc/self/exe, which the handoff carries across its exec and
// the fallback execs as /proc/self/fd/N, so nothing is linked or copied.
// Elsewhere it is a hard link to its own inode where the file system allows,
// and a copy where it does not.

// prevBinaryName is the file the running image is kept as, in handoffDir.
const prevBinaryName = "tuios.prev"

// prevBinary is the running image's binary, kept for a fallback.
type prevBinary struct {
	path string
	file fileID
	// open is the kept descriptor on Linux, where path is /proc/self/fd/N.
	// It names the running image's inode, which cannot change.
	open *os.File
	// sum is the copy's SHA-256, when the binary was copied rather than
	// linked: a copy has an inode of its own, so only its content says it
	// is still the binary.
	sum []byte
}

// keepPrevBinary keeps this image's binary for a later handoff to fall back
// to. Failure is logged: it only means a handoff from this image is refused.
func (d *Daemon) keepPrevBinary() {
	prev, err := keepRunningBinary(handoffDir())
	if err != nil {
		LogError("Live upgrade: the running binary could not be kept for a fallback, so this daemon cannot be upgraded live: %v", err)
		return
	}
	d.prevMu.Lock()
	d.prev = prev
	d.prevMu.Unlock()
}

// keepRunningBinary keeps the running executable open on Linux, and links or
// copies it into dir elsewhere.
func keepRunningBinary(dir string) (*prevBinary, error) {
	if runtime.GOOS == "linux" {
		f, err := os.Open("/proc/self/exe")
		if err != nil {
			return nil, err
		}
		return &prevBinary{path: "/proc/self/fd/" + strconv.Itoa(int(f.Fd())), open: f}, nil //nolint:gosec // a descriptor number
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, prevBinaryName)
	tmp := dst + ".new"
	_ = os.Remove(tmp)
	if err := os.Link(exe, tmp); err == nil {
		if err := os.Rename(tmp, dst); err != nil {
			return nil, err
		}
		id, err := statFileID(dst)
		if err != nil {
			return nil, err
		}
		return &prevBinary{path: dst, file: id}, nil
	}

	// Another file system: copy what is running.
	sum, err := copyExecutable(exe, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return nil, err
	}
	id, err := statFileID(dst)
	if err != nil {
		return nil, err
	}
	// The first run of a new file is slow on macOS. It is paid now, not in a
	// fallback.
	go func() {
		//tuios:allow-exec runs the kept copy once so a fallback does not pay its first exec
		_ = exec.Command(dst, "daemon", "handoff-accepts").Run()
	}()
	return &prevBinary{path: dst, file: id, sum: sum}, nil
}

// copyExecutable copies src to dst, owner only, and returns its SHA-256.
func copyExecutable(src, dst string) ([]byte, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) //nolint:gosec // an executable, owner only
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		_ = out.Close()
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// fd is the kept descriptor the exec hands on, -1 when the binary is kept
// by name.
func (p *prevBinary) fd() int {
	if p.open == nil {
		return -1
	}
	return int(p.open.Fd()) //nolint:gosec // a descriptor number
}

// restartAdvice is what to do when a live upgrade is not possible.
func restartAdvice() string {
	return "To run another tuios, use '" + tuiosCmd("restart-server") + "'. It stops every program in every pane, and lists them before it asks."
}

// checkPrevBinary says whether the kept binary is still the one kept.
func (d *Daemon) checkPrevBinary() (*prevBinary, error) {
	d.prevMu.Lock()
	prev := d.prev
	d.prevMu.Unlock()
	if prev == nil {
		return nil, errors.New("this daemon kept no copy of its binary to fall back to, so it cannot upgrade live. The daemon log says why. " + restartAdvice())
	}
	if prev.open != nil {
		return prev, nil
	}
	id, err := statFileID(prev.path)
	if err != nil || id != prev.file {
		return nil, fmt.Errorf("the copy of this daemon's binary kept for a fallback, %s, has changed since the daemon started, so a failed upgrade would have nothing to return to. The upgrade is refused. %s", prev.path, restartAdvice())
	}
	if prev.sum != nil {
		f, err := os.Open(prev.path)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil || !bytes.Equal(h.Sum(nil), prev.sum) {
			return nil, fmt.Errorf("the copy of this daemon's binary kept for a fallback, %s, has changed since the daemon started, so a failed upgrade would have nothing to return to. The upgrade is refused. %s", prev.path, restartAdvice())
		}
	}
	return prev, nil
}
