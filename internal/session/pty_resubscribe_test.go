package session

import (
	"strings"
	"testing"

	"tuios.dev/vt"
)

const fishBanner = "Welcome to fish, the friendly interactive shell\r\n$ "

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

func (p *PTY) appendAndBroadcast(data []byte) {
	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	p.broadcast(ptyChunk{data: data}, seq)
}

func drain(ch *ptyQueue) []byte {
	var out []byte
	for {
		chunk, ok, _ := ch.pop()
		if !ok {
			return out
		}
		out = append(out, chunk.data...)
	}
}

// TestRolledBufferRepaintsInsteadOfSplicing is the regression test for the
// artifacts a workspace switch left behind.
//
// A pane that produced more than the catch-up buffer holds while it was hidden
// cannot be resumed: the bytes between where the client stopped and where the
// buffer now starts are gone. Handing it the tail alone appends the second half
// of the stream to a screen drawn from the first, so the guest's output lands
// against cursor positions and modes set by bytes that never arrived, and the
// pane comes back showing text from two different moments at once.
//
// The assertion is on the guest's screen, because the byte count was already
// right while the screen was wrong.
func TestRolledBufferRepaintsInsteadOfSplicing(t *testing.T) {
	p := newBufferedPTY(t)

	term := vt.NewEmulator(80, 24)
	_, _ = term.Write(drain(p.Subscribe("client-1", 0)))
	if !strings.Contains(emulatorText(term), "Welcome to fish") {
		t.Fatal("the fixture did not put the banner on the client's screen")
	}
	resume := p.Unsubscribe("client-1")

	p.appendAndBroadcast([]byte(strings.Repeat("\x1b[11;1Hsecond moment\r", 6000)))

	_, _ = term.Write(drain(p.Subscribe("client-1", resume)))

	screen := emulatorText(term)
	if !strings.Contains(screen, "second moment") {
		t.Errorf("the pane came back without the output it produced while hidden:\n%s", screen)
	}
	if strings.Contains(screen, "Welcome to fish") {
		t.Errorf("the pane came back showing what it held before the gap as well as after it:\n%s", screen)
	}
}

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
