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

const copyDirPrefix = "exec-"

const copyName = "tuios"

const handoffOldCopyEnv = "TUIOS_HANDOFF_OLD_COPY"

type binaryCopy struct {
	dir, path string
	id        fileID
	sum       []byte
}

func openForCopy(abs string) (*os.File, error) {
	f, err := os.OpenFile(abs, os.O_RDONLY|openNoBlock, 0)
	if errors.Is(err, fs.ErrPermission) {
		LogBasic("Live upgrade: %s cannot be read, only run, so it runs by its path", abs)
		return nil, nil
	}
	return f, err
}

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

func (c *binaryCopy) unchanged() bool {
	if c == nil {
		return true
	}
	id, err := statFileID(c.path)
	return err == nil && id == c.id
}

func (c *binaryCopy) remove() {
	if c != nil {
		_ = os.RemoveAll(c.dir)
	}
}

func (b handoffBinary) runChecked(cmd *exec.Cmd) {
	if b.copy != nil {
		cmd.Path = b.copy.path
		return
	}
	b.runOpenFile(cmd)
}

func (b handoffBinary) execTarget() string {
	switch {
	case b.script:
		return b.path
	case b.copy != nil:
		return b.copy.path
	}
	return b.execPath()
}

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

func isCopyDir(dir string) bool {
	if !strings.HasPrefix(filepath.Base(dir), copyDirPrefix) {
		return false
	}
	parent, want := filepath.Dir(dir), handoffDir()
	if parent == want {
		return true
	}
	resolved, err := filepath.EvalSymlinks(want)
	return err == nil && parent == resolved
}

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
