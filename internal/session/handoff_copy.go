package session

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// The copy route: how a live upgrade runs the binary it checked where there
// is no /proc/self/fd to run an open file through (darwin, and every system
// but Linux). The binary is opened, the checks read the open file, and that
// open file is cloned (APFS) or copied into a folder of its own inside the
// handoff folder, which only this user can change. The preflight and the
// exec run the copy, so a rename over the binary's path after the open
// changes nothing that runs. A hard link is not used: it moves the change
// time of the binary the person confirmed, and it fails across file systems.
//
// Two cases keep the path, as before: a script, which its interpreter opens
// again by name, and a binary this user cannot read (mode 0111), which cannot
// be copied. On darwin the copy keeps the quarantine mark of the binary, so
// Gatekeeper checks a downloaded binary as it would by its path. A copy that
// cannot be made, for a full disk or a handoff folder others can write, keeps
// the path too, and the log says why.
//
// The image started from the copy runs from it for its whole life: on darwin
// os.Executable names the copy, and the panes and plugins it starts use that
// name. So the copy is removed by the image that adopts the panes from it.

// copyDirPrefix starts the name of every folder a copy is made in.
const copyDirPrefix = "exec-"

// copyName is the copy's file name, so ps shows the daemon as tuios.
const copyName = "tuios"

// handoffOldCopyEnv carries to the next image the folders of the copies the
// images before it ran from, separated by the path list separator. That image
// removes them once it has adopted the panes.
const handoffOldCopyEnv = "TUIOS_HANDOFF_OLD_COPY"

// binaryCopy is a private copy of a checked binary.
type binaryCopy struct {
	dir, path string
	id        fileID
	sum       []byte
}

// openForCopy opens the binary at abs to read. A binary this user cannot
// read returns no file, and runs by its path. O_NONBLOCK keeps a FIFO put at
// the path from blocking the open; the checks then refuse it.
func openForCopy(abs string) (*os.File, error) {
	f, err := os.OpenFile(abs, os.O_RDONLY|openNoBlock, 0)
	if errors.Is(err, fs.ErrPermission) {
		LogBasic("Live upgrade: %s cannot be read, only run, so it runs by its path", abs)
		return nil, nil
	}
	return f, err
}

// makeCopy copies the checked open file, whose stat is info, and points the
// preflight and the exec at the copy. A script, or a copy that cannot be
// made, runs by its path. A binary that changed while it was copied is
// refused.
func (b *handoffBinary) makeCopy(info os.FileInfo) error {
	if b.open == nil {
		return nil
	}
	byPath := func() {
		_ = b.open.Close()
		b.open = nil
	}
	var head [2]byte
	if n, _ := b.open.ReadAt(head[:], 0); n == 2 && string(head[:]) == "#!" {
		b.script = true
		byPath()
		return nil
	}
	c, err := newBinaryCopy(b.open, info.Size())
	if err != nil {
		LogBasic("Live upgrade: %s runs by its path, because no private copy of it could be made: %v", b.path, err)
		byPath()
		return nil
	}
	after, err := b.open.Stat()
	if err != nil || fileIDOf(after) != b.file {
		c.remove()
		return errfmt.Errorf("%s was replaced while it was being checked. Run the upgrade again when the install has finished", b.path)
	}
	b.copy = c
	return nil
}

// newBinaryCopy copies size bytes of src into a new folder in the handoff
// folder.
func newBinaryCopy(src *os.File, size int64) (*binaryCopy, error) {
	parent := handoffDir()
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	if err := checkPrivateDir(parent); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(parent, copyDirPrefix)
	if err != nil {
		return nil, err
	}
	c := &binaryCopy{dir: dir, path: filepath.Join(dir, copyName)}
	// A clone shares the checked file's blocks: under 1 ms and no disk
	// space, against about 40 ms for a written copy of a 43 MB binary
	// (measured on an M-series Mac). Where it cannot be made, across volumes
	// or off APFS, the bytes are copied.
	if err = cloneFrom(src, c.path); err == nil {
		c.sum, err = hashFile(c.path)
	} else {
		c.sum, err = copyFrom(io.NewSectionReader(src, 0, size), c.path)
	}
	if err != nil {
		c.remove()
		return nil, err
	}
	if err := c.markLike(src); err != nil {
		c.remove()
		return nil, err
	}
	if c.id, err = statFileID(c.path); err != nil {
		c.remove()
		return nil, err
	}
	return c, nil
}

// markLike gives the copy the quarantine mark src has.
func (c *binaryCopy) markLike(src *os.File) error {
	dst, err := os.OpenFile(c.path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	err = copyQuarantine(src, dst)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return err
}

// checkPrivateDir refuses a folder another user could rename things in.
func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errfmt.Errorf("%s is not a folder", dir)
	}
	if uid, ok := fileUID(info); ok && uid != os.Getuid() {
		return errfmt.Errorf("%s is owned by user %d", dir, uid)
	}
	if perm := info.Mode().Perm(); perm&0o022 != 0 {
		return errfmt.Errorf("%s can be written by its group or by others (mode %04o)", dir, perm)
	}
	return nil
}

// verify checks, after the preflight ran the copy, that it still holds the
// bytes read from the checked file. It runs before anything is paused.
func (c *binaryCopy) verify() error {
	if c == nil {
		return nil
	}
	changed := errfmt.Errorf("the private copy %s changed after it was made, so the upgrade is refused. Run the upgrade again", c.path)
	if !c.unchanged() {
		return changed
	}
	sum, err := hashFile(c.path)
	if err != nil || !bytes.Equal(sum, c.sum) {
		return changed
	}
	return nil
}

// hashFile is the SHA-256 of the file at path.
func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// unchanged reports whether the copy is still the file that was made. Its
// change time cannot be set back without root.
func (c *binaryCopy) unchanged() bool {
	if c == nil {
		return true
	}
	id, err := statFileID(c.path)
	return err == nil && id == c.id
}

// remove deletes the copy and its folder.
func (c *binaryCopy) remove() {
	if c != nil {
		_ = os.RemoveAll(c.dir)
	}
}

// runChecked points cmd at the file the checks read: the copy, or on Linux
// the open file.
func (b handoffBinary) runChecked(cmd *exec.Cmd) {
	if b.copy != nil {
		cmd.Path = b.copy.path
		return
	}
	b.runOpenFile(cmd)
}

// execTarget is what the exec runs: a script's path, the copy, or on Linux
// the open file.
func (b handoffBinary) execTarget() string {
	switch {
	case b.script:
		return b.path
	case b.copy != nil:
		return b.copy.path
	}
	return b.execPath()
}

// ownCopyDir is the folder of the copy this image runs from, or "" when it
// runs from anything else.
func ownCopyDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if dir := filepath.Dir(exe); isCopyDir(dir) {
		return dir
	}
	return ""
}

// isCopyDir reports whether dir is a folder a copy was made in.
func isCopyDir(dir string) bool {
	if !strings.HasPrefix(filepath.Base(dir), copyDirPrefix) {
		return false
	}
	parent, want := filepath.Dir(dir), handoffDir()
	if parent == want {
		return true
	}
	// os.Executable resolves links on Linux.
	resolved, err := filepath.EvalSymlinks(want)
	return err == nil && parent == resolved
}

// withOldCopies is env with this image's own copy folder added to the ones
// handoffOldCopyEnv lists, for the image an exec starts.
func withOldCopies(env []string) []string {
	own := ownCopyDir()
	if own == "" {
		return env
	}
	dirs := own
	if old := os.Getenv(handoffOldCopyEnv); old != "" {
		dirs = old + string(os.PathListSeparator) + own
	}
	return append(withoutEnv(env, handoffOldCopyEnv), handoffOldCopyEnv+"="+dirs)
}

// removeOldCopies removes the copies the images before this one ran from,
// once this image has the panes. It never removes its own.
func removeOldCopies() {
	old := os.Getenv(handoffOldCopyEnv)
	_ = os.Unsetenv(handoffOldCopyEnv)
	own := ownCopyDir()
	for dir := range strings.SplitSeq(old, string(os.PathListSeparator)) {
		if dir != "" && dir != own && isCopyDir(dir) {
			_ = os.RemoveAll(dir)
		}
	}
}
