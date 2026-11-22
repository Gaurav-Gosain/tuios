package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// What an attach through a host shows while it waits.
//
// Before the TUI takes the screen, an attach through a host waits on three
// steps: this machine's daemon opens a connection over its link, the host's
// daemon answers the hello, and the host's daemon attaches the session. The
// first step alone can take the daemon's 15 second budget, and each read of
// the other two is bounded at 30 seconds. Nothing was drawn during any of
// them, so a person saw a terminal with nothing on it. Now each step prints
// one line as it starts, and the whole wait has one budget: when it runs out
// the connection is closed and the error names the step.

// hostAttachBudget bounds an attach through a host, from the request for
// the link to the attach reply. The link step ends within the daemon's own
// budget of 15 seconds, which leaves the hello and the attach the rest.
const hostAttachBudget = 30 * time.Second

// hostWait follows one attach through a host.
type hostWait struct {
	host   string
	budget time.Duration
	out    io.Writer

	mu      sync.Mutex
	stage   string
	timer   *time.Timer
	done    bool
	expired bool
}

// startHostWait prints the first step and arms the budget. closeConn is
// called once if the budget runs out before stop. It ends the read the
// attach is blocked in.
func startHostWait(host string, budget time.Duration, out io.Writer, closeConn func()) *hostWait {
	w := &hostWait{host: host, budget: budget, out: out}
	w.step(fmt.Sprintf("Connecting to %s. Waiting for the link...", host), "the link to "+host)
	w.timer = time.AfterFunc(budget, func() {
		w.mu.Lock()
		if w.done {
			w.mu.Unlock()
			return
		}
		w.expired = true
		w.mu.Unlock()
		closeConn()
	})
	return w
}

// step prints line and records stage, the step the error names.
func (w *hostWait) step(line, stage string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.stage = stage
	_, _ = fmt.Fprintln(w.out, line)
}

// stop ends the wait. It reports false when the budget ran out first, and
// then the connection is closed.
func (w *hostWait) stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	if w.timer != nil {
		w.timer.Stop()
	}
	return !w.expired
}

// timedOut reports whether the budget ran out.
func (w *hostWait) timedOut() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.expired
}

// err is the error for a wait whose budget ran out.
func (w *hostWait) err(cause error) error {
	w.mu.Lock()
	stage := w.stage
	w.mu.Unlock()
	return &diagnosticError{
		What:  fmt.Sprintf("The attach on %s did not finish in %d seconds. It waited for %s.", w.host, int(w.budget.Seconds()), stage),
		Cause: "the link or the tuios on " + w.host + " stopped answering.",
		Fix:   "run 'tuios hosts' to see the link, and 'tuios attach --host " + w.host + " NAME --ssh' to use ssh directly",
		Err:   cause,
	}
}

// hostWaitOut is where the steps are printed: the terminal the TUI is about
// to take.
var hostWaitOut io.Writer = os.Stdout
