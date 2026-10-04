package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// A client used to match the daemon's replies by message type alone. The
// daemon answers a failed subscribe, which nobody waits for, with MsgError, so
// during a restore the error about pane A was taken as the answer to pane B's
// state request, and B's real state then went to whichever request was waiting
// next. A reply that came after its request timed out did the same. These are
// the wire compatibility tests for the request id that ties a reply to its
// request, and the race regressions for the two ways a reply went astray.

// rawFrame reads one frame off r and returns it whole, so a test can look at
// the codec byte a peer actually wrote.
func rawFrame(t *testing.T, r io.Reader) []byte {
	t.Helper()
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		t.Fatalf("read frame length: %v", err)
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatalf("read frame body: %v", err)
	}
	return append(hdr[:], body...)
}

// TestRequestIDFrames pins the frame layout. An untagged frame is byte for byte
// what every earlier build writes and reads, so a peer that knows nothing of
// request ids sees nothing new. A tagged frame carries its id in front of the
// payload and says so in the codec byte.
func TestRequestIDFrames(t *testing.T) {
	payload := []byte{0xde, 0xad, 0xbe, 0xef}

	t.Run("untagged is the old layout", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteMessage(&buf, &Message{Type: MsgGetTerminalState, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		want := []byte{0, 0, 0, 6, byte(MsgGetTerminalState), wireCodecGob, 0xde, 0xad, 0xbe, 0xef}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Fatalf("an untagged frame is % x, want % x", buf.Bytes(), want)
		}
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got.ReqID != 0 || !bytes.Equal(got.Payload, payload) {
			t.Fatalf("read back id %d payload % x", got.ReqID, got.Payload)
		}
	})

	t.Run("tagged carries the id outside the payload", func(t *testing.T) {
		for _, p := range [][]byte{payload, nil} {
			var buf bytes.Buffer
			if err := WriteMessage(&buf, &Message{Type: MsgTerminalState, Payload: p, ReqID: 0x0102030405060708}); err != nil {
				t.Fatal(err)
			}
			frame := buf.Bytes()
			if frame[5] != wireCodecGobTagged {
				t.Fatalf("a tagged frame has codec byte %d, want %d", frame[5], wireCodecGobTagged)
			}
			if n := binary.BigEndian.Uint32(frame); int(n) != 2+reqIDLen+len(p) {
				t.Fatalf("a tagged frame says it is %d bytes, want %d", n, 2+reqIDLen+len(p))
			}
			got, err := ReadMessage(&buf)
			if err != nil {
				t.Fatal(err)
			}
			if got.Type != MsgTerminalState || got.ReqID != 0x0102030405060708 || !bytes.Equal(got.Payload, p) {
				t.Fatalf("read back type %d id %#x payload % x", got.Type, got.ReqID, got.Payload)
			}
		}
	})

	t.Run("a tagged frame over its type's limit is refused with its id and the stream stays in step", func(t *testing.T) {
		var buf bytes.Buffer
		big := make([]byte, maxClientActivityFrame)
		if err := WriteMessage(&buf, &Message{Type: MsgClientActivity, Payload: big, ReqID: 42}); err != nil {
			t.Fatal(err)
		}
		if err := WriteMessage(&buf, &Message{Type: MsgList, ReqID: 43}); err != nil {
			t.Fatal(err)
		}
		var n uint32
		_ = binary.Read(&buf, binary.BigEndian, &n)
		_, err := readMessageBody(&buf, n, daemonFrameLimit)
		tooLarge, ok := errors.AsType[*FrameTooLargeError](err)
		if !ok {
			t.Fatalf("an oversized tagged frame read as %v, want a FrameTooLargeError", err)
		}
		if tooLarge.ReqID != 42 {
			t.Fatalf("the refusal carries request id %d, want 42", tooLarge.ReqID)
		}
		next, err := ReadMessage(&buf)
		if err != nil || next.Type != MsgList || next.ReqID != 43 {
			t.Fatalf("the frame after the refused one read as %+v, %v", next, err)
		}
	})

	t.Run("a tagged frame too short for its id is an error", func(t *testing.T) {
		frame := []byte{0, 0, 0, 5, byte(MsgList), wireCodecGobTagged, 1, 2, 3}
		if _, err := ReadMessage(bytes.NewReader(frame)); err == nil {
			t.Fatal("a tagged frame with 3 bytes of id read without error")
		}
	})
}

// TestRequestIDsAcrossPeerVintages runs each pairing of a peer that tags and
// one that does not. A daemon of this build talks to an old client exactly as
// before, and a client of this build talks to an old daemon by type, checking
// a state reply is about the pane it asked for.
func TestRequestIDsAcrossPeerVintages(t *testing.T) {
	t.Run("old client, new daemon", func(t *testing.T) {
		_, socketPath := startTestDaemon(t)
		conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		send := func(typ MessageType, payload any) {
			t.Helper()
			msg, err := NewMessage(typ, payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteMessage(conn, msg); err != nil {
				t.Fatal(err)
			}
		}
		// An old client: a hello in its own words, which name no request ids,
		// and untagged requests.
		send(MsgHello, &HelloPayload{Version: "old", PreferredCodec: "gob", Protocol: ProtocolVersion})
		send(MsgAttach, &AttachPayload{SessionName: "vintage", CreateNew: true, Width: 80, Height: 24})
		send(MsgGetTerminalState, &GetTerminalStatePayload{PTYID: "no-such-pane"})
		send(MsgList, nil)

		var types []MessageType
		for len(types) == 0 || types[len(types)-1] != MsgSessionList {
			frame := rawFrame(t, conn)
			if frame[5] != wireCodecGob {
				t.Fatalf("the daemon wrote codec byte %d to a client that never tagged a request", frame[5])
			}
			msg, err := ReadMessage(bytes.NewReader(frame))
			if err != nil {
				t.Fatalf("an old client could not read the daemon's frame: %v", err)
			}
			types = append(types, msg.Type)
			if msg.Type == MsgError {
				var e ErrorPayload
				if err := msg.ParsePayload(&e); err != nil || !strings.Contains(e.Message, "no-such-pane") {
					t.Fatalf("the error reply read as %+v, %v", e, err)
				}
			}
		}
		for _, want := range []MessageType{MsgWelcome, MsgAttached, MsgError, MsgSessionList} {
			found := false
			for _, got := range types {
				found = found || got == want
			}
			if !found {
				t.Fatalf("the old client was sent %v, which lacks %s", types, MessageTypeName(want))
			}
		}
	})

	t.Run("new client, new daemon", func(t *testing.T) {
		startTestDaemon(t)
		c := NewTUIClient()
		if err := c.Connect("new", 80, 24); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		if !c.requestIDs.Load() {
			t.Fatal("a client of this build did not take request ids from a daemon of this build")
		}
		if _, err := c.AttachSession("vintage", true, 80, 24); err != nil {
			t.Fatalf("attach before the read loop: %v", err)
		}
		c.StartReadLoop()
		_, err := c.GetTerminalState("no-such-pane", 0, 0)
		if err == nil || !strings.Contains(err.Error(), "no-such-pane") {
			t.Fatalf("a tagged state request for a missing pane answered %v", err)
		}
		if _, err := c.RefreshSessionList(); err != nil {
			t.Fatalf("a tagged list: %v", err)
		}
	})

	t.Run("new client, old daemon", func(t *testing.T) {
		requests := oldDaemon(t, func(req *Message, write func(*Message)) {
			if req.Type != MsgGetTerminalState {
				return
			}
			var p GetTerminalStatePayload
			_ = req.ParsePayload(&p)
			// First the answer to a request that gave up long ago, about
			// another pane, then the answer to this one.
			for _, id := range []string{"stale-pane", p.PTYID} {
				msg, _ := NewMessage(MsgTerminalState, &TerminalStatePayload{PTYID: id, State: &TerminalState{Width: len(id)}})
				write(msg)
			}
		})
		c := NewTUIClient()
		if err := c.Connect("new", 80, 24); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		if c.requestIDs.Load() {
			t.Fatal("the client took request ids from a daemon that never offered them")
		}
		c.StartReadLoop()
		st, err := c.GetTerminalState("pane-b", 0, 0)
		if err != nil {
			t.Fatalf("state request to an old daemon: %v", err)
		}
		if st.Width != len("pane-b") {
			t.Fatalf("the client took the state of another pane (width %d)", st.Width)
		}
		if codec := <-requests; codec != wireCodecGob {
			t.Fatalf("the client wrote codec byte %d to a daemon that does not read request ids", codec)
		}
	})
}

// oldDaemon is a daemon from before request ids: it welcomes without
// RequestIDs and answers each request through answer. It reports the codec
// byte of every request after the hello.
func oldDaemon(t *testing.T, answer func(req *Message, write func(*Message))) (requests chan byte) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	requests = make(chan byte, 16)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, err := ReadMessage(conn); err != nil {
			return
		}
		welcome, _ := NewMessage(MsgWelcome, &WelcomePayload{Version: "old", Codec: "gob", Protocol: ProtocolVersion})
		_ = WriteMessage(conn, welcome)
		for {
			var hdr [4]byte
			if _, err := io.ReadFull(conn, hdr[:]); err != nil {
				return
			}
			body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
			if _, err := io.ReadFull(conn, body); err != nil {
				return
			}
			select {
			case requests <- body[1]:
			default:
			}
			// An old daemon ignores the codec byte and decodes what follows.
			answer(&Message{Type: MessageType(body[0]), Payload: body[2:]}, func(m *Message) { _ = WriteMessage(conn, m) })
		}
	}()
	return requests
}

// TestSubscribeErrorDoesNotAnswerAStateRequest is the restore race. A
// subscribe nobody waits for fails, and the state request sent right behind
// it must get its own answer, not the subscribe's error. The daemon answers in
// the order it was asked, so the subscribe's error always arrives first: on a
// client that matches by type, the state request for pane B fails with pane
// A's error every time.
func TestSubscribeErrorDoesNotAnswerAStateRequest(t *testing.T) {
	startTestDaemon(t)
	c := NewTUIClient()
	if err := c.Connect("test", 80, 24); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.AttachSession("restore", true, 80, 24); err != nil {
		t.Fatal(err)
	}
	c.StartReadLoop()

	// Positive half: with nothing failing in front of it, a state request for
	// a missing pane is refused with that pane's error.
	if _, err := c.GetTerminalState("pane-b-missing", 0, 0); err == nil || !strings.Contains(err.Error(), "pane-b-missing") {
		t.Fatalf("the plain refusal read %v", err)
	}

	for round := range 3 {
		if err := c.SubscribePTY("pane-a-gone", 0, false, func([]byte) {}); err != nil {
			t.Fatal(err)
		}
		_, err := c.GetTerminalState("pane-b-missing", 0, 0)
		if err == nil {
			t.Fatalf("round %d: the state request for a missing pane succeeded", round)
		}
		if strings.Contains(err.Error(), "pane-a-gone") || !strings.Contains(err.Error(), "pane-b-missing") {
			t.Fatalf("round %d: the state request for pane B was answered with %q", round, err)
		}
	}
}

// TestLateReplyIsNotTakenByTheNextRequest is the other half: a reply that
// comes after its request gave up must not answer the request after it. The
// daemon here holds pane B's answer until pane C has asked, then sends both,
// B's first. A client that matches by type paints B's screen into pane C.
func TestLateReplyIsNotTakenByTheNextRequest(t *testing.T) {
	defer func(prev time.Duration) { roundTripTimeout = prev }(roundTripTimeout)
	roundTripTimeout = 200 * time.Millisecond

	server, client := net.Pipe()
	defer func() { _ = server.Close() }()
	c := newTestTUIClient(client)
	c.requestIDs.Store(true)
	c.StartReadLoop()
	defer func() { _ = c.Close() }()

	go func() {
		var held *Message
		for {
			req, err := ReadMessage(server)
			if err != nil {
				return
			}
			var p GetTerminalStatePayload
			_ = req.ParsePayload(&p)
			reply, _ := NewMessage(MsgTerminalState, &TerminalStatePayload{PTYID: p.PTYID, State: &TerminalState{Width: len(p.PTYID)}})
			reply.ReqID = req.ReqID
			if held == nil {
				held = reply
				continue
			}
			_ = WriteMessage(server, held)
			_ = WriteMessage(server, reply)
		}
	}()

	if _, err := c.GetTerminalState("pane-b", 0, 0); err == nil {
		t.Fatal("pane B's request was answered though the daemon held its reply")
	}
	st, err := c.GetTerminalState("pane-c-longer", 0, 0)
	if err != nil {
		t.Fatalf("pane C's request: %v", err)
	}
	if st.Width != len("pane-c-longer") {
		t.Fatalf("pane C was given the state of pane B (width %d)", st.Width)
	}
}
