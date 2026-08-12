package session

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A pane the client hides and shows again resubscribes to the same PTY. The
// catch-up buffer is there for a client that has never seen the pane; replaying
// it to a client that already holds the pane's screen paints the pane's whole
// history a second time, below the paint already there. That is what a
// workspace switch did to every pane it brought back.

const fishBanner = "Welcome to fish, the friendly interactive shell\r\n$ "

// newBufferedPTY returns a PTY holding output as if a shell had already printed
// it, with no reader goroutines to race the test.
func newBufferedPTY(t *testing.T) *PTY {
	t.Helper()
	p := &PTY{
		ID:           "ptytest-00000002",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: make([]byte, 64*1024),
	}
	p.appendAndBroadcast([]byte(fishBanner))
	return p
}

// appendAndBroadcast mirrors what readOutput does with one chunk of PTY output.
func (p *PTY) appendAndBroadcast(data []byte) {
	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	p.broadcast(data, seq)
}

// drain collects everything queued on a subscriber channel without blocking.
func drain(ch <-chan []byte) []byte {
	var out []byte
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, data...)
		default:
			return out
		}
	}
}

func TestFirstSubscriberGetsTheCatchUpBuffer(t *testing.T) {
	p := newBufferedPTY(t)

	got := drain(p.Subscribe("client-1", 0))
	if !bytes.Equal(got, []byte(fishBanner)) {
		t.Errorf("first subscriber got %q, want the buffered output %q", got, fishBanner)
	}
}

func TestResubscribeReplaysOnlyWhatWasMissed(t *testing.T) {
	p := newBufferedPTY(t)

	ch := p.Subscribe("client-1", 0)
	drain(ch)
	resume := p.Unsubscribe("client-1")

	// Nothing happened while the pane was hidden, so showing it again must
	// deliver nothing at all.
	got := drain(p.Subscribe("client-1", resume))
	if len(got) != 0 {
		t.Errorf("idle resubscribe replayed %q, want nothing", got)
	}
	resume = p.Unsubscribe("client-1")

	// The pane produced output while hidden: exactly that output, once.
	p.appendAndBroadcast([]byte("hidden output\r\n"))
	got = drain(p.Subscribe("client-1", resume))
	if string(got) != "hidden output\r\n" {
		t.Errorf("resubscribe replayed %q, want only the output missed while hidden", got)
	}
}

// TestResubscribeKeepsTheGuestScreenIntact drives the guest's own view: the
// bytes a client receives across a hide/show cycle, fed to the emulator that
// already rendered them, must leave one banner and one prompt on screen.
func TestResubscribeKeepsTheGuestScreenIntact(t *testing.T) {
	p := newBufferedPTY(t)

	term := vt.NewEmulator(80, 24)
	ch := p.Subscribe("client-1", 0)
	_, _ = term.Write(drain(ch))
	before := emulatorText(term)

	for range 3 {
		resume := p.Unsubscribe("client-1")
		ch = p.Subscribe("client-1", resume)
		_, _ = term.Write(drain(ch))
	}

	if n := strings.Count(emulatorText(term), "Welcome to fish"); n != 1 {
		t.Errorf("guest screen shows the banner %d times after three hide/show cycles, want 1", n)
	}
	if got := emulatorText(term); got != before {
		t.Errorf("guest screen changed across hide/show cycles:\nbefore:\n%s\nafter:\n%s", before, got)
	}
}

// TestResubscribeFallsBackWhenTheBufferRolled covers a pane that outran the
// catch-up buffer while hidden: the client cannot be resumed byte-exactly, so
// it gets everything the buffer still holds rather than a silent gap.
func TestResubscribeFallsBackWhenTheBufferRolled(t *testing.T) {
	p := newBufferedPTY(t)

	ch := p.Subscribe("client-1", 0)
	drain(ch)
	resume := p.Unsubscribe("client-1")

	p.appendAndBroadcast(bytes.Repeat([]byte("x"), 96*1024))

	got := drain(p.Subscribe("client-1", resume))
	if len(got) != 64*1024 {
		t.Errorf("rolled-buffer resubscribe replayed %d bytes, want the whole %d-byte buffer", len(got), 64*1024)
	}
}

// emulatorText reads an emulator's visible grid as text.
func emulatorText(term *vt.Emulator) string {
	var b strings.Builder
	for y := range term.Height() {
		for x := range term.Width() {
			cell := term.CellAt(x, y)
			if cell == nil || cell.String() == "" {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(cell.String())
		}
		b.WriteByte('\n')
	}
	return b.String()
}
