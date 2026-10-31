//go:build darwin

package kittyshm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"

	"tuios.dev/vt"
)

// The kernel's numbers for shm_open and shm_unlink. They are variables so a test
// can see what a missing call does.
var sysShmOpen, sysShmUnlink uintptr = syscall.SYS_SHM_OPEN, syscall.SYS_SHM_UNLINK

// Available reports whether the calls below work on this machine, and answers
// the same for the life of the process. Apple supports libSystem as the way into
// the kernel, and these are kernel numbers, so a later macOS may change them. See
// probe. It costs nothing until a program names shared memory.
var Available = sync.OnceValue(func() bool { return probe(fmt.Sprintf("/tuios-p-%d", os.Getpid())) })

// probe makes an object, reads it back through a mapping, and deletes it. A
// program told no falls back to a file or to sending the bytes.
//
// A system call number the kernel has no call for raises SIGSYS, and the Go
// runtime dies of that unless the signal goes to a channel. Then the call returns
// ENOSYS like any other failure.
func probe(name string) bool {
	sigsys := make(chan os.Signal, 1)
	signal.Notify(sigsys, syscall.SIGSYS)
	defer signal.Stop(sigsys)

	const marker = "tuios"
	fd, err := shmOpen(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() { _ = shmUnlink(name) }()
	defer func() { _ = syscall.Close(fd) }()
	if syscall.Ftruncate(fd, int64(os.Getpagesize())) != nil {
		return false
	}
	m, err := syscall.Mmap(fd, 0, os.Getpagesize(), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return false
	}
	copy(m, marker)
	_ = syscall.Munmap(m)

	o, err := Open(name, int64(os.Getpagesize()))
	if err != nil {
		return false
	}
	defer func() { _ = o.Close() }()
	head := make([]byte, len(marker))
	if _, err := io.ReadFull(o, head); err != nil || string(head) != marker {
		return false
	}
	if shmUnlink(name) != nil {
		return false
	}
	gone, err := shmOpen(name, os.O_RDONLY, 0)
	if err == nil {
		_ = syscall.Close(gone)
		return false
	}
	return errors.Is(err, syscall.ENOENT)
}

func shmOpen(name string, flags, mode int) (int, error) {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return -1, err
	}
	fd, _, errno := syscall.Syscall(sysShmOpen, uintptr(unsafe.Pointer(p)), uintptr(flags), uintptr(mode))
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

func shmUnlink(name string) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	if _, _, errno := syscall.Syscall(sysShmUnlink, uintptr(unsafe.Pointer(p)), 0, 0); errno != 0 {
		return errno
	}
	return nil
}

// Object is a shared memory object opened read-only.
type Object struct {
	file *os.File
	info os.FileInfo
	data []byte
	*bytes.Reader
}

// Stat describes the object.
func (o *Object) Stat() os.FileInfo { return o.info }

// open returns a descriptor for name. A shared memory object is no path, so no
// symlink or named pipe can stand in for it, and it needs no other flags.
func open(name string) (*os.File, error) {
	fd, err := shmOpen(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Stat describes the object name without mapping it.
func Stat(name string) (os.FileInfo, error) {
	f, err := open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.Stat()
}

// Open opens the object name for reading, up to limit bytes. Its size cannot
// change under the mapping: an object takes one ftruncate, the one that gave it
// its size, so a guest cannot shrink it and turn a read past the end into a
// SIGBUS.
func Open(name string, limit int64) (*Object, error) {
	f, err := open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.Size() > limit {
		_ = f.Close()
		return nil, ErrTooLarge
	}
	var data []byte
	if info.Size() > 0 {
		data, err = syscall.Mmap(int(f.Fd()), 0, int(info.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return &Object{file: f, info: info, data: data, Reader: bytes.NewReader(data)}, nil
}

// Close unmaps and closes the object. The object itself stays until it is
// deleted.
func (o *Object) Close() error {
	if o.data != nil {
		_ = syscall.Munmap(o.data)
		o.data = nil
		o.Reset(nil)
	}
	return o.file.Close()
}

// ReleaseFrame deletes the object cmd names when it is the frame cmd describes.
func ReleaseFrame(cmd *vt.KittyCommand) {
	info, err := Stat(cmd.FilePath)
	if err != nil {
		return
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	owned := ok && int(st.Uid) == os.Getuid()
	if !IsFrame(cmd, info.Size(), info.Mode().IsRegular(), owned, int64(os.Getpagesize())) {
		return
	}
	_ = shmUnlink(cmd.FilePath)
}
