package session

import (
	"bytes"
	"os"
	"sync"
	"time"
)

const popupCaptureMax = 1 << 20

const popupDrainWait = time.Second

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

func (c *popupCapture) finish() (string, bool) {
	select {
	case <-c.done:
	case <-time.After(popupDrainWait):
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.truncated
}

type popupExit struct {
	once   sync.Once
	done   chan struct{}
	code   int
	signal int
}

func (e *popupExit) exitSignal() int {
	select {
	case <-e.done:
		return e.signal
	default:
		return 0
	}
}

func newPopupExit() *popupExit {
	return &popupExit{done: make(chan struct{})}
}

func (e *popupExit) record(code int) {
	e.once.Do(func() {
		e.code = code
		close(e.done)
	})
}

func (e *popupExit) recordPTY(pty *PTY) {
	code, ok := pty.ExitStatus()
	if !ok {
		return
	}
	e.once.Do(func() {
		e.code = code
		e.signal = pty.ExitSignal()
		close(e.done)
	})
}

var popupBeforeWaitHook func(sess *Session, win WindowState)

func (d *Daemon) waitPopupExit(sess *Session, win WindowState, timeout time.Duration, kept *popupExit) (int, bool) {
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		if kept == nil {
			return -1, true
		}
		select {
		case <-kept.done:
			return kept.code, true
		case <-deadline:
			return 0, false
		case <-d.ctx.Done():
			return 0, false
		}
	}
	sub := d.events.subscribe(eventFilter{
		session: sess.Name(),
		sess:    sess,
		ptyID:   pty.ID,
		types:   map[string]bool{EventWindowExit: true, EventWindowClosed: true},
	}, defaultEventQueue)
	defer d.events.unsubscribe(sub)
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
