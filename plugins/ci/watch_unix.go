//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// take takes the lock of a watcher, or says another process holds it. The
// lock goes when the process ends, also on a kill.
func take(f *os.File) bool {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// held says a process holds the lock of f.
func held(f *os.File) bool {
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return true
}

// poke tells the watcher pid that the focus changed.
func poke(pid int) error { return syscall.Kill(pid, syscall.SIGUSR1) }

// onPoke runs fn on each poke.
func onPoke(fn func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)
	go func() {
		for range sig {
			fn()
		}
	}()
}
