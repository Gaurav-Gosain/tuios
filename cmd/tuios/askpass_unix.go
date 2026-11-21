//go:build unix

package main

import (
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// foregroundTerminal opens the controlling terminal when the program that
// ran this one, ssh, is in its foreground job, and that job is an ssh or
// git command: a person typed it there and types its answers there. A
// background child of an agent may still hold the terminal, and an agent may
// run its commands in its own foreground job, and reading the terminal would
// take the agent's keys, so neither counts.
func foregroundTerminal() (*os.File, bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, false
	}
	fg, err := unix.IoctlGetInt(int(tty.Fd()), unix.TIOCGPGRP)
	asker, err2 := syscall.Getpgid(os.Getppid())
	if err != nil || err2 != nil || fg != asker || !askpassJobTyped(fg) {
		_ = tty.Close()
		return nil, false
	}
	return tty, true
}

// askpassJobTyped reports whether the foreground job led by pgid is a
// command a person types to reach a host: its leader is ssh, git or one of
// their tools. Where the leader cannot be read it counts, as before.
func askpassJobTyped(pgid int) bool {
	name, ok := jobLeaderName(pgid)
	if !ok {
		return true
	}
	switch name {
	case "ssh", "scp", "sftp", "ssh-add", "ssh-keygen", "ssh-copy-id", "sshpass", "rsync", "git", "mosh":
		return true
	}
	return strings.HasPrefix(name, "git-")
}

// readHidden reads one line from tty with echo off. A signal while it reads,
// such as ctrl+c, puts echo back before the program ends, as ssh's own
// prompt does, so the terminal is not left silent.
func readHidden(tty *os.File) ([]byte, error) {
	fd := int(tty.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	done := make(chan struct{})
	defer func() {
		signal.Stop(sigs)
		close(done)
	}()
	go func() {
		select {
		case <-sigs:
			_ = term.Restore(fd, state)
			_, _ = tty.WriteString("\n")
			os.Exit(1)
		case <-done:
		}
	}()
	return term.ReadPassword(fd)
}
