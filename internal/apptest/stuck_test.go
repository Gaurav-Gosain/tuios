package apptest

import (
	"strings"
	"testing"
	"time"
)

// blockedCommand stands for a command stuck in its own code.
func blockedCommand(release <-chan struct{}) {
	<-release
}

// otherCommand is a goroutine in somebody else's code.
func otherCommand(release <-chan struct{}) {
	<-release
}

// TestStuckMessageSaysWhereTheCommandIsBlocked holds the failure of a command
// that did not answer in time to what a person needs from it on a slow machine:
// the old sentence, and the stack of the goroutine that is in the command's code.
//
// How it could pass wrongly, written down first:
//   - The stack could be there for any goroutine. Another goroutine blocked at
//     the same moment, in other code, must not be in the message.
//   - The message could be right only while the command is blocked. A command
//     that finished a moment after the limit leaves no goroutine, and the
//     message must say that and not show an empty list as if it were an answer.
//
// Negative control: a message built with no stacks fails the first two cases, and
// one with every goroutine in it fails the first and the last.
func TestStuckMessageSaysWhereTheCommandIsBlocked(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{}, 2)
	go func() { started <- struct{}{}; blockedCommand(release) }()
	go func() { started <- struct{}{}; otherCommand(release) }()
	<-started
	<-started
	// Both goroutines are running; give them a moment to park in the receive.
	var dump string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		dump = goroutineDump()
		if strings.Contains(dump, "apptest.blockedCommand") && strings.Contains(dump, "apptest.otherCommand") {
			break
		}
	}

	t.Run("a command blocked in its own code", func(t *testing.T) {
		msg := stuckMessage("github.com/Gaurav-Gosain/tuios/internal/apptest.blockedCommand", 5*time.Second, dump)
		if !strings.Contains(msg, "has not answered after 5s; if it waits by design, add it to parked") {
			t.Errorf("the old sentence is gone: %q", msg)
		}
		// The command's name is in the first sentence, so the proof of a stack is
		// what only a stack has: the goroutine's state and its source line.
		if !strings.Contains(msg, "[chan receive") || !strings.Contains(msg, "stuck_test.go:") {
			t.Errorf("the message has no stack of the blocked command:\n%s", msg)
		}
		if strings.Contains(msg, "otherCommand(") {
			t.Errorf("the message has the stack of a goroutine in other code:\n%s", msg)
		}
	})
	t.Run("a closure of the command", func(t *testing.T) {
		msg := stuckMessage("github.com/Gaurav-Gosain/tuios/internal/apptest.blockedCommand.func1", time.Second, dump)
		if !strings.Contains(msg, "stuck_test.go:") {
			t.Errorf("the closure's name did not find its function's stack:\n%s", msg)
		}
	})
	t.Run("a command that finished a moment after the limit", func(t *testing.T) {
		msg := stuckMessage("github.com/Gaurav-Gosain/tuios/internal/apptest.noSuchCommand", time.Second, dump)
		if !strings.Contains(msg, "finished just after the limit") || strings.Contains(msg, "goroutines in it") {
			t.Errorf("a command with no goroutine was not said to have finished:\n%s", msg)
		}
	})
}
