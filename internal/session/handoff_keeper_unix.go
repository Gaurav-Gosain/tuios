//go:build !windows && !js

package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// startKeeper starts bin as the keeper of a handoff, holding a copy of every
// descriptor in hold. argv is the command line it execs if the daemon dies
// before it is released: the previous binary, adopting the manifest.
//
// The keeper is started before any descriptor loses close-on-exec, and it is
// given only the ones in hold and its socket: a pane's program never sees any
// of them. It leads a session of its own, so no signal meant for the daemon's
// group reaches it.
func startKeeper(bin string, hold []int, argv []string) (*handoffKeeper, error) {
	// Both ends are close-on-exec before any fork can see them, the way the
	// net package makes a socket.
	syscall.ForkLock.RLock()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err == nil {
		unix.CloseOnExec(pair[0])
		unix.CloseOnExec(pair[1])
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("a socket pair for the keeper: %w", err)
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		_ = unix.Close(pair[0])
		_ = unix.Close(pair[1])
		return nil, err
	}
	defer func() { _ = null.Close() }()

	files := []uintptr{null.Fd(), null.Fd(), null.Fd(), uintptr(pair[1])} //nolint:gosec // a descriptor number
	nums := make([]string, len(hold))
	for i, fd := range hold {
		files = append(files, uintptr(fd)) //nolint:gosec // a descriptor number
		nums[i] = strconv.Itoa(fd)
	}
	args := append([]string{bin, "daemon", keeperCommand, "--hold=" + strings.Join(nums, ","), "--"}, argv...)
	//tuios:allow-exec the keeper inherits exactly the descriptors it holds, which only ForkExec's file list can say
	pid, err := syscall.ForkExec(bin, args, &syscall.ProcAttr{
		Env:   withoutEnv(os.Environ(), handoffFallbackEnv),
		Files: files,
		Sys:   &syscall.SysProcAttr{Setsid: true},
	})
	_ = unix.Close(pair[1])
	if err != nil {
		_ = unix.Close(pair[0])
		return nil, fmt.Errorf("starting %s: %w", bin, err)
	}
	return &handoffKeeper{pid: pid, conn: os.NewFile(uintptr(pair[0]), "handoff-keeper")}, nil //nolint:gosec // a descriptor number
}

// RunHandoffKeeper is the keeper's whole life. It holds the descriptors it
// inherited at keeperFirstHeld on, which the manifest names as hold, and
// waits on its socket. A byte means the daemon is up, or the handoff was put
// back, and it exits. The end of the socket with no byte means the daemon's
// process is gone, and it execs argv in its own place, telling that image
// where each descriptor is. It returns only when that exec fails.
func RunHandoffKeeper(hold []int, argv []string) error {
	if len(argv) == 0 {
		return errors.New("the keeper has no command to restore the daemon with")
	}
	conn := os.NewFile(keeperConnFD, "handoff-keeper")
	var b [1]byte
	n, err := conn.Read(b[:])
	if n > 0 {
		return nil
	}
	if err != nil && !errors.Is(err, io.EOF) {
		// A reset is the same news as an end: the other side is gone.
		debugLog("[DEBUG] Live upgrade keeper: reading the daemon's socket: %v", err)
	}
	unix.CloseOnExec(keeperConnFD)
	fds := make([]int, len(hold))
	for i := range hold {
		fds[i] = keeperFirstHeld + i
	}
	env := withoutEnv(withoutEnv(os.Environ(), handoffFallbackEnv), handoffKeptEnv)
	env = append(env, handoffFallbackEnv+"=1", handoffKeptEnv+"="+formatKept(hold, keeperFirstHeld))
	return execHandoff(argv[0], argv, env, fds, nil)
}
