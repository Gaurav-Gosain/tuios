package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const sockPath = "/tmp/handoff-probe.sock"
const lockPath = "/tmp/handoff-probe.lock"

func pidfdsInSelf() []string {
	ents, _ := os.ReadDir("/proc/self/fd")
	var out []string
	for _, e := range ents {
		l, _ := os.Readlink("/proc/self/fd/" + e.Name())
		if strings.Contains(l, "pidfd") {
			fd, _ := strconv.Atoi(e.Name())
			out = append(out, fmt.Sprintf("fd%d(%s,%s)", fd, l, cloexec(fd)))
		}
	}
	return out
}

func sigLine(key string) string {
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(l, key+":"))
		}
	}
	return "?"
}

func probeExecInPlace() {
	fmt.Println("== probe 2: exec in place (Linux) ==")
	pid := os.Getpid()
	fmt.Printf("  before exec: pid=%d\n", pid)

	c7 := exec.Command("sh", "-c", "exit 7")
	c9 := exec.Command("sh", "-c", "sleep 1; exit 9")
	must(c7.Start())
	must(c9.Start())
	fmt.Printf("  started children: exit7 pid=%d, exit9 pid=%d\n", c7.Process.Pid, c9.Process.Pid)
	pfds := pidfdsInSelf()
	fmt.Printf("  pidfds held by this process after os.StartProcess: %v\n", pfds)
	check(len(pfds) >= 2, "Go %s opened one pidfd per child (CLONE_PIDFD)", "runtime")
	allCloexec := len(pfds) > 0
	for _, p := range pfds {
		if !strings.Contains(p, "CLOEXEC") {
			allCloexec = false
		}
	}
	check(allCloexec, "every pidfd is CLOEXEC, so it vanishes at exec")

	time.Sleep(300 * time.Millisecond)
	st, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", c7.Process.Pid))
	fmt.Printf("  exit7 child state before exec: %s\n", procState(string(st)))
	check(procState(string(st)) == "Z", "exit7 child is a zombie before exec")

	os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	must(err)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	lf, err := ln.(*net.UnixListener).File() // dup, so the fd number is stable and ours
	must(err)
	lockF, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	must(err)
	must(unix.Flock(int(lockF.Fd()), unix.LOCK_EX|unix.LOCK_NB))

	client := exec.Command("/proc/self/exe", "client")
	client.Stdout = os.Stdout
	must(client.Start())
	time.Sleep(200 * time.Millisecond)
	fmt.Println("  client dialed and wrote before exec; connection not accepted")

	lockcheck := exec.Command("/proc/self/exe", "lockcheck")
	out, _ := lockcheck.CombinedOutput()
	fmt.Printf("  lockcheck before exec: %s", out)

	lfd := int(lf.Fd())
	kfd := int(lockF.Fd())
	env := append(os.Environ(),
		"PROBE_PID="+strconv.Itoa(pid),
		"PROBE_LFD="+strconv.Itoa(lfd),
		"PROBE_KFD="+strconv.Itoa(kfd),
		"PROBE_C7="+strconv.Itoa(c7.Process.Pid),
		"PROBE_C9="+strconv.Itoa(c9.Process.Pid),
		"PROBE_CLIENT="+strconv.Itoa(client.Process.Pid),
	)
	fmt.Printf("  SigIgn before exec=%s SigBlk(main thread)=%s\n", sigLine("SigIgn"), sigLine("SigBlk"))
	syscall.ForkLock.Lock()
	_, err = unix.FcntlInt(uintptr(lfd), unix.F_SETFD, 0)
	must(err)
	_, err = unix.FcntlInt(uintptr(kfd), unix.F_SETFD, 0)
	must(err)
	err = syscall.Exec("/proc/self/exe", []string{os.Args[0], "execinplace-after"}, env)
	syscall.ForkLock.Unlock()
	panic(err)
}

func execInPlaceAfter() {
	pid := os.Getpid()
	wantPid, _ := strconv.Atoi(os.Getenv("PROBE_PID"))
	lfd, _ := strconv.Atoi(os.Getenv("PROBE_LFD"))
	kfd, _ := strconv.Atoi(os.Getenv("PROBE_KFD"))
	c7, _ := strconv.Atoi(os.Getenv("PROBE_C7"))
	c9, _ := strconv.Atoi(os.Getenv("PROBE_C9"))
	fmt.Printf("  after exec: pid=%d\n", pid)
	check(pid == wantPid, "same pid after syscall.Exec")
	fmt.Printf("  fds after exec: %s\n", fdList())
	check(len(pidfdsInSelf()) == 0, "no pidfd survived exec")
	fmt.Printf("  SigIgn after exec=%s SigBlk=%s\n", sigLine("SigIgn"), sigLine("SigBlk"))
	ign, _ := strconv.ParseUint(sigLine("SigIgn"), 16, 64)
	check(ign&(1<<(uint(syscall.SIGCHLD)-1)) == 0, "SIGCHLD is not ignored after exec (otherwise children would auto-reap and Wait would get ECHILD)")
	check(cloexec(lfd) == "inheritable" && cloexec(kfd) == "inheritable", "listener fd %d and lock fd %d survived exec", lfd, kfd)

	out, _ := exec.Command("/proc/self/exe", "lockcheck").CombinedOutput()
	fmt.Printf("  lockcheck after exec (other process): %s", out)
	check(strings.Contains(string(out), "HELD"), "LOCK_EX still held across exec, seen from another process")

	ln, err := net.FileListener(os.NewFile(uintptr(lfd), "listener"))
	must(err)
	ln.(*net.UnixListener).SetDeadline(time.Now().Add(2 * time.Second))
	conn, err := ln.Accept()
	if err != nil {
		check(false, "accept of queued connection: %v", err)
	} else {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		b, _ := io.ReadAll(io.LimitReader(conn, 64))
		fmt.Printf("  accepted queued connection, read %q\n", b)
		check(string(b) == "hello-before-exec", "net.FileListener accepted the pre-exec queued connection and read its data")
		conn.Close()
	}
	// negative control for accept: no second connection is queued, so a short deadline must time out
	ln.(*net.UnixListener).SetDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = ln.Accept()
	check(err != nil, "negative control: a second Accept with nothing queued times out (%v)", err)

	p7, _ := os.FindProcess(c7)
	fmt.Printf("  pidfds after FindProcess(c7): %v\n", pidfdsInSelf())
	s7, err := p7.Wait()
	fmt.Printf("  FindProcess(%d).Wait() -> %v err=%v\n", c7, s7, err)
	check(err == nil && s7.ExitCode() == 7, "pre-exec zombie reaped with real exit code 7")
	p9, _ := os.FindProcess(c9)
	s9, err := p9.Wait()
	fmt.Printf("  FindProcess(%d).Wait() -> %v err=%v\n", c9, s9, err)
	check(err == nil && s9.ExitCode() == 9, "child exiting after exec reaped with real exit code 9")

	// negative controls
	p7again, _ := os.FindProcess(c7)
	_, err = p7again.Wait()
	fmt.Printf("  negative control: second Wait on reaped pid %d -> err=%v\n", c7, err)
	check(err != nil, "a second Wait on an already-reaped child fails (would detect lost status)")
	p1, _ := os.FindProcess(1)
	_, err = p1.Wait()
	fmt.Printf("  negative control: Wait on non-child pid 1 -> err=%v\n", err)
	check(err != nil, "Wait on a non-child fails, so the 7/9 above came from parenthood")

	unix.Close(kfd)
	out, _ = exec.Command("/proc/self/exe", "lockcheck").CombinedOutput()
	fmt.Printf("  negative control lockcheck after closing lock fd: %s", out)
	check(strings.Contains(string(out), "FREE"), "lockcheck reports FREE once the fd is closed, so HELD above was real")
	if cp, err := strconv.Atoi(os.Getenv("PROBE_CLIENT")); err == nil {
		p, _ := os.FindProcess(cp)
		p.Wait()
	}
	finish("execinplace")
}

func lockCheck() {
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	must(err)
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		fmt.Println("HELD (LOCK_EX|LOCK_NB -> EWOULDBLOCK)")
		return
	}
	if err == nil {
		fmt.Println("FREE (acquired)")
		return
	}
	fmt.Println("ERR", err)
}

func queuedClient() {
	c, err := net.Dial("unix", sockPath)
	must(err)
	_, err = c.Write([]byte("hello-before-exec"))
	must(err)
	c.(*net.UnixConn).CloseWrite()
	time.Sleep(3 * time.Second)
}
