//go:build linux && (amd64 || arm64)

package main

import (
	"syscall"
	"unsafe"
	_ "unsafe" // go:linkname

	"github.com/ebitengine/purego"
)

// GTK runs its main loop inside one long purego call, so the Go runtime
// sees the main thread as an M in a syscall that holds a P. Each time GTK
// calls back into Go (an onMain hop, an event, a frame), the callback
// takes a P back and wakes sysmon from its deep sleep. sysmon then polls
// every 20 µs, backing off to 10 ms, until it retakes the P of the
// main thread 10 ms later: 40 to 60 wakeups of an otherwise idle process
// for each callback.
//
// installGLibPoll gives the main context a poll function in Go that hands
// the P off before it waits (entersyscallblock), as the runtime does for
// its own blocking calls. While GTK waits every P is idle and sysmon
// sleeps until the next callback, which costs it a few wakeups instead of
// tens.
//
// This belongs in MyGo's Linux backend (internal/linux/poll.go of the
// patch in the wakeups notes). It lives here until MyGo takes it, so guios
// needs no fork of MyGo.

//go:linkname entersyscallblock runtime.entersyscallblock
func entersyscallblock()

//go:linkname exitsyscall runtime.exitsyscall
func exitsyscall()

// blockingPPoll is ppoll(2) with the P handed off for the wait. It must not
// grow its stack or allocate between entersyscallblock and exitsyscall.
//
//go:nosplit
func blockingPPoll(fds, nfds uintptr, ts *syscall.Timespec) (uintptr, syscall.Errno) {
	entersyscallblock()
	r, _, e := syscall.RawSyscall6(syscall.SYS_PPOLL, fds, nfds, uintptr(unsafe.Pointer(ts)), 0, 0, 0)
	exitsyscall()
	return r, e
}

// glibPoll is a GPollFunc: GPollFD has the layout of struct pollfd, and the
// timeout is in milliseconds, negative for none.
func glibPoll(fds uintptr, nfds uint32, timeout int32) int32 {
	if timeout == 0 {
		// A check that does not wait keeps the P.
		r, _, e := syscall.RawSyscall6(syscall.SYS_PPOLL, fds, uintptr(nfds), uintptr(unsafe.Pointer(&syscall.Timespec{})), 0, 0, 0)
		if e != 0 {
			return 0
		}
		return int32(r)
	}
	var ts *syscall.Timespec
	if timeout > 0 {
		t := syscall.NsecToTimespec(int64(timeout) * 1e6)
		ts = &t
	}
	r, e := blockingPPoll(fds, uintptr(nfds), ts)
	switch {
	case e == syscall.EINTR:
		// GLib reads errno, which a raw syscall does not set. Report no
		// ready descriptors, and GLib polls again.
		return 0
	case e != 0:
		return -1
	}
	return int32(r)
}

var glibPollCallback uintptr

// installGLibPoll sets glibPoll on GLib's default main context, which only
// the main thread iterates. Call it once, on the main thread. Without
// libglib (no GTK) it does nothing.
func installGLibPoll() {
	if glibPollCallback != 0 {
		return
	}
	lib, err := purego.Dlopen("libglib-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	sym, err := purego.Dlsym(lib, "g_main_context_set_poll_func")
	if err != nil {
		return
	}
	var setPollFunc func(ctx uintptr, fn uintptr)
	purego.RegisterFunc(&setPollFunc, sym)
	glibPollCallback = purego.NewCallback(glibPoll)
	setPollFunc(0, glibPollCallback)
}
