package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const prevBinaryName = "tuios.prev"

type prevBinary struct {
	path string
	file fileID
	open *os.File
	sum  []byte
}

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

func keepRunningBinary(dir string) (*prevBinary, error) {
	if runtime.GOOS == "linux" {
		f, err := os.Open("/proc/self/exe")
		if err != nil {
			return nil, err
		}
		return &prevBinary{path: "/proc/self/fd/" + strconv.Itoa(fdNum(f)), open: f}, nil
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
	go func() {
		//tuios:allow-exec runs the kept copy once so a fallback does not pay its first exec
		_ = exec.Command(dst, "daemon", "handoff-accepts").Run()
	}()
	return &prevBinary{path: dst, file: id, sum: sum}, nil
}

func copyExecutable(src, dst string) ([]byte, error) {
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	return copyFrom(in, dst)
}

func copyFrom(src io.Reader, dst string) ([]byte, error) {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700) //nolint:gosec // an executable, owner only
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), src); err != nil {
		_ = out.Close()
		return nil, err
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func (p *prevBinary) fd() int {
	if p.open == nil {
		return -1
	}
	return fdNum(p.open)
}

func restartAdvice() string {
	return "To run another tuios, use '" + tuiosCmd("restart-server") + "'. It stops every program in every pane, and lists them before it asks."
}

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
	if err != nil || id.withoutChange() != prev.file.withoutChange() {
		return nil, errfmt.Errorf("the copy of this daemon's binary kept for a fallback, %s, has changed since the daemon started, so a failed upgrade would have nothing to return to. The upgrade is refused. %s", prev.path, restartAdvice())
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
			return nil, errfmt.Errorf("the copy of this daemon's binary kept for a fallback, %s, has changed since the daemon started, so a failed upgrade would have nothing to return to. The upgrade is refused. %s", prev.path, restartAdvice())
		}
	}
	return prev, nil
}
