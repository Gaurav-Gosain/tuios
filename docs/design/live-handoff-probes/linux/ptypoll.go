package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func probePTYPoll() {
	fmt.Println("== probe 4: readiness on a Linux PTY master ==")
	master, slave := openPTY()
	fds := []unix.PollFd{{Fd: int32(master), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 100)
	fmt.Printf("  poll(master) idle -> n=%d revents=%#x err=%v\n", n, fds[0].Revents, err)
	check(n == 0, "negative control: idle master is not readable")
	_, err = unix.Write(slave, []byte("hi"))
	must(err)
	fds[0].Revents = 0
	n, err = unix.Poll(fds, 1000)
	fmt.Printf("  poll(master) after slave write -> n=%d revents=%#x err=%v\n", n, fds[0].Revents, err)
	check(n == 1 && fds[0].Revents&unix.POLLIN != 0, "data -> POLLIN")
	buf := make([]byte, 64)
	r, _ := unix.Read(master, buf)
	fmt.Printf("  read %q\n", buf[:r])
	unix.Close(slave)
	fds[0].Revents = 0
	n, err = unix.Poll(fds, 1000)
	fmt.Printf("  poll(master) after last slave close -> n=%d revents=%#x (POLLIN=%#x POLLHUP=%#x)\n", n, fds[0].Revents, unix.POLLIN, unix.POLLHUP)
	check(fds[0].Revents&unix.POLLHUP != 0, "slave closed -> POLLHUP")
	_, err = unix.Read(master, buf)
	fmt.Printf("  read(master) -> err=%v\n", err)
	check(errors.Is(err, unix.EIO), "read after hangup gives EIO")
	unix.Close(master)

	fmt.Println("-- spec 2.4 mechanism 1: is the creack/pty v1.1.24 master (via xpty) in Go's poller on Linux? --")
	pm, ps, err := pty.Open()
	must(err)
	err = pm.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	rerr, hung := guardedRead(pm)
	fmt.Printf("  creack/pty v1.1.24 pty.Open() master: SetReadDeadline err=%v, Read err=%v hung>1s=%v\n", err, rerr, hung)
	check(hung, "creack/pty pty.Open() master ignores read deadlines: its ptsname/unlockpt ioctls call os.File.Fd(), which put the fd in blocking mode; SetReadDeadline still returns nil and Read blocks in the kernel (contradicts spec 2.4 mechanism 1)")
	ps.Write([]byte("x")) // unblock the hung reader
	time.Sleep(50 * time.Millisecond)
	pm.Close()
	ps.Close()

	fmt.Println("-- positive control: /dev/ptmx opened with os.OpenFile and ioctls via SyscallConn keeps deadlines --")
	f, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	must(err)
	sc, _ := f.SyscallConn()
	var ptn int
	sc.Control(func(fd uintptr) {
		must(unix.IoctlSetPointerInt(int(fd), unix.TIOCSPTLCK, 0))
		ptn, err = unix.IoctlGetInt(int(fd), unix.TIOCGPTN)
		must(err)
	})
	sf, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptn), os.O_RDWR|unix.O_NOCTTY, 0)
	must(err)
	err = f.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	rerr, hung = guardedRead(f)
	fmt.Printf("  SetReadDeadline err=%v, Read err=%v hung>1s=%v\n", err, rerr, hung)
	check(err == nil && !hung && errors.Is(rerr, os.ErrDeadlineExceeded), "a poller-registered master supports read deadlines on Linux")
	_ = f.Fd()
	err = f.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	rerr, hung = guardedRead(f)
	fmt.Printf("  after f.Fd(): SetReadDeadline err=%v, Read err=%v hung>1s=%v\n", err, rerr, hung)
	check(hung, "os.File.Fd() makes deadlines silently ineffective (SetReadDeadline err=%v, read blocks)", err)
	sf.Write([]byte("x"))
	time.Sleep(50 * time.Millisecond)
	f.Close()
	sf.Close()
	finish("ptypoll")
}

func guardedRead(f *os.File) (error, bool) {
	ch := make(chan error, 1)
	go func() {
		b := make([]byte, 16)
		_, err := f.Read(b)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err, false
	case <-time.After(time.Second):
		return nil, true
	}
}
