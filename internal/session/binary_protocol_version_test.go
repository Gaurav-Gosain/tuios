package session

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeDaemon answers exactly one binary handshake with the welcome it is given,
// which is how a daemon of another vintage is put in front of a client without
// building that vintage.
func fakeDaemon(t *testing.T, welcome *WelcomePayload) (hello chan *HelloPayload) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	hello = make(chan *HelloPayload, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		codec := GetCodec(CodecGob)
		msg, _, err := ReadMessageWithCodec(conn)
		if err != nil || msg.Type != MsgHello {
			return
		}
		var payload HelloPayload
		if err := msg.ParsePayloadWithCodec(&payload, codec); err != nil {
			return
		}
		hello <- &payload
		reply, err := NewMessageWithCodec(MsgWelcome, welcome, codec)
		if err != nil {
			return
		}
		_ = WriteMessageWithCodec(conn, reply, codec)
		// Hold the connection open so a client that accepts the handshake is
		// not failed by an EOF it never asked about.
		time.Sleep(2 * time.Second)
	}()
	return hello
}

// TestClientRefusesADaemonSpeakingAnotherProtocol is the release case: a user
// upgrades tuios while their daemon keeps running, and a new client meets a
// daemon that does not speak its wire protocol.
//
// An unchecked version field is worse than no field, because it implies a check
// that does not happen. The client has to refuse, and the refusal has to name
// the command that fixes it.
func TestClientRefusesADaemonSpeakingAnotherProtocol(t *testing.T) {
	seen := fakeDaemon(t, &WelcomePayload{
		Version:  "9.9.9",
		Codec:    "gob",
		Protocol: ProtocolVersion + 1,
	})

	c := NewTUIClient()
	err := c.Connect("0.8.0", 80, 24)
	if err == nil {
		_ = c.Close()
		t.Fatalf("the client attached to a daemon speaking protocol %d while it speaks %d", ProtocolVersion+1, ProtocolVersion)
	}

	var mismatch *ProtocolMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected a *ProtocolMismatchError, got %T: %v", err, err)
	}
	if mismatch.DaemonProtocol != ProtocolVersion+1 || mismatch.ClientProtocol != ProtocolVersion {
		t.Fatalf("the error does not carry both versions: %+v", mismatch)
	}
	for _, want := range []string{"9.9.9", "0.8.0", "tuios kill-server"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the message a user sees does not mention %q:\n%s", want, err.Error())
		}
	}

	// The precondition: the client announced its own version, or the daemon had
	// nothing to compare and the check is not a check.
	select {
	case h := <-seen:
		if h.Protocol != ProtocolVersion {
			t.Fatalf("client announced protocol %d, want %d", h.Protocol, ProtocolVersion)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("vacuous: the daemon never saw a hello")
	}
}

// TestClientAcceptsADaemonThatPredatesTheVersionField pins the other half of the
// decision. Every daemon that ever shipped speaks this protocol; the field
// saying so is what is new. Reading its absence as a mismatch would refuse every
// running daemon at exactly the release the check exists for.
func TestClientAcceptsADaemonThatPredatesTheVersionField(t *testing.T) {
	fakeDaemon(t, &WelcomePayload{
		Version:      "0.7.0",
		Codec:        "gob",
		SessionNames: []string{"work"},
	})

	c := NewTUIClient()
	if err := c.Connect("0.8.0", 80, 24); err != nil {
		t.Fatalf("a v0.7.0 daemon was refused by a v0.8.0 client: %v", err)
	}
	_ = c.Close()
}

// TestDaemonAnswersAHello is the same failure from the other side, for the
// daemon that outlives the client that started it, plus the upgrade case that
// has to keep working: a client from before the field says nothing and is
// served.
func TestDaemonAnswersAHello(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(d.Stop)

	socketPath, err := GetSocketPath()
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}
	codec := GetCodec(CodecGob)

	say := func(t *testing.T, hello *HelloPayload) *Message {
		t.Helper()
		conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		msg, err := NewMessageWithCodec(MsgHello, hello, codec)
		if err != nil {
			t.Fatalf("encode hello: %v", err)
		}
		if err := WriteMessageWithCodec(conn, msg, codec); err != nil {
			t.Fatalf("send hello: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, _, err := ReadMessageWithCodec(conn)
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		return resp
	}

	t.Run("refuses a protocol it does not serve", func(t *testing.T) {
		resp := say(t, &HelloPayload{Version: "99.0.0", PreferredCodec: "gob", Protocol: ProtocolVersion + 1})
		if resp.Type == MsgWelcome {
			t.Fatalf("the daemon welcomed a client speaking protocol %d while it speaks %d", ProtocolVersion+1, ProtocolVersion)
		}
		if resp.Type != MsgError {
			t.Fatalf("expected an error reply, got type %d", resp.Type)
		}
		var errPayload ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, codec); err != nil {
			t.Fatalf("parse error reply: %v", err)
		}
		if !strings.Contains(errPayload.Message, "tuios kill-server") {
			t.Fatalf("the daemon's refusal does not name the fix: %s", errPayload.Message)
		}
	})

	t.Run("serves a client that predates the version field", func(t *testing.T) {
		resp := say(t, &HelloPayload{Version: "0.7.0", PreferredCodec: "gob"})
		if resp.Type != MsgWelcome {
			t.Fatalf("a v0.7.0 client was refused by this daemon (reply type %d)", resp.Type)
		}
		var welcome WelcomePayload
		if err := resp.ParsePayloadWithCodec(&welcome, codec); err != nil {
			t.Fatalf("parse welcome: %v", err)
		}
		if welcome.Protocol != ProtocolVersion {
			t.Fatalf("the daemon announced protocol %d, want %d", welcome.Protocol, ProtocolVersion)
		}
	})
}
