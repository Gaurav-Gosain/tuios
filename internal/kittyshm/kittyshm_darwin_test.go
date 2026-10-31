//go:build darwin

package kittyshm

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"tuios.dev/vt"
)

// makeObject makes a shared memory object of size bytes that starts with head.
func makeObject(t *testing.T, name string, size int, head string) {
	t.Helper()
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	fd, _, errno := syscall.Syscall(syscall.SYS_SHM_OPEN, uintptr(unsafe.Pointer(p)), uintptr(os.O_CREATE|os.O_EXCL|os.O_RDWR), 0o600)
	if errno != 0 {
		t.Fatalf("shm_open: %v", errno)
	}
	defer func() { _ = syscall.Close(int(fd)) }()
	if err := syscall.Ftruncate(int(fd), int64(size)); err != nil {
		t.Fatal(err)
	}
	m, err := syscall.Mmap(int(fd), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	copy(m, head)
	_ = syscall.Munmap(m)
	t.Cleanup(func() { _, _, _ = syscall.Syscall(syscall.SYS_SHM_UNLINK, uintptr(unsafe.Pointer(p)), 0, 0) })
}

// The real calls: the object is read through the mapping, and only the frame the
// command describes is deleted.
func TestObjectOnThisMachine(t *testing.T) {
	name := fmt.Sprintf("/tuios-t-%d", os.Getpid())
	makeObject(t, name, 50*50*4, "frame")

	o, err := Open(name, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 5)
	if _, err := o.Read(head); err != nil || string(head) != "frame" {
		t.Fatalf("read %q, %v", head, err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(name, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an object past the limit opened: %v", err)
	}

	cmd := func(w, h int) *vt.KittyCommand {
		return &vt.KittyCommand{FilePath: name, Format: vt.KittyFormatRGBA, Width: w, Height: h}
	}
	ReleaseFrame(cmd(400, 400))
	if _, err := Stat(name); err != nil {
		t.Fatalf("an object of another size was deleted: %v", err)
	}
	ReleaseFrame(cmd(50, 50))
	if _, err := Stat(name); err == nil {
		t.Fatal("the frame is still there")
	}
}

// A later macOS may drop or move these numbers. The answer is then no, and the
// process lives. A call the kernel does not have raises SIGSYS.
func TestProbeSaysNoWhenTheCallsAreGone(t *testing.T) {
	name := fmt.Sprintf("/tuios-t-p-%d", os.Getpid())
	if !probe(name) {
		t.Fatal("the probe says no on a machine where the calls work")
	}
	open, unlink := sysShmOpen, sysShmUnlink
	t.Cleanup(func() { sysShmOpen, sysShmUnlink = open, unlink })

	sysShmOpen = 9999
	if probe(name) {
		t.Fatal("the probe says yes with no shm_open")
	}
	sysShmOpen, sysShmUnlink = open, 9999
	if probe(name) {
		t.Fatal("the probe says yes with no shm_unlink")
	}
	// That run could not delete its object.
	sysShmUnlink = unlink
	_ = shmUnlink(name)
}
