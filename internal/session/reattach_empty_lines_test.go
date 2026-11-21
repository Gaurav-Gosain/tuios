package session

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tuios.dev/vt"
)

type lockedClient struct {
	mu   sync.Mutex
	term *vt.Emulator
}

func newLockedClient(w, h int) *lockedClient {
	return &lockedClient{term: vt.NewEmulator(w, h)}
}

func (c *lockedClient) Write(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.term.Write(data)
}

func (c *lockedClient) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return emulatorText(c.term)
}

func (c *lockedClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.term.Close()
}

func newEmulatedPTY(t *testing.T, w, h int) *PTY {
	t.Helper()
	p := &PTY{
		ID:           "ptytest-emulated",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: make([]byte, 64*1024),
		vtWriteChan:  make(chan vtChunk, 8),
		ctx:          context.Background(),
		terminal:     vt.NewEmulator(w, h),
	}
	go p.vtWriter()
	return p
}

func (p *PTY) feed(data []byte) int64 {
	p.outputMu.Lock()
	seq := p.appendToBuffer(data)
	p.outputMu.Unlock()
	select {
	case p.vtWriteChan <- vtChunk{data: data, seq: seq}:
	case <-p.ctx.Done():
		return seq
	}
	p.broadcast(ptyChunk{data: data}, seq)
	return seq
}

func (p *PTY) fedThrough(t *testing.T, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p.outputMu.RLock()
		out := p.outputSeq
		p.outputMu.RUnlock()
		p.terminalMu.RLock()
		vt := p.vtSeq
		p.terminalMu.RUnlock()
		if vt >= out && out > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("daemon emulator never consumed the %s", what)
}

func (p *PTY) daemonText() string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return ""
	}
	return terminalText(p.terminal)
}

func terminalText(t vt.Terminal) string {
	var b strings.Builder
	for y := range t.Height() {
		for x := range t.Width() {
			cell := t.CellAt(x, y)
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

const topDraw = "\x1b[?1049h\x1b[H\x1b[2J" +
	"\x1b[1;1Htop - 12:00:00 up 1 day" +
	"\x1b[2;1HTasks: 3 total" +
	"\x1b[3;1H  PID USER     COMMAND" +
	"\x1b[4;1H 1234 seba     top" +
	"\x1b[5;1H 5678 root     sshd" +
	"\x1b[6;1H 9012 seba     bash"

func topRepaint(base int) string {
	return "\x1b[4;1H\x1b[2K" + strconv.Itoa(base) + " seba     top" +
		"\x1b[5;1H\x1b[2K" + strconv.Itoa(base+1) + " root     sshd" +
		"\x1b[6;1H\x1b[2K" + strconv.Itoa(base+2) + " seba     bash"
}

func reattachFrom(p *PTY, clientID string) (*lockedClient, *ptyQueue) {
	state := p.GetTerminalState(0, 0)
	if state == nil {
		return nil, nil
	}
	client := newLockedClient(state.Width, state.Height)
	client.mu.Lock()
	ApplyTerminalState(client.term, state)
	client.mu.Unlock()

	ch := p.SubscribeFromSnapshot(clientID, state.Seq)
	replay := drain(ch)
	if len(replay) > 0 {
		client.Write(replay)
	}
	go func() {
		for chunk, ok := ch.next(); ok; chunk, ok = ch.next() {
			if len(chunk.data) > 0 {
				client.Write(chunk.data)
			}
		}
	}()
	return client, ch
}

// TestReattachWhileTopRedraws is the moving case: the guest repaints its rows
// while the snapshot is taken and the stream resumes, so the catch-up ring
// carries bytes the snapshot does not. The client must converge on the daemon
// with no rows skipped, doubled, or interleaved with blanks.
func TestReattachWhileTopRedraws(t *testing.T) {
	p := newEmulatedPTY(t, 80, 24)
	p.feed([]byte(topDraw))
	p.fedThrough(t, "initial draw")

	for i := range 3 {
		p.feed([]byte(topRepaint(1000 + i*10)))
		time.Sleep(2 * time.Millisecond)
	}

	client, _ := reattachFrom(p, "repro-moving")
	if client == nil {
		t.Fatal("reattach produced no client emulator")
	}
	defer client.Close()
	defer func() { _ = p.Unsubscribe("repro-moving") }()

	for i := range 5 {
		p.feed([]byte(topRepaint(2000 + i*10)))
		time.Sleep(2 * time.Millisecond)
	}
	p.fedThrough(t, "repaints after resume")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(client.Text(), "2042 seba     bash") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	want := normalizeText(p.daemonText())
	got := normalizeText(client.Text())
	if want != got {
		t.Errorf("reattach while top redraws diverged (issue #123):\n--- daemon ---\n%s\n--- client ---\n%s", want, got)
	}
}

// TestReattachThenQuitAltScreen covers the reported "it still shows like this
// even after quitting top": the client reattaches while top runs (alternate
// screen), then the guest quits top. The shell's main screen underneath must
// come back intact.
func TestReattachThenQuitAltScreen(t *testing.T) {
	p := newEmulatedPTY(t, 80, 24)
	p.feed([]byte(topDraw))
	p.fedThrough(t, "initial draw")

	client, ch := reattachFrom(p, "repro-quit")
	if client == nil {
		t.Fatal("reattach produced no client emulator")
	}
	defer client.Close()
	defer func() { _ = p.Unsubscribe("repro-quit") }()
	_ = ch

	p.feed([]byte("\x1b[?1049l$ "))
	p.fedThrough(t, "quit alt screen")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(client.Text(), "$") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	want := normalizeText(p.daemonText())
	got := normalizeText(client.Text())
	if want != got {
		t.Errorf("client after quitting the alt screen diverged (issue #123):\n--- daemon ---\n%s\n--- client ---\n%s", want, got)
	}
}

func normalizeText(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
