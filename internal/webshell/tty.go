package webshell

import (
	"fmt"
	"strings"
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
	log  screenLog // the main screen, for the shell to draw again on a resize
	laid int       // the width the main screen was last laid out for
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

// Print writes a string.
func (t *TTY) Print(s string) {
	t.log.write(s)
	t.write(s)
}

// Lay prints lines laid out for the pane width, each on a line of its own.
// When the pane changes width, the shell calls lay again to draw them for
// the new one, so lay must print nothing itself and give the same lines for
// the same width.
func (t *TTY) Lay(lay func(cols int) []string) {
	cols, _ := t.Size()
	t.log.layout(lay)
	var b strings.Builder
	for _, l := range lay(cols) {
		b.WriteString(l + "\r\n")
	}
	t.write(b.String())
}

// write writes a string without keeping it in the log, for the line being
// edited, which the shell draws again itself.
func (t *TTY) write(s string) { _, _ = t.pty.out.Write([]byte(s)) }

// keep puts s in the log without writing it: it is on the screen already.
func (t *TTY) keep(s string) { t.log.write(s) }

// repaint draws the main screen again for the pane's width: everything the
// log kept, then tail, which is what the caller is drawing live. Output laid
// out for an old width would otherwise be rewrapped by the terminal and
// break. The scrollback is cleared with the screen because the log holds it
// too.
func (t *TTY) repaint(tail string) {
	// This repaint answers any resize still waiting to be read. The signal
	// is taken before the size is read, so a resize that lands after the
	// read leaves a signal for the next repaint.
	select {
	case <-t.Resized():
	default:
	}
	cols, _ := t.Size()
	t.laid = cols
	t.write("\x1b[H\x1b[2J\x1b[3J" + t.log.replay(cols) + tail)
}

// relaidNeeded reports whether the pane changed width since the main screen
// was last laid out: a full-screen program reads the resize itself, and the
// main screen it returns to is still laid out for the old width.
func (t *TTY) relaidNeeded() bool {
	cols, _ := t.Size()
	return cols != t.laid
}

// keepLay puts a layout in the log without writing it: it is on the screen
// already.
func (t *TTY) keepLay(lay func(cols int) []string) { t.log.layout(lay) }

// Printf writes a formatted string.
func (t *TTY) Printf(format string, args ...any) { t.Print(fmt.Sprintf(format, args...)) }

// Emit reports an event from this guest's window.
func (t *TTY) Emit(typ string, data map[string]any) {
	emit(Event{Type: typ, WindowID: t.env["TUIOS_WINDOW_ID"], Data: data})
}
