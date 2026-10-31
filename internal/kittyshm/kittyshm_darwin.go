//go:build darwin

package kittyshm

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"tuios.dev/vt"
)

// ByName reports whether this machine reaches a shared memory object by name.
const ByName = true

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
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return nil, err
	}
	fd, _, errno := syscall.Syscall(syscall.SYS_SHM_OPEN, uintptr(unsafe.Pointer(p)), uintptr(os.O_RDONLY), 0)
	if errno != 0 {
		return nil, errno
	}
	return os.NewFile(fd, name), nil
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

// Open opens the object name for reading. Its size cannot change under the
// mapping: an object takes one ftruncate, the one that gave it its size, so a
// guest cannot shrink it and turn a read past the end into a SIGBUS.
func Open(name string) (*Object, error) {
	f, err := open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
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
	p, err := syscall.BytePtrFromString(cmd.FilePath)
	if err != nil {
		return
	}
	_, _, _ = syscall.Syscall(syscall.SYS_SHM_UNLINK, uintptr(unsafe.Pointer(p)), 0, 0)
}
