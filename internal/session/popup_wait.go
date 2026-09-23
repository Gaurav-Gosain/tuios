package session

import (
	"bytes"
	"os"
	"sync"
	"time"
)

// A popup the caller waits on.
//
// popup with wait keeps the call open until the popup's command exits and
// returns its exit status, so a script can use a popup the way it uses a
// command: run a picker, get the answer. capture_stdout sends the command's
// standard output to a pipe the daemon owns instead of the pane, and returns
// it. A picker such as fzf or gum draws on the terminal and prints only the
// choice to stdout, so the choice comes back and the drawing stays on the
// screen.
//
// Nothing here grants anything new. The caller picked the command, the popup
// runs it with the caller's rights, and what comes back is what that command
// printed. The pipe is the daemon's: it is handed to the popup's process
// alone and read only by the call that asked for it.

// popupCaptureMax bounds the captured output. A picker's answer is a line; the
// bound is there for the command that prints a file.
const popupCaptureMax = 1 << 20

// popupDrainWait is how long the capture keeps reading after the command
// exits. A child that inherited the pipe can hold it open after its parent is
// gone, and the call must not wait on it.
const popupDrainWait = time.Second

// popupCapture reads a popup's standard output from the daemon's end of the
// pipe, keeping at most popupCaptureMax bytes.
type popupCapture struct {
	r         *os.File
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
	done      chan struct{}
}

func newPopupCapture(r *os.File) *popupCapture {
	c := &popupCapture{r: r, done: make(chan struct{})}
	go c.read()
	return c
}

// read drains the pipe until every writer has closed it. It keeps reading
// past the bound and past a call that stopped waiting, so the command never
// blocks or dies on a full or closed pipe.
func (c *popupCapture) read() {
	defer close(c.done)
	defer func() { _ = c.r.Close() }()
	chunk := make([]byte, 32*1024)
	for {
		n, err := c.r.Read(chunk)
		if n > 0 {
			c.mu.Lock()
			room := popupCaptureMax - c.buf.Len()
			if n > room {
				n, c.truncated = max(room, 0), true
			}
			c.buf.Write(chunk[:n])
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// finish waits a moment for the rest of the output and returns what was read.
// A child still holding the pipe keeps the reader draining after this returns.
func (c *popupCapture) finish() (string, bool) {
	select {
	case <-c.done:
	case <-time.After(popupDrainWait):
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.truncated
}

// waitPopupExit blocks until the popup's process exits, the timeout passes (a
// zero timeout waits for as long as the popup is open), or the daemon stops.
// It reports the exit status and whether the process exited.
func (d *Daemon) waitPopupExit(sess *Session, win WindowState, timeout time.Duration) (int, bool) {
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		return -1, true
	}
	sub := d.events.subscribe(eventFilter{
		session: sess.Name,
		ptyID:   pty.ID,
		types:   map[string]bool{EventWindowExit: true, EventWindowClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	for {
		if code, exited := pty.ExitStatus(); exited {
			return code, true
		}
		select {
		case <-sub.ch:
		case <-deadline:
			return 0, false
		case <-d.ctx.Done():
			return 0, false
		}
	}
}
