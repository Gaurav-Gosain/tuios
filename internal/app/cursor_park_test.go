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

func (r *parkRig) step(cur *tea.Cursor, visible bool) []byte {
	r.t.Helper()
	start := r.log.count()
	r.park.set(cur, visible)
	r.p.Send(parkStep{cur: cur})
	r.waitQuiet()
	return bytes.Join(r.log.since(start), nil)
}

func (r *parkRig) waitQuiet() {
	for {
		select {
		case <-r.log.got:
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

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

			out := r.step(at, false)
			if bytes.Contains(out, seqShowCursor) || r.hostVisibility() != -1 {
				t.Fatalf("first parked frame: %q", out)
			}
			r.step(at, true)
			if out = r.step(at, false); bytes.Contains(out, seqShowCursor) || lastVisibility(out) != -1 {
				t.Fatalf("parked after shown: %q", out)
			}
			for range 3 {
				if out = r.step(at, false); bytes.Contains(out, seqCursorVis) {
					t.Fatalf("parked frame carries a visibility sequence: %q", out)
				}
				if len(out) == 0 {
					t.Fatal("a changed cell wrote nothing")
				}
			}

			if out = r.step(at, true); lastVisibility(out) != 1 {
				t.Fatalf("shown at the same place: %q", out)
			}
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
	if out, _ = p.frame([]byte("\x1b[?2500hC")); string(out) != "\x1b[?2500hC" {
		t.Fatalf("got %q", out)
	}
}
