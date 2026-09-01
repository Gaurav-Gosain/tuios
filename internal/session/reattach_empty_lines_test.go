package session

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// These tests reproduce the reported shape of issue #123: after detach and
// attach, a full-screen program like top comes back with empty lines
// interleaved between its output lines.
//
// The daemon side is a PTY whose emulator is fed directly (no shell in the
// middle), so the byte stream, the catch-up ring and the daemon emulator move
// together exactly as they do under a real PTY. The client side is rebuilt the
// way restoreTerminalContent rebuilds it: a fresh emulator gets the daemon's
// snapshot (ApplyTerminalState) and then the stream resumes from the position
// the snapshot ends at. The client's screen must converge on the daemon's.

// lockedClient wraps a client emulator so the live-stream goroutine that
// writes it and the test body that reads it cannot race. The race detector
// is on in the project's CI, and a goroutine writing an emulator the test
// reads through emulatorText without a lock is exactly the report it makes.
type lockedClient struct {
	mu   sync.Mutex
	term *vt.Emulator
}

func newLockedClient(w, h int) *lockedClient {
	return &lockedClient{term: vt.NewEmulator(w, h)}
}

// Write applies stream bytes under the lock.
func (c *lockedClient) Write(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.term.Write(data)
}

// Text renders the visible grid under the lock.
func (c *lockedClient) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return emulatorText(c.term)
}

// Close closes the emulator.
func (c *lockedClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.term.Close()
}

// newEmulatedPTY builds a PTY with a live emulator and its vtWriter goroutine,
// but no real shell: tests feed it bytes directly. The vtWriteChan is small so
// the emulator can be pushed behind the ring with a test-sized burst; the real
// daemon's channel is 256 x 16KB chunks, which can hold 4MB of unprocessed
// output.
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

// feed writes guest output through the same two paths the read loop uses: the
// catch-up ring (so a resubscribing client is handed it) and the emulator (so
// the daemon's screen advances). Like readOutput it blocks on the emulator
// channel rather than dropping a chunk. It returns the stream position the
// chunk ends at.
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

// fedThrough waits until the daemon emulator has consumed every byte fed so
// far, i.e. vtSeq catches outputSeq.
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

// daemonText renders the daemon emulator's visible grid exactly the way
// emulatorText renders a client emulator, so the two sides compare cell for
// cell.
func (p *PTY) daemonText() string {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return ""
	}
	return terminalText(p.terminal)
}

// terminalText reads any terminal's visible grid as text, one line per row.
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

// topDraw is the initial full-screen draw a program like top makes: enter the
// alternate screen, clear it, paint three header rows and three process rows.
const topDraw = "\x1b[?1049h\x1b[H\x1b[2J" +
	"\x1b[1;1Htop - 12:00:00 up 1 day" +
	"\x1b[2;1HTasks: 3 total" +
	"\x1b[3;1H  PID USER     COMMAND" +
	"\x1b[4;1H 1234 seba     top" +
	"\x1b[5;1H 5678 root     sshd" +
	"\x1b[6;1H 9012 seba     bash"

// topRepaint repaints the three process rows with fresh PIDs, the way top's
// periodic update does (cursor to row, erase line, write row).
func topRepaint(base int) string {
	return "\x1b[4;1H\x1b[2K" + strconv.Itoa(base) + " seba     top" +
		"\x1b[5;1H\x1b[2K" + strconv.Itoa(base+1) + " root     sshd" +
		"\x1b[6;1H\x1b[2K" + strconv.Itoa(base+2) + " seba     bash"
}

// reattach rebuilds a client emulator from the daemon's snapshot, resumes the
// stream from the snapshot's position, and keeps applying the live stream to
// the client emulator, exactly what restoreTerminalContent plus the subscribe
// handler (WriteOutputAsync) do. It returns the locked client and the
// subscription channel; the caller owns closing the client and unsubscribing.
func reattachFrom(p *PTY, clientID string) (*lockedClient, <-chan ptyChunk) {
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
	// The live stream: every chunk the daemon broadcasts after the replay is
	// applied to the client, as the client's output handler does. All writes
	// go through the lock the test reads under.
	go func() {
		for chunk := range ch {
			if len(chunk.data) > 0 {
				client.Write(chunk.data)
			}
		}
	}()
	return client, ch
}

// TestReattachAfterTopDrewOnce is the quiescent case: the guest drew its
// screen and went quiet before the client left, so the attach has nothing to
// replay. The client must reproduce the daemon's screen exactly.
func TestReattachAfterTopDrewOnce(t *testing.T) {
	p := newEmulatedPTY(t, 80, 24)
	p.feed([]byte(topDraw))
	p.fedThrough(t, "initial draw")

	client, ch := reattachFrom(p, "repro-quiet")
	if client == nil {
		t.Fatal("reattach produced no client emulator")
	}
	defer client.Close()
	defer func() { _ = p.Unsubscribe("repro-quiet") }()
	_ = ch

	want := normalizeText(p.daemonText())
	got := normalizeText(client.Text())
	if want != got {
		t.Errorf("quiet reattach diverged (issue #123):\n--- daemon ---\n%s\n--- client ---\n%s", want, got)
	}
}

// TestReattachWhileTopRedraws is the moving case: the guest repaints its rows
// while the snapshot is taken and the stream resumes, so the catch-up ring
// carries bytes the snapshot does not. The client must converge on the daemon
// with no rows skipped, doubled, or interleaved with blanks.
func TestReattachWhileTopRedraws(t *testing.T) {
	p := newEmulatedPTY(t, 80, 24)
	p.feed([]byte(topDraw))
	p.fedThrough(t, "initial draw")

	// The guest keeps repainting: a few redraws land before the snapshot, and
	// a few more while the client is being rebuilt.
	for i := 0; i < 3; i++ {
		p.feed([]byte(topRepaint(1000 + i*10)))
		time.Sleep(2 * time.Millisecond)
	}

	client, _ := reattachFrom(p, "repro-moving")
	if client == nil {
		t.Fatal("reattach produced no client emulator")
	}
	defer client.Close()
	defer func() { _ = p.Unsubscribe("repro-moving") }()

	// More repaints after the resume, then let everything settle.
	for i := 0; i < 5; i++ {
		p.feed([]byte(topRepaint(2000 + i*10)))
		time.Sleep(2 * time.Millisecond)
	}
	p.fedThrough(t, "repaints after resume")

	// The live stream applies asynchronously: wait until the client has caught
	// up to the daemon's latest repaint before comparing.
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

// TestReattachSnapshotWhileEmulatorBehind takes the snapshot while the daemon
// emulator is still chewing on a large batch of output, so vtSeq trails
// outputSeq and the catch-up ring has rolled past the snapshot's position. The
// client must lay the snapshot down and then replay the tail, converging on
// the daemon with no rows skipped, doubled, or blanked.
func TestReattachSnapshotWhileEmulatorBehind(t *testing.T) {
	p := newEmulatedPTY(t, 80, 24)
	p.feed([]byte(topDraw))
	p.fedThrough(t, "initial draw")

	// A goroutine feeds a burst of repaints batched into 16KB chunks, exactly
	// as readOutput reads from the PTY pipe. The small vtWriteChan lets the
	// emulator fall behind while the ring keeps rolling. Each chunk is copied
	// out of the scratch buffer before it is handed to feed, because feed's
	// emulator and subscriber paths hold the slice by reference: reusing the
	// backing array for the next chunk would race the vtWriter and the client
	// stream goroutines against this one.
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		scratch := make([]byte, 0, 16*1024)
		send := func() {
			data := make([]byte, len(scratch))
			copy(data, scratch)
			p.feed(data)
			scratch = scratch[:0]
		}
		for i := 0; i < 20000; i++ {
			scratch = append(scratch, []byte(topRepaint(10000+i%3000))...)
			if len(scratch) >= 16*1024 {
				send()
			}
		}
		if len(scratch) > 0 {
			send()
		}
	}()

	// Wait until the ring has rolled past where the emulator has got to: the
	// condition the bug lived in.
	waitFor := func(desc string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", desc)
	}
	waitFor("the ring to roll past the emulator", func() bool {
		p.outputMu.RLock()
		out := p.outputSeq
		bufStart := out - int64(p.outputPos)
		p.outputMu.RUnlock()
		p.terminalMu.RLock()
		vt := p.vtSeq
		p.terminalMu.RUnlock()
		return out > 70*1024 && vt < bufStart
	})

	// Snapshot now: vtSeq is behind and the ring no longer holds fromSeq.
	state := p.GetTerminalState(0, 0)
	if state == nil {
		t.Fatal("GetTerminalState returned nil")
	}

	client := newLockedClient(state.Width, state.Height)
	client.mu.Lock()
	ApplyTerminalState(client.term, state)
	client.mu.Unlock()

	ch := p.SubscribeFromSnapshot("repro-behind", state.Seq)
	replay := drain(ch)
	if len(replay) > 0 {
		client.Write(replay)
	}
	go func() {
		for chunk := range ch {
			if len(chunk.data) > 0 {
				client.Write(chunk.data)
			}
		}
	}()
	defer client.Close()
	defer func() { _ = p.Unsubscribe("repro-behind") }()

	// Let the burst finish and both sides settle on the final repaint.
	<-feedDone
	p.fedThrough(t, "burst after reattach")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(client.Text(), "11999 seba     top") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	want := normalizeText(p.daemonText())
	got := normalizeText(client.Text())
	if want != got {
		t.Errorf("reattach with emulator behind diverged (issue #123):\n--- daemon ---\n%s\n--- client ---\n%s", want, got)
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

	// The guest quits top: leave the alternate screen.
	p.feed([]byte("\x1b[?1049l$ ")) // as a shell prompt would after top exits
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

// normalizeText trims trailing whitespace per line and drops trailing empty
// lines, so two renderings of the same screen compare equal regardless of how
// each path pads a blank cell.
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
