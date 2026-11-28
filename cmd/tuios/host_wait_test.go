package main

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestHostWaitNamesTheStepThatRanOut: a wait whose budget runs out closes
// the connection once, and its error names the last step and what to do.
func TestHostWaitNamesTheStepThatRanOut(t *testing.T) {
	var out bytes.Buffer
	var closed atomic.Int32
	w := startHostWait("build", 20*time.Millisecond, &out, func() { closed.Add(1) })
	w.step("Attaching session \"work\" on build...", "tuios on build to attach session \"work\"")
	deadline := time.Now().Add(5 * time.Second)
	for closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if closed.Load() != 1 {
		t.Fatalf("the connection was closed %d times, want 1", closed.Load())
	}
	if w.stop() {
		t.Fatal("stop reported the wait in time after the budget ran out")
	}
	msg := w.err(errors.New("use of closed network connection")).Error()
	for _, want := range []string{"tuios on build to attach session \"work\"", "tuios hosts"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q:\n%s", want, msg)
		}
	}
	if got := out.String(); !strings.HasPrefix(got, "Connecting to build. Waiting for the link...\n") {
		t.Errorf("the first line is not the link step:\n%s", got)
	}
}

// TestHostWaitInTimeClosesNothing: a wait stopped before its budget closes
// nothing and prints nothing more.
func TestHostWaitInTimeClosesNothing(t *testing.T) {
	var out bytes.Buffer
	var closed atomic.Int32
	w := startHostWait("build", 50*time.Millisecond, &out, func() { closed.Add(1) })
	if !w.stop() {
		t.Fatal("stop reported the budget ran out")
	}
	w.step("late line", "late")
	time.Sleep(100 * time.Millisecond)
	if closed.Load() != 0 {
		t.Fatal("a wait stopped in time closed the connection")
	}
	if strings.Contains(out.String(), "late line") {
		t.Errorf("a step after stop was printed:\n%s", out.String())
	}
}
