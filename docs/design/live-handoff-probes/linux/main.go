// Command handoffprobes verifies the Linux-side claims of docs/design/live-handoff.md.
// Usage: handoffprobes <ptybuf|execinplace|nonchild|ptypoll|memfd> (internal stages use other names).
package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: handoffprobes <ptybuf|execinplace|nonchild|ptypoll|memfd>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "ptybuf":
		probePTYBuf()
	case "ptybuf-child":
		ptyBufChild()
	case "execinplace":
		probeExecInPlace()
	case "execinplace-after":
		execInPlaceAfter()
	case "lockcheck":
		lockCheck()
	case "client":
		queuedClient()
	case "nonchild":
		probeNonChild()
	case "nonchild-checker":
		nonChildChecker()
	case "oldd":
		oldDaemon()
	case "ptypoll":
		probePTYPoll()
	case "memfd":
		probeMemfd()
	case "memfd-after":
		memfdAfter()
	default:
		fmt.Println("unknown probe", os.Args[1])
		os.Exit(2)
	}
}

var failures int

func check(ok bool, format string, args ...any) {
	tag := "PASS"
	if !ok {
		tag = "FAIL"
		failures++
	}
	fmt.Printf("  [%s] %s\n", tag, fmt.Sprintf(format, args...))
}

func finish(name string) {
	if failures > 0 {
		fmt.Printf("RESULT %s: %d FAILED checks\n", name, failures)
		os.Exit(1)
	}
	fmt.Printf("RESULT %s: all checks passed\n", name)
}

// openPTY returns master and slave fds using the raw Linux ioctls.
func openPTY() (master, slave int) {
	m, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	must(err)
	must(unix.IoctlSetPointerInt(m, unix.TIOCSPTLCK, 0))
	n, err := unix.IoctlGetInt(m, unix.TIOCGPTN)
	must(err)
	s, err := unix.Open(fmt.Sprintf("/dev/pts/%d", n), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	must(err)
	return m, s
}

func makeRaw(fd int) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	must(err)
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	must(unix.IoctlSetTermios(fd, unix.TCSETS, t))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func fdList() string {
	ents, _ := os.ReadDir("/proc/self/fd")
	var out []string
	for _, e := range ents {
		l, _ := os.Readlink("/proc/self/fd/" + e.Name())
		out = append(out, e.Name()+"->"+l)
	}
	return strings.Join(out, " ")
}

func cloexec(fd int) string {
	f, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		return "err:" + err.Error()
	}
	if f&unix.FD_CLOEXEC != 0 {
		return "CLOEXEC"
	}
	return "inheritable"
}
