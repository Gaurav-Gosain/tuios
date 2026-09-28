package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// The signal policy exists because a quit can be asked for and never carried
// out: a force-killed ssh client leaves a pty nobody drains, the frame write
// that fills it wedges the event loop, and the quit sitting in the message
// channel is never read. These drive the policy directly, without a program or
// a real signal, by passing the channel, the ask and the exit in.

// shortGrace stands in for signalQuitGrace where a test waits for it to elapse.
// The grace is a parameter rather than a variable the tests overwrite, so the
// policy goroutine never reads a value a test's cleanup is writing.
const shortGrace = 30 * time.Millisecond

func TestSignalQuitAsksThenExitsWhenTheQuitIsNotCarriedOut(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	done := make(chan struct{})
	asked := make(chan struct{})
	exited := make(chan int, 1)

	go runSignalQuit(sigs, done, shortGrace, func() { close(asked) }, func(code int) { exited <- code })

	sigs <- syscall.SIGTERM

	select {
	case <-asked:
	case <-time.After(time.Second):
		t.Fatal("the quit was never asked for")
	}

	select {
	case code := <-exited:
		if want := 128 + int(syscall.SIGTERM); code != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the process was never told to exit after the grace elapsed")
	}
}

func TestSignalQuitExitsAtOnceOnASecondSignal(t *testing.T) {
	// A grace long enough that only the second signal can end it, so the test
	// fails if the second signal is not what did.
	sigs := make(chan os.Signal, 2)
	done := make(chan struct{})
	asked := make(chan struct{})
	release := make(chan struct{})
	exited := make(chan int, 1)

	// The ask blocks, standing in for a Send that the event loop never reads.
	go runSignalQuit(sigs, done, time.Hour, func() { close(asked); <-release }, func(code int) { exited <- code })

	sigs <- syscall.SIGTERM
	<-asked
	sigs <- syscall.SIGINT

	select {
	case code := <-exited:
		if want := 128 + int(syscall.SIGINT); code != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second signal did not end the wait")
	}
	close(release)
}

func TestSignalQuitLeavesWhenTheProgramReturns(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	done := make(chan struct{})
	asked := make(chan struct{})
	exited := make(chan int, 1)

	go runSignalQuit(sigs, done, time.Hour, func() { close(asked) }, func(code int) { exited <- code })

	sigs <- syscall.SIGTERM
	<-asked
	close(done)

	select {
	case code := <-exited:
		t.Fatalf("exited with %d after the program had already returned", code)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSignalQuitDoesNothingWhenTheProgramIsAlreadyGone(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	done := make(chan struct{})
	asked := make(chan struct{})
	exited := make(chan int, 1)

	close(done)
	go runSignalQuit(sigs, done, shortGrace, func() { close(asked) }, func(code int) { exited <- code })

	select {
	case <-asked:
		t.Fatal("asked to quit after the program had returned")
	case code := <-exited:
		t.Fatalf("exited with %d after the program had returned", code)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSignalExitCode(t *testing.T) {
	if got, want := signalExitCode(syscall.SIGTERM), 143; got != want {
		t.Errorf("SIGTERM exit code = %d, want %d", got, want)
	}
	if got, want := signalExitCode(syscall.SIGINT), 130; got != want {
		t.Errorf("SIGINT exit code = %d, want %d", got, want)
	}
}
