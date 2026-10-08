package session

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The limits in frame_budget.go are a security boundary: any process that can
// reach the daemon's socket can send a frame header, and before them a header
// alone made the daemon allocate the 16 MiB it announced. This test holds the
// frame reader, the read budget, the per-pane input bound and the connection
// caps to each limit.
//
// The ways this could fail, written down before the code:
//
//  1. The body is still allocated from the header: a frame that announces
//     16 MiB and sends 1 KiB costs megabytes. Measured with the allocator's
//     own count, which sees an allocation whether or not it is touched.
//  2. The buffer grows but loses or reorders bytes at a chunk edge.
//  3. A short request type still takes 16 MiB: a hello over the short limit
//     must be refused unread, with the stream in step after it.
//  4. Large frames starve each other: each holds part of the budget while it
//     waits for the rest, so several at once all time out where one after
//     another would all succeed. Six concurrent 16 MiB frames must all be
//     read.
//  5. A refused frame holds budget while its body is skipped, which can take
//     the whole body deadline.
//  6. A frame small enough to be every keystroke is charged, so a spent
//     budget would stop typing.
//  7. A paste into a pane that does not read holds the budget for as long as
//     the pane does not read, or a second paste to that pane waits too, with
//     its memory, and nobody is told. The same for a large send-text, whose
//     request line holds the verb budget while the verb runs.
//  8. Input typed while a refused paste waits for its retry overtakes it:
//     a paste and then Enter must reach the pane in that order.
//  9. The connection caps are off by one, a refused connection is counted
//     and never given back, the link sockets share the main socket's slots,
//     or a refused client is not told why.

// budgetWhole reports whether none of b is taken.
func budgetWhole(b *memBudget) bool {
	if !b.sem.TryAcquire(b.size) {
		return false
	}
	b.sem.Release(b.size)
	return true
}

// rawFrameHeader is the length prefix, type and codec of an untagged frame
// with a payload of n bytes.
func rawFrameHeader(t MessageType, n int) []byte {
	h := make([]byte, 6)
	binary.BigEndian.PutUint32(h, uint32(2+n))
	h[4], h[5] = byte(t), wireCodecGob
	return h
}

// readOne reads one frame from r the way the client does, with the daemon's
// type limits.
func readOne(r io.Reader) (*Message, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	return readMessageBody(r, n, daemonFrameLimit)
}

func TestFrameBodyGrowsWithWhatArrives(t *testing.T) {
	t.Run("a header that announces 16 MiB costs what was sent", func(t *testing.T) {
		const sent = 1 << 10
		frame := append(rawFrameHeader(MsgInput, maxFrameBytes-2), make([]byte, sent)...)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, err := readOne(bytes.NewReader(frame))
		runtime.ReadMemStats(&after)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("a cut frame read as %v, want an unexpected EOF", err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 64<<10 {
			t.Fatalf("reading 1 KiB of a frame that announced 16 MiB allocated %d bytes", got)
		}
	})

	t.Run("a large frame reads back byte for byte at every chunk edge", func(t *testing.T) {
		sizes := []int{0, 1, firstFrameChunk - 1, firstFrameChunk, firstFrameChunk + 1,
			largeFrame - 1, largeFrame, largeFrame + 1, frameGrowStep + 1, 3*frameGrowStep + 7, maxFrameBytes - 2}
		for _, n := range sizes {
			p := make([]byte, n)
			for i := range p {
				p[i] = byte(i*7 + i>>13)
			}
			var buf bytes.Buffer
			if err := WriteMessage(&buf, &Message{Type: MsgInput, Payload: p}); err != nil {
				t.Fatal(err)
			}
			msg, err := readOne(&buf)
			if err != nil {
				t.Fatalf("%d bytes: %v", n, err)
			}
			if !bytes.Equal(msg.Payload, p) {
				t.Fatalf("%d bytes read back different", n)
			}
		}
	})
}

func TestFrameLimitRefusesShortRequestsUnread(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(rawFrameHeader(MsgHello, maxRequestFrame))
	buf.Write(make([]byte, maxRequestFrame))
	if err := WriteMessage(&buf, &Message{Type: MsgList}); err != nil {
		t.Fatal(err)
	}
	_, err := readOne(&buf)
	if _, ok := errors.AsType[*FrameTooLargeError](err); !ok {
		t.Fatalf("a hello of %d bytes read as %v, want a FrameTooLargeError", maxRequestFrame+2, err)
	}
	next, err := readOne(&buf)
	if err != nil || next.Type != MsgList {
		t.Fatalf("the frame after the refused hello read as %+v, %v", next, err)
	}
}

// frameReader is one connection's read side for readClientFrame, and the
// other end of it to write frames into.
type frameReader struct {
	cs     *connState
	br     *bufio.Reader
	client net.Conn
}

func newFrameReader(t *testing.T) frameReader {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return frameReader{cs: &connState{conn: server}, br: bufio.NewReaderSize(server, connReadBuffer), client: client}
}

// testFrameDaemon is a daemon with nothing started, for readClientFrame.
func testFrameDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := NewDaemon(&DaemonConfig{})
	t.Cleanup(d.manager.Shutdown)
	return d
}

func TestFrameBudgetReadsLargeFramesInTurn(t *testing.T) {
	t.Run("six concurrent 16 MiB frames are all read", func(t *testing.T) {
		d := testFrameDaemon(t)
		// A command frame is charged twice its size, so two fit the budget at
		// once and the other four wait their turn.
		payload := make([]byte, maxFrameBytes-2)
		var frame bytes.Buffer
		if err := WriteMessage(&frame, &Message{Type: MsgExecuteCommand, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 6)
		for range 6 {
			fr := newFrameReader(t)
			go func() { _, _ = fr.client.Write(frame.Bytes()) }()
			wg.Go(func() {
				msg, release, err := d.readClientFrame(fr.cs, fr.br)
				if err != nil {
					errs <- err
					return
				}
				// The handler's time, with the budget held.
				time.Sleep(100 * time.Millisecond)
				release()
				if len(msg.Payload) != len(payload) {
					errs <- errors.New("short payload")
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("a large frame was not read: %v", err)
		}
	})

	t.Run("a refused frame holds no budget while its body is skipped", func(t *testing.T) {
		d := testFrameDaemon(t)
		fr := newFrameReader(t)
		budget := d.readBudgetFor(fr.cs)
		release, ok := budget.acquire(personBudgetBytes-(1<<20), time.Second)
		if !ok {
			t.Fatal("could not spend the budget")
		}
		// The sender sends the header and part of the body, then stalls, so
		// the skip waits for the rest.
		go func() {
			_, _ = fr.client.Write(rawFrameHeader(MsgInput, 8<<20))
			_, _ = fr.client.Write(make([]byte, 1<<20))
		}()
		done := make(chan error, 1)
		go func() {
			_, _, err := d.readClientFrame(fr.cs, fr.br)
			done <- err
		}()
		// Past the budget wait, the frame is refused and its body is being
		// skipped. All of the budget must be free.
		time.Sleep(readBudgetWait + 300*time.Millisecond)
		release()
		all, ok := budget.acquire(personBudgetBytes, 100*time.Millisecond)
		if !ok {
			t.Fatal("the budget is not whole while a refused frame is skipped")
		}
		all()
		_ = fr.client.Close()
		<-done
	})

	t.Run("a keystroke frame is never charged", func(t *testing.T) {
		d := testFrameDaemon(t)
		fr := newFrameReader(t)
		release, ok := d.readBudgetFor(fr.cs).acquire(personBudgetBytes, time.Second)
		if !ok {
			t.Fatal("could not spend the budget")
		}
		defer release()
		go func() {
			_ = WritePTYInput(fr.client, "00000000-0000-0000-0000-000000000000", make([]byte, largeFrame-64))
		}()
		start := time.Now()
		if _, done, err := d.readClientFrame(fr.cs, fr.br); err != nil {
			t.Fatalf("a small input frame on a spent budget: %v", err)
		} else {
			done()
		}
		if time.Since(start) > readBudgetWait/2 {
			t.Fatal("a small input frame waited for the budget")
		}
	})

	t.Run("a link draws on its own budget", func(t *testing.T) {
		d := testFrameDaemon(t)
		person := d.readBudgetFor(&connState{})
		if link := d.readBudgetFor(&connState{viaLink: true}); link == person {
			t.Fatal("a link draws on the person's budget")
		}
		if human := d.readBudgetFor(&connState{viaLink: true, linkHuman: true}); human != person {
			t.Fatal("the person working through a hub does not draw on the person's budget")
		}
		if hosted := d.readBudgetFor(&connState{paneOnly: true}); hosted == person {
			t.Fatal("a hosted pane call draws on the person's budget")
		}
	})
}

// TestPasteIntoAPaneThatDoesNotRead pastes 8 MiB into a pane whose program
// never reads its input, so the write to the pane blocks. The read budget
// must be whole while it does. A second paste to the same pane must be
// refused, sent again once, refused again and reported to the client's
// handler.
func TestPasteIntoAPaneThatDoesNotRead(t *testing.T) {
	d, sock := startTestDaemon(t)
	sess, err := d.manager.CreateSession("paste", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	pty, err := sess.createPTY(80, 24, ptySpawn{windowID: "w-paste", command: []string{"sleep", "600"}})
	if err != nil {
		t.Fatal(err)
	}
	// Whole lines, so the pane's input queue fills and the write blocks.
	paste := bytes.Repeat([]byte(strings.Repeat("a", 79)+"\n"), (8<<20)/80)

	first := attachTUI(t, sock, "paste")
	if err := first.WritePTY(pty.ID, paste); err != nil {
		t.Fatalf("the first paste: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !pty.largeWriteWaiting() {
		if time.Now().After(deadline) {
			t.Fatal("the first paste never reached the pane")
		}
		time.Sleep(20 * time.Millisecond)
	}
	all, ok := d.readBudgetFor(&connState{}).acquire(personBudgetBytes, 200*time.Millisecond)
	if !ok {
		t.Fatal("a paste blocked on a pane holds the read budget")
	}
	all()

	second := attachTUI(t, sock, "paste")
	type refusal struct{ ptyID, message string }
	refused := make(chan refusal, 1)
	second.OnPasteRefused(func(ptyID, message string) { refused <- refusal{ptyID, message} })
	second.StartReadLoop()
	start := time.Now()
	if err := second.WritePTY(pty.ID, paste); err != nil {
		t.Fatalf("the second paste: %v", err)
	}
	select {
	case r := <-refused:
		if r.ptyID != pty.ID || r.message != PasteRefusedBusy {
			t.Fatalf("the refusal names pane %s with %q, want %s with %q", r.ptyID, r.message, pty.ID, PasteRefusedBusy)
		}
		if waited := time.Since(start); waited < pasteRetryDelay {
			t.Fatalf("the refusal came after %v, before the paste was sent again", waited)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a second paste to a pane that does not read was never refused")
	}
}

func TestConnectionCaps(t *testing.T) {
	d := &Daemon{}
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	pipe := func() (net.Conn, net.Conn) {
		a, b := net.Pipe()
		conns = append(conns, a, b)
		return a, b
	}
	for i := range maxConnections {
		a, _ := pipe()
		if !d.admitConnection(a, &d.openConns, maxConnections) {
			t.Fatalf("connection %d of %d was refused", i+1, maxConnections)
		}
	}

	t.Run("a refused binary client gets an error frame", func(t *testing.T) {
		a, b := pipe()
		if d.admitConnection(a, &d.openConns, maxConnections) {
			t.Fatalf("connection %d was admitted over the cap", maxConnections+1)
		}
		_ = b.SetReadDeadline(time.Now().Add(5 * time.Second))
		msg, err := ReadMessage(b)
		if err != nil || msg.Type != MsgError {
			t.Fatalf("a refused connection read %+v, %v", msg, err)
		}
		var p ErrorPayload
		_ = msg.ParsePayload(&p)
		if p.Code != ErrCodeBusy || p.Message != errTooManyConnections {
			t.Fatalf("the refusal says %d %q", p.Code, p.Message)
		}
		if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("a refused connection is not closed: %v", err)
		}
	})

	t.Run("a refused JSON client gets an error line", func(t *testing.T) {
		a, b := pipe()
		if d.admitConnection(a, &d.openConns, maxConnections) {
			t.Fatal("a connection was admitted over the cap")
		}
		_ = b.SetDeadline(time.Now().Add(5 * time.Second))
		if _, err := b.Write([]byte("{")); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(b).ReadBytes('\n')
		if err != nil {
			t.Fatalf("read the refusal: %v", err)
		}
		var resp verbResponse
		if err := json.Unmarshal(line, &resp); err != nil || resp.Error == nil || resp.Error.Code != ErrVerbTooManyConnections {
			t.Fatalf("the refusal line is %q", line)
		}
	})

	if got := d.openConns.Load(); got != maxConnections {
		t.Fatalf("%d connections counted after refusals, want %d", got, maxConnections)
	}

	t.Run("the link sockets have slots of their own", func(t *testing.T) {
		for i := range maxLinkConnections {
			a, _ := pipe()
			if !d.admitConnection(a, &d.openLinkConns, maxLinkConnections) {
				t.Fatalf("link connection %d of %d was refused with the main socket full", i+1, maxLinkConnections)
			}
		}
		a, _ := pipe()
		if d.admitConnection(a, &d.openLinkConns, maxLinkConnections) {
			t.Fatalf("link connection %d was admitted over the cap", maxLinkConnections+1)
		}
	})

	d.openConns.Add(-1)
	a, _ := pipe()
	if !d.admitConnection(a, &d.openConns, maxConnections) {
		t.Fatal("a connection was refused after another one ended")
	}
}

// TestSendTextIntoPanesThatDoNotRead sends 200 KiB of text into each of two
// panes whose programs never read, so both send-texts block in the write to
// the pane. Three 200 KiB set-buffer calls must then succeed at once: the
// blocked verbs must not hold the read budget. A third send-text into one of
// the blocked panes must be refused as busy, after its wait for the slot.
func TestSendTextIntoPanesThatDoNotRead(t *testing.T) {
	d, sock := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "st")
	setup := dialVerb(t, sock)
	open := func() string {
		t.Helper()
		got := result(t, callP(setup, t, "new-window", map[string]any{
			"session": "st", "command": []string{"sleep", "600"}, "focus": false,
		}))
		id, _ := got["window_id"].(string)
		if id == "" {
			t.Fatalf("new-window = %v", got)
		}
		return id
	}
	paneOf := func(id string) *PTY {
		for _, w := range sess.GetState().Windows {
			if w.ID == id {
				return sess.GetPTY(w.PTYID)
			}
		}
		return nil
	}
	text := strings.Repeat(strings.Repeat("s", 79)+"\n", (200<<10)/80)
	sendText := func(window string) string {
		raw, err := json.Marshal(map[string]any{"id": 1, "verb": "send-text", "params": map[string]any{"session": "st", "window": window, "text": text}})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	for _, window := range []string{open(), open()} {
		c := dialVerb(t, sock)
		c.send(t, sendText(window))
		deadline := time.Now().Add(10 * time.Second)
		for pty := paneOf(window); pty == nil || !pty.largeWriteWaiting(); pty = paneOf(window) {
			if time.Now().After(deadline) {
				t.Fatal("the send-text never blocked in the pane")
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Cleanup(func() { _ = c.conn.Close() })
	}

	data := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("b"), 150<<10))
	for i := range 3 {
		c := dialVerb(t, sock)
		start := time.Now()
		resp := c.call(t, `{"id":1,"verb":"set-buffer","params":{"name":"b`+string(rune('0'+i))+`","data_b64":"`+data+`"}}`)
		if e := resp["error"]; e != nil {
			t.Fatalf("set-buffer %d with two send-texts blocked: %v", i+1, e)
		}
		if waited := time.Since(start); waited > readBudgetWait/2 {
			t.Fatalf("set-buffer %d waited %v for the budget", i+1, waited)
		}
	}

	var blocked string
	for _, w := range sess.GetState().Windows {
		if pty := sess.GetPTY(w.PTYID); pty != nil && pty.largeWriteWaiting() {
			blocked = w.ID
		}
	}
	// The pane's slot holder has waited less than paneWriteWait, so this
	// one waits for the slot first, then is refused.
	third := dialVerb(t, sock)
	_ = third.conn.SetDeadline(time.Now().Add(paneWriteWait + 10*time.Second))
	third.send(t, sendText(blocked))
	line, err := third.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read the third send-text's answer: %v", err)
	}
	var resp map[string]any
	_ = json.Unmarshal(line, &resp)
	e, _ := resp["error"].(map[string]any)
	if e == nil || e["code"] != ErrVerbBusy {
		t.Fatalf("a send-text into a pane that has not read the last one got %v, want %s", resp, ErrVerbBusy)
	}
}

// TestPasteKeepsItsPlaceWhenRetried pastes into a pane, presses Enter at once,
// and has a stand-in daemon refuse the paste as busy the first time. The
// pane must get the paste, then the paste again, then Enter: Enter waits
// behind the paste and its retry.
func TestPasteKeepsItsPlaceWhenRetried(t *testing.T) {
	clientEnd, daemonEnd := net.Pipe()
	defer func() { _ = clientEnd.Close(); _ = daemonEnd.Close() }()
	c := NewTUIClient()
	c.conn = clientEnd
	c.requestIDs.Store(true)
	c.StartReadLoop()
	defer func() { _ = c.Close() }()

	const pane = "11111111-2222-3333-4444-555555555555"
	paste := bytes.Repeat([]byte("p"), 200<<10)

	type input struct{ n int }
	got := make(chan input, 16)
	go func() {
		refusedOnce := false
		for {
			msg, err := ReadMessage(daemonEnd)
			if err != nil {
				return
			}
			switch msg.Type {
			case MsgInput:
				_, data, _ := ParseBinaryPTYMessage(msg.Payload)
				got <- input{n: len(data)}
				if len(data) == len(paste) && !refusedOnce {
					refusedOnce = true
					reply, _ := NewMessage(MsgError, &ErrorPayload{Code: ErrCodeBusy, Message: "busy"})
					reply.ReqID = msg.ReqID
					_ = WriteMessage(daemonEnd, reply)
				}
			case MsgPing:
				reply, _ := NewMessage(MsgPong, nil)
				reply.ReqID = msg.ReqID
				_ = WriteMessage(daemonEnd, reply)
			}
		}
	}()

	if err := c.WritePTY(pane, paste); err != nil {
		t.Fatal(err)
	}
	if err := c.WritePTY(pane, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	want := []int{len(paste), len(paste), 1}
	for i, n := range want {
		select {
		case in := <-got:
			if in.n != n {
				t.Fatalf("input %d to the pane was %d bytes, want %d (want the paste, its retry, then Enter)", i+1, in.n, n)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("input %d never reached the pane", i+1)
		}
	}
}

// TestLargeWritesIntoAReadingPaneQueue sends six 1 MiB send-text calls at
// once into a pane running cat, which reads. All six must succeed: a large
// write waits for the pane's slot while the one before it goes in. Into a
// pane that does not read, the next large write must still be refused.
func TestLargeWritesIntoAReadingPaneQueue(t *testing.T) {
	d, sock := startTestDaemon(t)
	_ = makeSessionWithWindow(t, d, "q")
	setup := dialVerb(t, sock)
	got := result(t, callP(setup, t, "new-window", map[string]any{
		"session": "q", "command": []string{"cat"}, "focus": false,
	}))
	window, _ := got["window_id"].(string)
	text := strings.Repeat(strings.Repeat("c", 79)+"\n", (1<<20)/80)
	raw, err := json.Marshal(map[string]any{"id": 1, "verb": "send-text", "params": map[string]any{"session": "q", "window": window, "text": text}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 6)
	for range 6 {
		c := dialVerb(t, sock)
		wg.Go(func() {
			_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
			c.send(t, string(raw))
			line, err := c.r.ReadBytes('\n')
			if err != nil {
				errs <- err.Error()
				return
			}
			var resp map[string]any
			_ = json.Unmarshal(line, &resp)
			if e := resp["error"]; e != nil {
				errs <- fmt.Sprint(e)
			}
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("a 1 MiB send-text into cat failed: %s", e)
	}
}

// TestPasteHoldIsASafetyNet has a stand-in daemon that never answers the
// ping behind a paste. The input held behind the paste must go out after
// pasteHoldTimeout. Once the connection is gone, input to the held pane
// must return the connection's error.
func TestPasteHoldIsASafetyNet(t *testing.T) {
	old := pasteHoldTimeout
	pasteHoldTimeout = 300 * time.Millisecond
	defer func() { pasteHoldTimeout = old }()

	clientEnd, daemonEnd := net.Pipe()
	defer func() { _ = clientEnd.Close(); _ = daemonEnd.Close() }()
	c := NewTUIClient()
	c.conn = clientEnd
	c.requestIDs.Store(true)
	c.StartReadLoop()
	defer func() { _ = c.Close() }()

	const pane = "11111111-2222-3333-4444-555555555555"
	got := make(chan int, 16)
	go func() {
		for {
			msg, err := ReadMessage(daemonEnd)
			if err != nil {
				return
			}
			if msg.Type == MsgInput {
				_, data, _ := ParseBinaryPTYMessage(msg.Payload)
				got <- len(data)
			}
		}
	}()
	if err := c.WritePTY(pane, bytes.Repeat([]byte("p"), 200<<10)); err != nil {
		t.Fatal(err)
	}
	if err := c.WritePTY(pane, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	if n := <-got; n != 200<<10 {
		t.Fatalf("the first input was %d bytes, want the paste", n)
	}
	select {
	case n := <-got:
		if n != 1 {
			t.Fatalf("the held input was %d bytes, want Enter", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the input held behind a paste with no pong was never sent")
	}

	// A paste on a connection that then goes: the next input returns the
	// connection's error, well before the hold would run out.
	pasteHoldTimeout = time.Minute
	if err := c.WritePTY(pane, bytes.Repeat([]byte("q"), 200<<10)); err != nil {
		t.Fatal(err)
	}
	<-got
	_ = daemonEnd.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := c.WritePTY(pane, []byte("\r"))
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("input to a held pane on a lost connection was queued with no error")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
