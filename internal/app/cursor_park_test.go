package app

import (
	"bytes"
	"strconv"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The wire contract of the parked cursor (#649), on frames Bubble Tea's own
// renderer writes. A parked frame must never carry a show: the host would
// draw the cursor at the caret for as long as the terminal takes to read the
// rest. The hide must be on the wire whenever the host could be showing it,
// and the show must come back when the guest shows its cursor again, the
// exit included.
//
// Ways it could fail, each checked below:
//   - the renderer's show (?25h, at the end of every frame without mode 2026)
//     reaches the host in a parked frame;
//   - the first parked frame leaves the host's initial visible cursor on;
//   - parked to shown at the same place writes no show, since the renderer
//     thinks the cursor never went away and writes no frame;
//   - parked to shown at a new place shows the cursor before the frame
//     moves it (an early write from View);
//   - the frame that leaves the alternate screen leaves the cursor hidden
//     for the shell that ran tuios.

// parkModel is a client whose view changes one cell per update and hands the
// renderer the cursor in cur.
type parkModel struct {
	n   int
	cur *tea.Cursor
}

type parkStep struct{ cur *tea.Cursor }

func (m *parkModel) Init() tea.Cmd { return nil }

func (m *parkModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := msg.(parkStep); ok {
		m.n++
		m.cur = s.cur
	}
	return m, nil
}

func (m *parkModel) View() tea.View {
	v := tea.NewView("count " + strconv.Itoa(m.n%10) + "\nline two")
	v.AltScreen = true
	v.Cursor = m.cur
	return v
}

// frameLog keeps each write the renderer makes, as the host gets it.
type frameLog struct {
	mu     sync.Mutex
	writes [][]byte
	got    chan struct{}
}

func (l *frameLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.writes = append(l.writes, bytes.Clone(p))
	l.mu.Unlock()
	select {
	case l.got <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (l *frameLog) since(i int) [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]byte(nil), l.writes[i:]...)
}

func (l *frameLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.writes)
}

// parkRig runs a real Bubble Tea program whose writes go through the frame
// hook a served client gets, the same as tuios-web and the SSH server.
type parkRig struct {
	t    *testing.T
	p    *tea.Program
	park *cursorPark
	log  *frameLog
	done chan struct{}
}

func newParkRig(t *testing.T, syncd bool) *parkRig {
	t.Helper()
	log := &frameLog{got: make(chan struct{}, 1)}
	w := NewFrameHookWriter(log)
	park := &cursorPark{shown: true, out: w.WriteOutside}
	w.SetHook(func(frame []byte) ([]byte, []byte) { return park.frame(frame) })
	p := tea.NewProgram(&parkModel{}, tea.WithOutput(w), tea.WithInput(nil),
		tea.WithWindowSize(20, 4), tea.WithoutSignalHandler(),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	r := &parkRig{t: t, p: p, park: park, log: log, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		_, _ = p.Run()
	}()
	if syncd {
		p.Send(tea.ModeReportMsg{Mode: ansi.ModeSynchronizedOutput, Value: ansi.ModeReset})
	}
	r.waitQuiet()
	return r
}

// step changes one cell, hands the renderer cur, tells the park what the
// view wants as View does, and returns what reached the host.
func (r *parkRig) step(cur *tea.Cursor, visible bool) []byte {
	r.t.Helper()
	start := r.log.count()
	r.park.set(cur, visible)
	r.p.Send(parkStep{cur: cur})
	r.waitQuiet()
	return bytes.Join(r.log.since(start), nil)
}

// waitQuiet waits for writes to stop for longer than a frame.
func (r *parkRig) waitQuiet() {
	for {
		select {
		case <-r.log.got:
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

// hostVisibility is the host's cursor visibility after every write so far.
func (r *parkRig) hostVisibility() int {
	return lastVisibility(bytes.Join(r.log.since(0), nil))
}

func (r *parkRig) quit() []byte {
	start := r.log.count()
	r.p.Quit()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		r.t.Fatal("the program did not quit")
	}
	return bytes.Join(r.log.since(start), nil)
}

// lastVisibility is the host's cursor visibility after out: +1 shown, -1
// hidden, 0 when out does not say.
func lastVisibility(out []byte) int {
	on, off := bytes.LastIndex(out, seqShowCursor), bytes.LastIndex(out, seqHideCursor)
	switch {
	case on > off:
		return 1
	case off > on:
		return -1
	}
	return 0
}

func TestParkedFrameWire(t *testing.T) {
	for _, syncd := range []bool{false, true} {
		name := "no-2026"
		if syncd {
			name = "2026"
		}
		t.Run(name, func(t *testing.T) {
			r := newParkRig(t, syncd)
			at := tea.NewCursor(3, 1)

			// The first parked frame, after a first frame with no cursor:
			// no show, and the host's cursor is off.
			out := r.step(at, false)
			if bytes.Contains(out, seqShowCursor) || r.hostVisibility() != -1 {
				t.Fatalf("first parked frame: %q", out)
			}
			// Shown, then parked at the same place: the hide is written
			// at once, since no frame moves the cursor.
			r.step(at, true)
			if out = r.step(at, false); bytes.Contains(out, seqShowCursor) || lastVisibility(out) != -1 {
				t.Fatalf("parked after shown: %q", out)
			}
			// Steady parked frames carry no visibility at all.
			for range 3 {
				if out = r.step(at, false); bytes.Contains(out, seqCursorVis) {
					t.Fatalf("parked frame carries a visibility sequence: %q", out)
				}
				if len(out) == 0 {
					t.Fatal("a changed cell wrote nothing")
				}
			}

			// Shown again at the same place: the renderer has nothing to
			// say about visibility, so the show comes from the park.
			if out = r.step(at, true); lastVisibility(out) != 1 {
				t.Fatalf("shown at the same place: %q", out)
			}
			// Parked again, then shown at a new place: the show must come
			// after the move, never before it.
			r.step(at, false)
			moved := tea.NewCursor(6, 2)
			out = r.step(moved, true)
			move := bytes.Index(out, []byte(ansi.CursorPosition(7, 3)))
			show := bytes.Index(out, seqShowCursor)
			if show < 0 || lastVisibility(out) != 1 {
				t.Fatalf("shown at a new place: %q", out)
			}
			if move < 0 || show < move {
				t.Fatalf("the show is not after the move to the new place: %q", out)
			}

			// Leaving while parked: the shell gets its cursor back.
			r.step(at, false)
			if out = r.quit(); lastVisibility(out) != 1 {
				t.Fatalf("exit while parked leaves the cursor hidden: %q", out)
			}
		})
	}
}

// TestParkedStripSequences checks the edit on its own, on the bytes the
// renderer writes without mode 2026.
func TestParkedStripSequences(t *testing.T) {
	p := &cursorPark{shown: false, hidePark: true}
	frame := []byte("\x1b[?25lA\x1b[2;3H\x1b[?25h")
	out, after := p.frame(frame)
	if string(out) != "A\x1b[2;3H" || after != nil {
		t.Fatalf("got %q after %q", out, after)
	}
	if string(frame) != "\x1b[?25lA\x1b[2;3H\x1b[?25h" {
		t.Fatalf("the renderer's buffer was changed: %q", frame)
	}
	p.shown = true
	if out, _ = p.frame([]byte("B\x1b[?25h")); string(out) != "\x1b[?25lB" {
		t.Fatalf("host showing: got %q", out)
	}
	// Other private modes that start the same way pass through.
	if out, _ = p.frame([]byte("\x1b[?2500hC")); string(out) != "\x1b[?2500hC" {
		t.Fatalf("got %q", out)
	}
}
