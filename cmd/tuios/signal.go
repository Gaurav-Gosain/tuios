package main

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// signalQuitGrace is how long a requested quit may take before the process
// stops waiting and exits. A Bubble Tea quit is normally immediate; the grace
// elapses only when the event loop cannot act on the request at all, or the
// cleanup after it is stuck.
const signalQuitGrace = 3 * time.Second

// forcedResetBudget bounds the terminal reset written on a forced exit. That
// write goes to the same terminal that stopped draining, so it may never
// finish; the process leaves without it rather than wait.
const forcedResetBudget = 250 * time.Millisecond

// signalExitCode is the shell's 128 + the signal number, so a caller sees the
// run was signalled and by which one.
func signalExitCode(sig os.Signal) int {
	if n, ok := sig.(syscall.Signal); ok {
		return 128 + int(n)
	}
	return 1
}

// armSignalQuit turns SIGINT and SIGTERM into a quit, and guarantees the
// process ends even when the program cannot carry one out. It returns finish,
// which the caller runs once its cleanup after Program.Run is done.
//
// The guarantee is the point. The quit is asked for with Program.Send, which
// writes to the message channel the event loop reads (tuios passes
// tea.WithoutSignalHandler, so this is the only handler). When the loop is
// stuck writing a frame to a terminal that has stopped draining, that write is
// never read. A force-killed SSH client leaves exactly that behind: a socket
// sshd has not noticed is gone, so it stops draining the pty and the frame
// write blocks. The signal was delivered; the process simply cannot obey it,
// and only SIGHUP, which nothing catches, ends it. So the quit is asked for
// and then, if the run and its cleanup have not finished within
// signalQuitGrace, the process exits outright. A second signal is somebody who
// has already waited once and exits at once.
//
// The handler stays registered until finish, not until Program.Run returns.
// Unregistered during cleanup, a second Ctrl+C would take the default action
// and end the process with the terminal still raw; registered, it follows the
// same policy, and a forced exit restores the terminal first.
//
// Call it before Program.Run: it records the terminal's state before Bubble
// Tea puts it in raw mode, which is what a forced exit restores.
func armSignalQuit(p *tea.Program) (finish func()) {
	restore := captureTerminalState()
	return armSignalQuitWith(p, signalQuitGrace, func(code int) {
		forceExit(restore, code)
	})
}

// armSignalQuitWith is armSignalQuit with the grace and the exit passed in, so
// a test can arm it against a real program and a real signal without the test
// binary exiting.
func armSignalQuitWith(p *tea.Program, grace time.Duration, exit func(int)) (finish func()) {
	sigs := make(chan os.Signal, 2)
	finished := make(chan struct{})
	stopped := make(chan struct{})
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer close(stopped)
		runSignalQuit(sigs, finished, grace, func() { p.Send(tea.QuitMsg{}) }, exit)
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(finished)
			<-stopped
			signal.Stop(sigs)
		})
	}
}

// runSignalQuit is armSignalQuit's policy, with the signal source, the quit and
// the exit passed in so a test can drive it without a program or a real signal.
// finished is closed once the program has returned and its cleanup is done.
func runSignalQuit(sigs <-chan os.Signal, finished <-chan struct{}, grace time.Duration, ask func(), exit func(int)) {
	var first os.Signal
	select {
	case first = <-sigs:
	case <-finished:
		return
	}

	// On its own goroutine: Send blocks until the event loop reads it, and the
	// loop is the thing that may never get there. Once the program has
	// returned, Send returns at once, so a signal during cleanup costs nothing.
	go ask()

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-finished:
		// The quit landed and the cleanup finished without us.
		return
	case second := <-sigs:
		exit(signalExitCode(second))
	case <-timer.C:
		exit(signalExitCode(first))
	}
}

// captureTerminalState records the termios of stdin and stdout, where they are
// terminals, and returns what puts them back. It must run before Bubble Tea
// switches the terminal to raw mode.
func captureTerminalState() (restore func()) {
	type saved struct {
		fd    int
		state *term.State
	}
	var states []saved
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fd := int(f.Fd())
		if !term.IsTerminal(fd) {
			continue
		}
		if st, err := term.GetState(fd); err == nil {
			states = append(states, saved{fd, st})
		}
	}
	return func() {
		for _, s := range states {
			_ = term.Restore(s.fd, s.state)
		}
	}
}

// forceExit ends a process whose program could not quit, leaving the terminal
// as usable as it can.
//
// The termios restore goes first because it is an ioctl, which cannot block on
// a terminal that stopped reading output, and it alone takes the terminal out
// of raw mode. The escape sequences that leave the alternate screen and turn
// mouse tracking off are output, so they are written under a deadline: when
// the terminal is still alive (a slow link, a plain kill -TERM) they land, and
// when it is not the process leaves without them. They are led by CAN, which
// cancels any escape sequence a frame write left half finished, so the reset
// is not swallowed into it.
//
// The daemon client's detach sync and Close are skipped: both can block on the
// same wedge, and the session itself lives on in the daemon.
func forceExit(restoreTerminal func(), code int) {
	restoreTerminal()
	wrote := make(chan struct{})
	go func() {
		_, _ = os.Stdout.WriteString("\x18" + terminal.ResetSequence)
		close(wrote)
	}()
	select {
	case <-wrote:
	case <-time.After(forcedResetBudget):
	}
	os.Exit(code)
}
