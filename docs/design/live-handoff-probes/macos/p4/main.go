// Probe 4: exec in place (spec Appendix A) and the section 2.2 reap hazard.
//
//	cd .. && GOWORK=off GOFLAGS=-mod=mod GOPROXY=off go build -o p4bin ./p4 && ./p4bin
package main

/*
#include <sys/wait.h>
#include <errno.h>
// Blocking, non-reaping peek. Returns 0 and fills code/status, or -errno.
static int peek_exit(int pid, int *code, int *status) {
	siginfo_t si;
	for (;;) {
		int r = waitid(P_PID, (id_t)pid, &si, WEXITED | WNOWAIT);
		if (r == 0) { *code = si.si_code; *status = si.si_status; return 0; }
		if (errno != EINTR) return -errno;
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 2 {
		driver()
		return
	}
	switch os.Args[1] {
	case "a1":
		stageA1(os.Args[2] == "clear")
	case "a2":
		stageA2()
	case "b1":
		stageB1(os.Args[2])
	case "b2":
		stageB2()
	case "client":
		c, err := net.Dial("unix", os.Args[2])
		if err != nil {
			fmt.Println("client dial:", err)
			os.Exit(1)
		}
		c.Write([]byte("hello-before-exec"))
		time.Sleep(3 * time.Second)
	case "trylock":
		f, _ := os.OpenFile(os.Args[2], os.O_RDWR, 0)
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			fmt.Println("HELD_BY_OTHER", err)
		} else {
			fmt.Println("ACQUIRED")
		}
	}
}

func driver() {
	self, _ := os.Executable()
	run := func(title string, args ...string) {
		fmt.Printf("\n### %s\n", title)
		out, err := exec.Command(self, args...).CombinedOutput()
		fmt.Print(string(out))
		if err != nil {
			fmt.Println("  (exit:", err, ")")
		}
	}
	run("4A Appendix A: exec in place, CLOEXEC cleared on listener + lock fds", "a1", "clear")
	run("4A NEG CONTROL: same, CLOEXEC NOT cleared", "a1", "noclear")
	run("4B hazard: goroutine blocked in cmd.Wait() reaps a child that exits just before exec (+20 live kids)", "b1", "plain", "20")
	run("4B fix (cgo): waitid(WEXITED|WNOWAIT) peek, then wait4 behind a reapGate closed before exec (+20 live kids)", "b1", "gated-cgo", "20")
	run("4B fix (pure Go): raw SYS_WAITID WNOWAIT peek + reapGate (+20 live kids)", "b1", "gated-syscall", "20")
	run("4B fix (pure Go): kqueue EVFILT_PROC NOTE_EXIT peek on own child + reapGate (+20 live kids)", "b1", "gated-kqueue", "20")
	run("4B NEG CONTROL: kqueue peek then reap with the gate removed", "b1", "nogate", "0")
}

func fdOf(sc syscall.Conn) int {
	raw, _ := sc.SyscallConn()
	var fd int
	raw.Control(func(u uintptr) { fd = int(u) })
	return fd
}

func clearCloexec(fd int) {
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
		panic(err)
	}
}

func execSelf(args ...string) {
	self, _ := os.Executable()
	argv := append([]string{self}, args...)
	syscall.ForkLock.Lock() // no fork may observe the cleared-CLOEXEC fds
	err := syscall.Exec(self, argv, os.Environ())
	syscall.ForkLock.Unlock()
	panic(err)
}

// ---- 4A ----

func stageA1(clear bool) {
	dir, _ := os.MkdirTemp("", "p4a")
	sock := filepath.Join(dir, "s.sock")
	lockp := filepath.Join(dir, "lock")
	z := exec.Command("/bin/sh", "-c", "exit 7")
	l := exec.Command("/bin/sh", "-c", "sleep 1; exit 9")
	z.Start()
	l.Start()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		panic(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	lf, _ := os.OpenFile(lockp, os.O_CREATE|os.O_RDWR, 0o600)
	lockFd := fdOf(lf)
	if err := unix.Flock(lockFd, unix.LOCK_EX); err != nil {
		panic(err)
	}
	self, _ := os.Executable()
	cl := exec.Command(self, "client", sock)
	cl.Start()
	time.Sleep(300 * time.Millisecond) // client queued in backlog; z is a zombie
	lnFd := fdOf(ln.(*net.UnixListener))
	fmt.Printf("  pre-exec  pid=%d listener fd=%d lock fd=%d zombie(exit 7) pid=%d live(exit 9) pid=%d kill(z,0)=%v\n",
		os.Getpid(), lnFd, lockFd, z.Process.Pid, l.Process.Pid, syscall.Kill(z.Process.Pid, 0))
	if clear {
		clearCloexec(lnFd)
		clearCloexec(lockFd)
	}
	execSelf("a2", strconv.Itoa(os.Getpid()), strconv.Itoa(lnFd), strconv.Itoa(lockFd),
		strconv.Itoa(z.Process.Pid), strconv.Itoa(l.Process.Pid), lockp, dir)
}

func stageA2() {
	a := os.Args[2:]
	origPid, _ := strconv.Atoi(a[0])
	lnFd, _ := strconv.Atoi(a[1])
	zpid, _ := strconv.Atoi(a[3])
	lpid, _ := strconv.Atoi(a[4])
	lockp, dir := a[5], a[6]
	defer os.RemoveAll(dir)
	ok := func(b bool) string {
		if b {
			return "PASS"
		}
		return "FAIL"
	}
	fmt.Printf("  post-exec pid=%d same pid: %s\n", os.Getpid(), ok(os.Getpid() == origPid))

	self, _ := os.Executable()
	out, _ := exec.Command(self, "trylock", lockp).Output()
	fmt.Printf("  other process tries LOCK_EX|LOCK_NB: %s  lock held across exec: %s\n", strings.TrimSpace(string(out)), ok(strings.HasPrefix(string(out), "HELD")))

	var got string
	f := os.NewFile(uintptr(lnFd), "listener")
	ln, err := net.FileListener(f)
	if err == nil {
		ln.(*net.UnixListener).SetDeadline(time.Now().Add(2 * time.Second))
		c, aerr := ln.Accept()
		if aerr == nil {
			buf := make([]byte, 64)
			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _ := c.Read(buf)
			got = string(buf[:n])
		} else {
			err = aerr
		}
	}
	fmt.Printf("  net.FileListener(inherited fd) accept+read: %q err=%v  queued connection survived: %s\n", got, err, ok(got == "hello-before-exec"))

	for _, w := range []struct {
		pid  int
		want int
	}{{zpid, 7}, {lpid, 9}} {
		p, _ := os.FindProcess(w.pid)
		st, err := p.Wait()
		code := -1
		if st != nil {
			code = st.ExitCode()
		}
		fmt.Printf("  os.FindProcess(%d).Wait() -> exit=%d err=%v (want %d)  %s\n", w.pid, code, err, w.want, ok(code == w.want))
	}
	p, _ := os.FindProcess(1)
	_, err = p.Wait()
	fmt.Printf("  NEG CONTROL Wait on non-child pid 1 -> err=%v  detects not-a-child: %s\n", err, ok(errors.Is(err, syscall.ECHILD)))
}

// ---- 4B ----

// peek blocks until pid has exited without reaping it. kind: cgo | syscall | kqueue.
func peek(kind string, pid int) string {
	switch kind {
	case "cgo":
		var code, status C.int
		r := C.peek_exit(C.int(pid), &code, &status)
		return fmt.Sprintf("cgo waitid r=%d si_code=%d si_status=%d", r, code, status)
	case "syscall": // pure Go, no cgo: raw SYS_WAITID (x/sys has no darwin Waitid)
		var si [128]byte // darwin siginfo_t is 104 bytes
		for {
			_, _, e := syscall.Syscall6(syscall.SYS_WAITID, 1 /*P_PID*/, uintptr(pid), uintptr(unsafe.Pointer(&si[0])), uintptr(unix.WEXITED|unix.WNOWAIT), 0, 0)
			if e == syscall.EINTR {
				continue
			}
			code := *(*int32)(unsafe.Pointer(&si[8]))
			status := *(*int32)(unsafe.Pointer(&si[20]))
			return fmt.Sprintf("raw SYS_WAITID errno=%v si_code=%d si_status=%d", e, code, status)
		}
	case "kqueue": // pure Go: EVFILT_PROC on our own child does not reap
		kq, _ := unix.Kqueue()
		defer unix.Close(kq)
		var ch [1]unix.Kevent_t
		unix.SetKevent(&ch[0], pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
		ch[0].Fflags = unix.NOTE_EXIT | unix.NOTE_EXITSTATUS
		if _, err := unix.Kevent(kq, ch[:], nil, nil); err != nil {
			return fmt.Sprintf("kqueue register err=%v (treat ESRCH as already exited)", err)
		}
		ev := make([]unix.Kevent_t, 1)
		for {
			n, err := unix.Kevent(kq, nil, ev, nil)
			if err == unix.EINTR {
				continue
			}
			return fmt.Sprintf("kqueue n=%d err=%v data=0x%x exit=%d", n, err, ev[0].Data, syscall.WaitStatus(ev[0].Data).ExitStatus())
		}
	}
	panic(kind)
}

// stageB1 mode: plain | nogate | gated-cgo | gated-syscall | gated-kqueue ; nLive children still
// running (peek still blocked) at exec time.
func stageB1(mode string) {
	nLive := 0
	if len(os.Args) > 3 {
		nLive, _ = strconv.Atoi(os.Args[3])
	}
	type child struct {
		cmd  *exec.Cmd
		want int
	}
	kids := []child{{exec.Command("/bin/sh", "-c", "sleep 0.3; exit 5"), 5}}
	for i := 0; i < nLive; i++ {
		kids = append(kids, child{exec.Command("/bin/sh", "-c", fmt.Sprintf("sleep 1.2; exit %d", 10+i)), 10 + i})
	}
	var reapGate sync.Mutex
	gated := strings.HasPrefix(mode, "gated")
	if gated {
		reapGate.Lock() // the quiesce closes the gate before writing records
	}
	msgs := make(chan string, 4*len(kids))
	for _, k := range kids {
		k.cmd.Start()
		k := k
		go func() {
			if mode == "plain" { // today's monitorExit
				err := k.cmd.Wait()
				msgs <- fmt.Sprintf("old image cmd.Wait reaped pid %d: exit=%d err=%v", k.cmd.Process.Pid, k.cmd.ProcessState.ExitCode(), err)
				return
			}
			kind := strings.TrimPrefix(mode, "gated-")
			if mode == "nogate" {
				kind = "kqueue"
			}
			msgs <- fmt.Sprintf("old image peek pid %d: %s", k.cmd.Process.Pid, peek(kind, k.cmd.Process.Pid))
			reapGate.Lock()
			var ws syscall.WaitStatus
			syscall.Wait4(k.cmd.Process.Pid, &ws, 0, nil)
			msgs <- fmt.Sprintf("old image reaped pid %d through the gate: exit=%d", k.cmd.Process.Pid, ws.ExitStatus())
			reapGate.Unlock()
		}()
	}
	time.Sleep(800 * time.Millisecond) // kid 0 exits inside the "handoff window"; others still run
	for drained := false; !drained; {
		select {
		case m := <-msgs:
			fmt.Println("  " + m)
		default:
			drained = true
		}
	}
	var spec []string
	for i, k := range kids {
		want := strconv.Itoa(k.want)
		if i == 0 && !gated {
			want = "lost"
		}
		spec = append(spec, fmt.Sprintf("%d:%s", k.cmd.Process.Pid, want))
	}
	fmt.Printf("  pre-exec: kid0 pid %d kill(,0)=%v (nil => zombie kept); %d live kids with a peek still blocked\n", kids[0].cmd.Process.Pid, syscall.Kill(kids[0].cmd.Process.Pid, 0), nLive)
	execSelf(append([]string{"b2"}, spec...)...)
}

func stageB2() {
	allOK := true
	for _, sp := range os.Args[2:] {
		parts := strings.SplitN(sp, ":", 2)
		pid, _ := strconv.Atoi(parts[0])
		p, _ := os.FindProcess(pid)
		st, err := p.Wait()
		got := "lost"
		if st != nil {
			got = strconv.Itoa(st.ExitCode())
		}
		ok := got == parts[1]
		allOK = allOK && ok
		if !ok || len(os.Args) <= 4 {
			fmt.Printf("  new image Wait(%d): got=%s err=%v want=%s\n", pid, got, err, parts[1])
		}
	}
	v := map[bool]string{true: "PASS", false: "FAIL"}[allOK]
	fmt.Printf("  new image: %d pids checked, all statuses as expected: %s\n", len(os.Args)-2, v)
}
