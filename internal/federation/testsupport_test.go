package federation

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type stubDaemon struct {
	path string
	ln   net.Listener
	wg   sync.WaitGroup
}

func startStubDaemon(t *testing.T, handle func(verb string, params json.RawMessage) (any, *RemoteError)) *stubDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "fed")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &stubDaemon{path: path, ln: ln}
	d.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.wg.Go(func() {
				defer func() { _ = conn.Close() }()
				serveStub(conn, handle)
			})
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		d.wg.Wait()
		_ = os.RemoveAll(dir)
	})
	return d
}

func serveStub(conn net.Conn, handle func(string, json.RawMessage) (any, *RemoteError)) {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Verb   string          `json:"verb"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		result, rerr := handle(req.Verb, req.Params)
		resp := map[string]any{"id": req.ID}
		if rerr != nil {
			resp["error"] = rerr
		} else {
			resp["result"] = result
		}
		line, _ := json.Marshal(resp)
		if _, err := conn.Write(append(line, '\n')); err != nil {
			return
		}
	}
	_ = sc.Err()
}

func (d *stubDaemon) dial() (net.Conn, error) { return net.Dial("unix", d.path) }

func helloOK(version string, sessions int) func(string, json.RawMessage) (any, *RemoteError) {
	return func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: version, PID: 4242, Sessions: sessions}, nil
		}
		return nil, &RemoteError{Code: "unknown_verb", Message: "unknown verb " + verb}
	}
}

type pipeEnd struct {
	r    *os.File
	w    *os.File
	once sync.Once
}

func (p *pipeEnd) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeEnd) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeEnd) Close() error {
	p.once.Do(func() {
		_ = p.r.Close()
		_ = p.w.Close()
	})
	return nil
}
func (p *pipeEnd) Diagnostic() string { return "" }

func duplexPipe(t *testing.T) (*pipeEnd, *pipeEnd) {
	t.Helper()
	ar, bw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	br, aw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	a := &pipeEnd{r: ar, w: aw}
	b := &pipeEnd{r: br, w: bw}
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return a, b
}

func proxyDialer(t *testing.T, d *stubDaemon) Dialer {
	t.Helper()
	return func(_ context.Context, _ Host) (Transport, error) {
		hub, remote := duplexPipe(t)
		go func() {
			_ = ServeProxyFor(remote, remote, func(StreamOpen) (net.Conn, error) { return d.dial() })
		}()
		return hub, nil
	}
}

type silentTransport struct {
	closed chan struct{}
	once   sync.Once
}

func newSilentTransport() *silentTransport {
	return &silentTransport{closed: make(chan struct{})}
}

func (s *silentTransport) Read(b []byte) (int, error) {
	<-s.closed
	return 0, io.EOF
}
func (s *silentTransport) Write(b []byte) (int, error) { return len(b), nil }
func (s *silentTransport) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
func (s *silentTransport) Diagnostic() string { return "" }

func testOptions(dial Dialer) Options {
	return Options{
		Dial:            dial,
		ClientName:      "tuios-test",
		ClientVersion:   "0.0.0-test",
		VerbProtocol:    1,
		MinVerbProtocol: 1,
		CallTimeout:     700 * time.Millisecond,
		InitialBackoff:  20 * time.Millisecond,
		MaxBackoff:      50 * time.Millisecond,
	}
}

func managerFor(t *testing.T, opts Options, hosts ...Host) *Manager {
	t.Helper()
	table, problems := NewTable(hosts)
	if len(problems) > 0 {
		t.Fatalf("host table rejected an entry: %v", problems)
	}
	m := New(table, opts)
	m.Start(context.Background())
	t.Cleanup(m.Stop)
	return m
}
