package agentproto

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"
)

// peer is the agent's end of a connection in a test: it reads what the
// client sends and writes what a scripted agent would.
type peer struct {
	t     *testing.T
	msgs  chan map[string]any
	raw   chan string
	w     io.Writer
	close func()
}

// newPeer returns the client's reader and writer and the agent's end.
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
	}()
	return fromAgentR, toAgentW, p
}

// next is the next message the client sent.
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

// nextRaw is the next message as it was written.
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

// expect reads the next message and checks its method.
func (p *peer) expect(method string) map[string]any {
	p.t.Helper()
	m := p.next()
	if m["method"] != method {
		p.t.Fatalf("the client sent %v, want %s", m, method)
	}
	return m
}

// quiet checks the client sends nothing for a moment.
func (p *peer) quiet() {
	p.t.Helper()
	select {
	case m := <-p.msgs:
		<-p.raw
		p.t.Fatalf("the client sent %v, want nothing", m)
	case <-time.After(100 * time.Millisecond):
	}
}

// send writes one message from the agent.
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

// line writes one raw line from the agent.
func (p *peer) line(s string) {
	p.t.Helper()
	if _, err := io.WriteString(p.w, s+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

// respond answers request m with result.
func (p *peer) respond(m map[string]any, result any) {
	p.t.Helper()
	p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": result})
}

// events collects what a client emits.
type events struct {
	ch chan Event
}

func newEvents() *events { return &events{ch: make(chan Event, 256)} }

func (e *events) emit(ev Event) { e.ch <- ev }

// next is the next event.
func (e *events) next(t *testing.T) Event {
	t.Helper()
	select {
	case ev := <-e.ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return nil
	}
}

// permission waits for a permission event, skipping others.
func (e *events) permission(t *testing.T) *Permission {
	t.Helper()
	for {
		if p, ok := e.next(t).(*Permission); ok {
			return p
		}
	}
}
