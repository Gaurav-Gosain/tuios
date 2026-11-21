package main

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/crashexit"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

const signalQuitGrace = 3 * time.Second

const forcedResetBudget = 250 * time.Millisecond

func signalExitCode(sig os.Signal) int {
	if n, ok := sig.(syscall.Signal); ok {
		return 128 + int(n)
	}
	return 1
}

func armSignalQuit(p *tea.Program) (runReturned, finish func()) {
	restore := captureTerminalState()
	var returned atomic.Bool
	crashexit.SetTeardown(func() { resetTerminalForExit(restore, returned.Load()) })
	finishQuit := armSignalQuitWith(p, signalQuitGrace, func(code int) {
		forceExit(restore, returned.Load(), code)
	})
	return func() { returned.Store(true) }, func() {
		finishQuit()
		crashexit.SetTeardown(nil)
	}
}

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

func runSignalQuit(sigs <-chan os.Signal, finished <-chan struct{}, grace time.Duration, ask func(), exit func(int)) {
	var first os.Signal
	select {
	case first = <-sigs:
	case <-finished:
		return
	}

	go ask()

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-finished:
		return
	case second := <-sigs:
		exit(signalExitCode(second))
	case <-timer.C:
		exit(signalExitCode(first))
	}
}

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

const forcedExitPrefix = "\x1b[<1u\x1b[>4m\x1b[?1049l"

const forcedExitAfterRunPrefix = "\x1b[>4m"

func forcedExitReset(runReturned bool) string {
	prefix := forcedExitPrefix
	if runReturned {
		prefix = forcedExitAfterRunPrefix
	}
	return "\x18" + prefix + terminal.ResetSequence
}

func forceExit(restoreTerminal func(), runReturned bool, code int) {
	resetTerminalForExit(restoreTerminal, runReturned)
	stopCPUProfile()
	os.Exit(code)
}

func resetTerminalForExit(restoreTerminal func(), runReturned bool) {
	restoreTerminal()
	wrote := make(chan struct{})
	go func() {
		_, _ = os.Stdout.WriteString(forcedExitReset(runReturned))
		close(wrote)
	}()
	select {
	case <-wrote:
	case <-time.After(forcedResetBudget):
	}
}
