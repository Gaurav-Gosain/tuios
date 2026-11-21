package acp

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

type peer struct {
	t     *testing.T
	msgs  chan map[string]any
	raw   chan string
	w     io.Writer
	close func()
}

func newPeer(t *testing.T) (io.Reader, io.Writer, *peer) {
	t.Helper()
	fromAgentR, fromAgentW := io.Pipe()
	toAgentR, toAgentW := io.Pipe()
	p := &peer{t: t, msgs: make(chan map[string]any, 64), raw: make(chan string, 64), w: fromAgentW}
	var once sync.Once
	p.close = func() {
		once.Do(func() {
			_ = fromAgentW.Close()
			_ = toAgentR.Close()
		})
	}
	t.Cleanup(p.close)
	go func() {
		sc := bufio.NewScanner(toAgentR)
		sc.Buffer(make([]byte, 64<<10), maxMessage)
		for sc.Scan() {
			line := sc.Text()
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				continue
			}
			p.raw <- line
			p.msgs <- m
		}
		_ = sc.Err()
	}()
	return fromAgentR, toAgentW, p
}

func (p *peer) next() map[string]any {
	p.t.Helper()
	select {
	case m := <-p.msgs:
		<-p.raw
		return m
	case <-time.After(5 * time.Second):
		p.t.Fatal("the client sent nothing")
		return nil
	}
}

func (p *peer) nextRaw() (string, map[string]any) {
	p.t.Helper()
	select {
	case m := <-p.msgs:
		return <-p.raw, m
	case <-time.After(5 * time.Second):
		p.t.Fatal("the client sent nothing")
		return "", nil
	}
}

func (p *peer) expect(method string) map[string]any {
	p.t.Helper()
	m := p.next()
	if m["method"] != method {
		p.t.Fatalf("the client sent %v, want %s", m, method)
	}
	return m
}

func (p *peer) send(v any) {
	p.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		p.t.Fatal(err)
	}
	if _, err := p.w.Write(append(data, '\n')); err != nil {
		p.t.Fatal(err)
	}
}

func (p *peer) line(s string) {
	p.t.Helper()
	if _, err := io.WriteString(p.w, s+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

func (p *peer) respond(m map[string]any, result any) {
	p.t.Helper()
	p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": result})
}
