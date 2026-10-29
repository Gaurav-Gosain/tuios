package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"tuios.dev/vt"
)

// The preflight: before anything is paused, the new binary is run once, on
// the very file the exec will use, and asked which manifests it reads. A
// binary that cannot run, or reads another version, is refused with nothing
// changed. The run also pays the cost of a first exec of a new file (0.3 to
// 0.5 s on macOS) outside the pause, and proves that the new binary starts at
// all: package init and flag parsing are the code that runs first after the
// exec. See docs/design/live-handoff.md, section 2.1.

// HandoffAccepts is what a tuios binary says about the handoffs it can take,
// as `tuios daemon handoff-accepts` prints it.
type HandoffAccepts struct {
	// Handoff is the manifest version range it reads, oldest first.
	Handoff [2]int `json:"handoff"`
	Backend string `json:"backend"`
	Version string `json:"version"`
	OS      string `json:"os"`
}

// LocalHandoffAccepts is what this binary takes.
func LocalHandoffAccepts(version string) HandoffAccepts {
	return HandoffAccepts{
		Handoff: [2]int{MinHandoffVersion, HandoffVersion},
		Backend: vt.Backend,
		Version: version,
		OS:      runtime.GOOS,
	}
}

// handoffPreflightTimeout bounds the preflight run. A first exec of a new
// file is slow on macOS, and a machine under load slower still.
const handoffPreflightTimeout = 30 * time.Second

// handoffBinary is a binary a preflight ran: its path and the file it was
// when it ran, which the exec must still find there.
type handoffBinary struct {
	path     string
	file     fileID
	accepts  HandoffAccepts
	duration time.Duration
}

// fileID names a file by device and inode.
type fileID struct{ dev, ino uint64 }

// preflightBinary runs path's handoff-accepts and checks it reads the
// manifest this build writes.
func preflightBinary(path string) (handoffBinary, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return handoffBinary{}, err
	}
	before, err := statFileID(abs)
	if err != nil {
		return handoffBinary{}, fmt.Errorf("%s: %w", abs, err)
	}
	if err := checkBinaryOwner(abs); err != nil {
		return handoffBinary{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), handoffPreflightTimeout)
	defer cancel()
	var out, errOut bytes.Buffer
	//tuios:allow-exec the preflight runs the new binary once, before anything is paused
	cmd := exec.CommandContext(ctx, abs, "daemon", "handoff-accepts")
	cmd.Stdout, cmd.Stderr = &out, &errOut
	started := time.Now()
	if err := cmd.Run(); err != nil {
		detail := clip(bytes.TrimSpace(errOut.Bytes()), 200)
		return handoffBinary{}, fmt.Errorf("%s does not run as a tuios that can take this daemon's state: %v %s", abs, err, detail)
	}
	took := time.Since(started)
	var acc HandoffAccepts
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &acc); err != nil {
		return handoffBinary{}, fmt.Errorf("%s ran, but did not say which live upgrades it accepts (it printed %q), so it is not a tuios this daemon can upgrade to. Check the path names a tuios binary", abs, clip(bytes.TrimSpace(out.Bytes()), 200))
	}
	if acc.OS != runtime.GOOS {
		return handoffBinary{}, fmt.Errorf("%s is built for %s, and this daemon runs on %s. Upgrade to a tuios built for %s", abs, acc.OS, runtime.GOOS, runtime.GOOS)
	}
	if HandoffVersion < acc.Handoff[0] || HandoffVersion > acc.Handoff[1] {
		return handoffBinary{}, fmt.Errorf("%s (%s) cannot read this daemon's state. This daemon writes handoff v%d, and that binary reads v%d to v%d. Upgrade live to a tuios that reads v%d. %s",
			abs, acc.Version, HandoffVersion, acc.Handoff[0], acc.Handoff[1], HandoffVersion, restartAdvice())
	}
	after, err := statFileID(abs)
	if err != nil || after != before {
		return handoffBinary{}, fmt.Errorf("%s was replaced while it was being checked", abs)
	}
	return handoffBinary{path: abs, file: after, accepts: acc, duration: took}, nil
}

// clip is b cut to at most n bytes.
func clip(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// checkBinaryOwner refuses a binary someone else could have put there: one
// owned by neither this user nor root, or one its group or others can write.
// The daemon runs it with every pane it holds.
func checkBinaryOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if uid, ok := fileUID(info); ok && uid != 0 && uid != os.Getuid() {
		return fmt.Errorf("%s is owned by user %d, not by you or root, so this daemon will not run it. Install it as yourself or as root", path, uid)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("%s can be written by its group or by others (mode %04o), so this daemon will not run it. Make it writable only by its owner: chmod go-w %s", path, perm, path)
	}
	return nil
}

// stillThere reports whether the binary's path still names the file the
// preflight ran.
func (b handoffBinary) stillThere() bool {
	id, err := statFileID(b.path)
	return err == nil && id == b.file
}

var errNotAnExecutable = errors.New("not a regular executable file")

// statFileID is the device and inode of the executable at path.
func statFileID(path string) (fileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileID{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fileID{}, errNotAnExecutable
	}
	return fileIDOf(info), nil
}
