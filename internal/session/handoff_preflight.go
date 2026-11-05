package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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
	path string
	file fileID
	// open is the file the checks read. On Linux the preflight and the exec
	// run it by descriptor. Elsewhere its bytes are copied to copy, which
	// they run. Either way no rename over the path after the checks can
	// change what runs.
	open *os.File
	// copy is the private copy the copy route runs, nil on the others.
	copy *binaryCopy
	// script is a binary that starts with #!, which runs by its path.
	script   bool
	accepts  HandoffAccepts
	duration time.Duration
}

// fileID names a file by device and inode, and pins its content by size,
// modification and change time. A rename over the path changes the inode; a
// write in place keeps it, but changes the size or a time. The change time
// cannot be set back without root, where it is read.
type fileID struct {
	dev, ino     uint64
	size         int64
	mtime, ctime int64
}

// withoutChange is f without its change time.
func (f fileID) withoutChange() fileID {
	f.ctime = 0
	return f
}

// preflightOpts are the test-only parts of a preflight.
type preflightOpts struct {
	// afterCheck, when not nil, runs between the checks and the run.
	afterCheck func()
	// copy takes the copy route on Linux too, so its tests run there.
	copy bool
}

// preflightBinary runs path's handoff-accepts and checks it reads the
// manifest this build writes.
func preflightBinary(path string, opts preflightOpts) (handoffBinary, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return handoffBinary{}, err
	}
	bin, err := preflightOpen(abs, opts.copy || !execByDescriptor)
	if err != nil {
		return handoffBinary{}, err
	}
	ok := false
	defer func() {
		if !ok {
			bin.close()
		}
	}()
	before := bin.file
	if opts.afterCheck != nil {
		opts.afterCheck()
	}
	ctx, cancel := context.WithTimeout(context.Background(), handoffPreflightTimeout)
	defer cancel()
	var out, errOut bytes.Buffer
	//tuios:allow-exec the preflight runs the new binary once, before anything is paused
	cmd := exec.CommandContext(ctx, abs, "daemon", "handoff-accepts")
	if !bin.script {
		bin.runChecked(cmd)
	}
	cmd.Stdout, cmd.Stderr = &out, &errOut
	started := time.Now()
	if err := cmd.Run(); err != nil {
		why := err.Error()
		if detail := clip(bytes.TrimSpace(errOut.Bytes()), 200); len(detail) > 0 {
			why += " " + string(detail)
		}
		return handoffBinary{}, errfmt.Errorf("%s does not run as a tuios that can take this daemon's state: %s. Check that the path names a tuios binary that runs on this machine", abs, why)
	}
	took := time.Since(started)
	var acc HandoffAccepts
	if err := jsonx.Unmarshal(bytes.TrimSpace(out.Bytes()), &acc); err != nil {
		return handoffBinary{}, errfmt.Errorf("%s ran, but did not say which live upgrades it accepts (it printed %q), so it is not a tuios this daemon can upgrade to. Check the path names a tuios binary", abs, clip(bytes.TrimSpace(out.Bytes()), 200))
	}
	if acc.OS != runtime.GOOS {
		return handoffBinary{}, errfmt.Errorf("%s is built for %s, and this daemon runs on %s. Upgrade to a tuios built for %s", abs, acc.OS, runtime.GOOS, runtime.GOOS)
	}
	if HandoffVersion < acc.Handoff[0] || HandoffVersion > acc.Handoff[1] {
		return handoffBinary{}, errfmt.Errorf("%s (%s) cannot read this daemon's state. This daemon writes handoff v%d, and that binary reads v%d to v%d. Upgrade live to a tuios that reads v%d. %s",
			abs, acc.Version, HandoffVersion, acc.Handoff[0], acc.Handoff[1], HandoffVersion, restartAdvice())
	}
	after, err := bin.current()
	if err != nil || after != before {
		return handoffBinary{}, errfmt.Errorf("%s was replaced while it was being checked. Run the upgrade again when the install has finished", abs)
	}
	if err := bin.copy.verify(); err != nil {
		return handoffBinary{}, err
	}
	bin.file, bin.accepts, bin.duration = after, acc, took
	ok = true
	return bin, nil
}

// preflightOpen checks the binary at abs. It opens it first and checks the
// open file. On Linux the preflight and the exec then run that file; on the
// copy route they run a private copy of it.
func preflightOpen(abs string, copyRoute bool) (handoffBinary, error) {
	open := openBinary
	if copyRoute {
		open = openForCopy
	}
	f, err := open(abs)
	if err != nil {
		return handoffBinary{}, errfmt.Errorf("%s: %w", abs, err)
	}
	bin := handoffBinary{path: abs, open: f}
	var info os.FileInfo
	if f != nil {
		info, err = f.Stat()
	} else {
		info, err = os.Stat(abs)
	}
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0) {
		err = errNotAnExecutable
	}
	if err != nil {
		bin.close()
		return handoffBinary{}, errfmt.Errorf("%s: %w", abs, err)
	}
	if err := checkBinaryInfo(abs, info); err != nil {
		bin.close()
		return handoffBinary{}, err
	}
	bin.file = fileIDOf(info)
	if copyRoute {
		if err := bin.makeCopy(info); err != nil {
			bin.close()
			return handoffBinary{}, err
		}
		return bin, nil
	}
	bin.script = bin.isScript()
	return bin, nil
}

// current is the identity of the file the preflight ran: the open file for
// a binary run through it, the file at the path otherwise.
func (b handoffBinary) current() (fileID, error) {
	if b.open == nil || b.script {
		return statFileID(b.path)
	}
	info, err := b.open.Stat()
	if err != nil {
		return fileID{}, err
	}
	return fileIDOf(info), nil
}

// close lets go of the open file, and removes the copy.
func (b handoffBinary) close() {
	if b.open != nil {
		_ = b.open.Close()
	}
	b.copy.remove()
}

// clip is b cut to at most n bytes.
func clip(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// CheckUpgradeBinary is the daemon's owner and mode check on the binary at
// path, for the command that runs it before it asks the daemon. A path it
// cannot stat passes, and is left to the run to report.
func CheckUpgradeBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	return checkBinaryInfo(path, info)
}

// UpgradeBinary is a binary that passed the owner and mode check, pinned by
// its file. The restart of daemon upgrade --or-restart runs it by path three
// times, and Unchanged before each run holds the path to the file it checked,
// as a confirmed upgrade's pin does for the live path. Where fileIDOf reads
// nothing (Windows) only the check holds.
type UpgradeBinary struct {
	path string
	file fileID
}

// PinUpgradeBinary makes the owner and mode check on the executable at path
// and pins its file.
func PinUpgradeBinary(path string) (UpgradeBinary, error) {
	info, err := os.Stat(path)
	if err != nil {
		return UpgradeBinary{}, err
	}
	if err := checkBinaryInfo(path, info); err != nil {
		return UpgradeBinary{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return UpgradeBinary{}, errfmt.Errorf("%s is %w", path, errNotAnExecutable)
	}
	return UpgradeBinary{path: path, file: fileIDOf(info)}, nil
}

// Unchanged fails when the path no longer names the pinned file, or the file
// no longer passes the check.
func (b UpgradeBinary) Unchanged() error {
	info, err := os.Stat(b.path)
	if err != nil {
		return err
	}
	if fileIDOf(info) != b.file {
		return errfmt.Errorf("%s was replaced or changed after it was checked", b.path)
	}
	return checkBinaryInfo(b.path, info)
}

// checkBinaryInfo refuses a binary someone else could have put there: one
// owned by neither this user nor root, or one its group or others can write.
// The daemon runs it with every pane it holds.
func checkBinaryInfo(path string, info os.FileInfo) error {
	if uid, ok := fileUID(info); ok && uid != 0 && uid != os.Getuid() {
		return errfmt.Errorf("%s is owned by user %d, not by you or root, so this daemon will not run it. Install it as yourself or as root", path, uid)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return errfmt.Errorf("%s can be written by its group or by others (mode %04o), so this daemon will not run it. Make it writable only by its owner: chmod go-w %s", path, perm, path)
	}
	return nil
}

// stillThere reports whether the binary's path still names the file the
// preflight ran, and the copy, when there is one, is still the copy it ran.
func (b handoffBinary) stillThere() bool {
	id, err := statFileID(b.path)
	return err == nil && id == b.file && b.copy.unchanged()
}

var errNotAnExecutable = errors.New("not a regular executable file")

// errBinaryReplaced refuses a confirmed binary whose file changed after the
// person said yes to it.
func errBinaryReplaced(path string) error {
	return errfmt.Errorf("%s was changed after the upgrade to it was confirmed. Ask again to upgrade to the file there now", path)
}

// statFileID is the identity of the executable at path.
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
