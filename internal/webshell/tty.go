package webshell

import (
	"fmt"
	"sync"
)

// Event is something a guest did that a guided tour may want to know about,
// such as a command the user ran at the fake shell.
type Event struct {
	Type     string         `json:"type"`
	WindowID string         `json:"windowId,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// The events guests emit.
const (
	// EventCommandStart is a command line starting at the prompt. Data:
	// command, line, cwd.
	EventCommandStart = "shell.start"
	// EventCommand is a command line that finished. Data: command, line, cwd,
	// exitCode (an int).
	EventCommand = "shell.command"
	// EventCwd is the shell changing directory. Data: cwd.
	EventCwd = "shell.cwd"
	// EventAgentReport is the fake agent reporting its state, for tuios and
	// not for the page: the browser build turns it into app.AgentReportMsg.
	// Data: state, message, kind, harness.
	EventAgentReport = "agent.report"
	// EventTapePlay asks tuios to play a tape, from `tuios tape play`. The
	// browser build turns it into app.PlayTapeMsg. Data: name, script.
	EventTapePlay = "tape.play"
)

var (
	eventMu   sync.RWMutex
	eventSink func(Event)
)

// SetEventSink installs the function guests report events to. The wasm entry
// point forwards them to the page.
func SetEventSink(fn func(Event)) {
	eventMu.Lock()
	eventSink = fn
	eventMu.Unlock()
}

func emit(e Event) {
	eventMu.RLock()
	fn := eventSink
	eventMu.RUnlock()
	if fn != nil {
		fn(e)
	}
}

// TTY is what a guest program sees: input as chunks, output, and the size.
type TTY struct {
	pty  *Pty
	env  map[string]string
	In   <-chan []byte
	quit chan struct{}
	once sync.Once
}

func newTTY(p *Pty, env map[string]string) *TTY {
	in := make(chan []byte, 64)
	t := &TTY{pty: p, env: env, In: in, quit: make(chan struct{})}
	go func() {
		defer close(in)
		buf := make([]byte, 4096)
		for {
			n, err := p.in.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				select {
				case in <- chunk:
				case <-t.quit:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return t
}

func (t *TTY) stop() { t.once.Do(func() { close(t.quit) }) }

// Resized fires after the window changes size.
func (t *TTY) Resized() <-chan struct{} { return t.pty.resized }

// Size is the window size in cells.
func (t *TTY) Size() (cols, rows int) {
	c, r, _ := t.pty.Size()
	return c, r
}

// Env reads the environment the window gave the guest.
func (t *TTY) Env(key string) string { return t.env[key] }

// Write sends output to the window.
func (t *TTY) Write(b []byte) (int, error) { return t.pty.out.Write(b) }

// Print writes a string.
func (t *TTY) Print(s string) { _, _ = t.pty.out.Write([]byte(s)) }

// Printf writes a formatted string.
func (t *TTY) Printf(format string, args ...any) { t.Print(fmt.Sprintf(format, args...)) }

// Emit reports an event from this guest's window.
func (t *TTY) Emit(typ string, data map[string]any) {
	emit(Event{Type: typ, WindowID: t.env["TUIOS_WINDOW_ID"], Data: data})
}
