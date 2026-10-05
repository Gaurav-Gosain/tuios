package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

// A call that times out leaves its reply on the connection. Before the
// client marked itself broken, the next call read that late reply as its own,
// so a loop that captured pane after pane handed back one pane's screen as
// the next pane's. This drives the race on a pipe: the first reply comes
// after its call gave up, and the second call must fail rather than return
// it. The way it could pass wrongly: the second call times out too and fails
// for that reason. The server answers it at once, so a failure here is the
// client refusing, which the error says.
func TestVerbClientRefusesAReplyOutOfStep(t *testing.T) {
	clientEnd, serverEnd := bufferedPair(t)
	c := &VerbClient{conn: clientEnd, r: bufio.NewReader(clientEnd)}

	release := make(chan struct{})
	go func() {
		r := bufio.NewReader(serverEnd)
		var ids []json.RawMessage
		for range 2 {
			line, err := r.ReadBytes('\n')
			if err != nil {
				return
			}
			var req verbRequest
			_ = json.Unmarshal(line, &req)
			ids = append(ids, req.ID)
			if len(ids) == 1 {
				<-release
			}
			for _, id := range ids {
				out, _ := json.Marshal(verbResponse{ID: id, Result: map[string]any{"for": string(id)}})
				if _, err := serverEnd.Write(append(out, '\n')); err != nil {
					return
				}
			}
			ids = ids[:0]
		}
	}()

	if _, err := c.CallWithTimeout("capture-pane", map[string]any{"window": "a"}, 50*time.Millisecond); err == nil {
		t.Fatal("the first call should time out")
	}
	close(release)
	res, err := c.CallWithTimeout("capture-pane", map[string]any{"window": "b"}, time.Second)
	if err == nil {
		t.Fatalf("the second call returned %s after the first timed out; it must refuse", res)
	}
	if !errors.Is(err, ErrVerbClientBroken) {
		t.Fatalf("the second call failed for another reason: %v", err)
	}
}

// A reply with another request's id is refused even with no timeout before
// it, and the connection stays refused.
func TestVerbClientRefusesAReplyForAnotherRequest(t *testing.T) {
	clientEnd, serverEnd := bufferedPair(t)
	c := &VerbClient{conn: clientEnd, r: bufio.NewReader(clientEnd)}
	go func() {
		r := bufio.NewReader(serverEnd)
		for {
			if _, err := r.ReadBytes('\n'); err != nil {
				return
			}
			out, _ := json.Marshal(verbResponse{ID: json.RawMessage("99"), Result: "stale"})
			if _, err := serverEnd.Write(append(out, '\n')); err != nil {
				return
			}
		}
	}()
	if _, err := c.CallWithTimeout("list-windows", nil, time.Second); !errors.Is(err, ErrVerbClientBroken) {
		t.Fatalf("a reply to request 99 was taken for request 1: %v", err)
	}
	if _, err := c.CallWithTimeout("list-windows", nil, time.Second); !errors.Is(err, ErrVerbClientBroken) {
		t.Fatalf("the connection was used again after a reply out of step: %v", err)
	}
}

// bufferedPair is two ends of a loopback connection. Unlike net.Pipe it
// buffers, so a reply the client is not reading yet does not block the
// server, which is how a real socket behaves.
func bufferedPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	server := <-accepted
	if server == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client, server
}
