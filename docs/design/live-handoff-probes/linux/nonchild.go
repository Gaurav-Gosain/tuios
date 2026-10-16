package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func ppidOf(pid int) int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	f := strings.Fields(s[i+2:])
	p, _ := strconv.Atoi(f[1])
	return p
}

func probeNonChild() {
	fmt.Println("== probe 3: exit status of a non-child, and subreaper (Linux) ==")
	target := exec.Command("sh", "-c", "sleep 1; exit 5")
	must(target.Start())
	tp := target.Process.Pid
	checker := exec.Command("/proc/self/exe", "nonchild-checker", strconv.Itoa(tp))
	checker.Stdout = os.Stdout
	checker.Stderr = os.Stderr
	must(checker.Start())
	must(checker.Wait())

	// negative control: the real parent can waitid(P_PIDFD) and gets the status
	pfd, err := unix.PidfdOpen(tp, 0)
	if err != nil {
		// target may already be reaped by nothing yet; it is a zombie of ours, pidfd_open works on zombies
		check(false, "parent pidfd_open: %v", err)
	} else {
		var info unix.Siginfo
		err = unix.Waitid(unix.P_PIDFD, pfd, &info, unix.WEXITED, nil)
		status := siginfoStatus(&info)
		fmt.Printf("  negative control: real parent waitid(P_PIDFD) -> err=%v status=%d\n", err, status)
		check(err == nil && status == 5, "the parent gets exit status 5 through the same call, so ECHILD in the checker is about parenthood")
	}

	fmt.Println("-- PR_SET_CHILD_SUBREAPER: S (this process) -> D (old daemon) -> P (pane), D exits --")
	subreaperRun(true)
	fmt.Println("-- negative control: same without subreaper --")
	subreaperRun(false)
	finish("nonchild")
}

// siginfoStatus extracts si_status from a CLD_EXITED siginfo (offset 24 on 64-bit Linux).
func siginfoStatus(info *unix.Siginfo) int {
	b := (*[128]byte)(unsafe.Pointer(info))
	return int(int32(uint32(b[24]) | uint32(b[25])<<8 | uint32(b[26])<<16 | uint32(b[27])<<24))
}

func nonChildChecker() {
	tp, _ := strconv.Atoi(os.Args[2])
	fmt.Printf("  checker pid=%d (sibling, not parent) target pid=%d target ppid=%d\n", os.Getpid(), tp, ppidOf(tp))
	pfd, err := unix.PidfdOpen(tp, 0)
	check(err == nil, "pidfd_open on a non-child works (err=%v)", err)
	if err != nil {
		return
	}
	fds := []unix.PollFd{{Fd: int32(pfd), Events: unix.POLLIN}}
	n, _ := unix.Poll(fds, 200)
	fmt.Printf("  poll(pidfd, 200ms) while alive -> n=%d revents=%#x\n", n, fds[0].Revents)
	check(n == 0, "negative control: pidfd not readable while target alive")
	start := time.Now()
	fds[0].Revents = 0
	n, _ = unix.Poll(fds, 3000)
	fmt.Printf("  poll(pidfd, 3s) -> n=%d revents=%#x after %v\n", n, fds[0].Revents, time.Since(start).Round(time.Millisecond))
	check(n == 1 && fds[0].Revents&unix.POLLIN != 0, "pidfd becomes readable when the non-child exits (liveness works)")

	var info unix.Siginfo
	err = unix.Waitid(unix.P_PIDFD, pfd, &info, unix.WEXITED|unix.WNOWAIT, nil)
	fmt.Printf("  waitid(P_PIDFD, WEXITED|WNOWAIT) from non-child -> err=%v\n", err)
	check(errors.Is(err, unix.ECHILD), "non-child gets ECHILD: no exit status available")
	err = unix.Waitid(unix.P_PID, tp, &info, unix.WEXITED|unix.WNOWAIT, nil)
	fmt.Printf("  waitid(P_PID, WEXITED|WNOWAIT) from non-child -> err=%v\n", err)
	check(errors.Is(err, unix.ECHILD), "P_PID also ECHILD")
	// /proc/<pid>/stat exit_code field (52) is readable while zombie? report it
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", tp))
	if err == nil {
		s := string(b)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+2:])
		fmt.Printf("  /proc/%d/stat of zombie: state=%s exit_code field=%s (raw wait status, readable by a non-child while it is a zombie)\n", tp, f[0], f[len(f)-1])
	} else {
		fmt.Printf("  /proc/%d/stat unreadable: %v\n", tp, err)
	}
	finish("nonchild-checker")
}

func subreaperRun(sub bool) {
	if sub {
		must(unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0))
	} else {
		must(unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 0, 0, 0, 0))
	}
	d := exec.Command("/proc/self/exe", "oldd")
	out, err := d.StdoutPipe()
	must(err)
	must(d.Start())
	line, _ := bufio.NewReader(out).ReadString('\n')
	must(d.Wait())
	pp, _ := strconv.Atoi(strings.TrimSpace(line))
	time.Sleep(100 * time.Millisecond)
	pp2 := ppidOf(pp)
	fmt.Printf("  S pid=%d, D pid=%d exited, P pid=%d now has ppid=%d\n", os.Getpid(), d.Process.Pid, pp, pp2)
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(pp, &ws, 0, nil)
	if sub {
		check(pp2 == os.Getpid(), "P reparented to the subreaper S")
		fmt.Printf("  S wait4(P) -> pid=%d exit=%d err=%v\n", wpid, ws.ExitStatus(), err)
		check(err == nil && ws.ExitStatus() == 11, "subreaper gets P's real exit status 11")
	} else {
		check(pp2 != os.Getpid(), "without subreaper P reparents elsewhere (ppid=%d)", pp2)
		fmt.Printf("  S wait4(P) -> err=%v\n", err)
		check(errors.Is(err, syscall.ECHILD), "without subreaper S cannot wait for P (ECHILD)")
		time.Sleep(1500 * time.Millisecond)
	}
}

func oldDaemon() {
	p := exec.Command("sh", "-c", "sleep 1; exit 11")
	p.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	must(p.Start())
	fmt.Println(p.Process.Pid)
	os.Exit(0)
}
