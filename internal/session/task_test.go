package session

import (
	"sync"
	"testing"
)

// The task cards' grants and their one race. The rest of the behaviour is
// proved end to end in e2e/tui/tasks_test.go. See task.go.

// panePlacer places each connection in the pane next names when the
// connection makes its first call, and keeps it there. A test sets next before
// the first call on each new connection.
type panePlacer struct {
	mu   sync.Mutex
	next string
	seen map[*connState]string
}

func placeConnections(d *Daemon) *panePlacer {
	p := &panePlacer{seen: map[*connState]string{}}
	d.setApprovalPeer(func(cs *connState) (bool, string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w, ok := p.seen[cs]
		if !ok {
			w = p.next
			p.seen[cs] = w
		}
		return w != "", w
	})
	return p
}

// paneConn dials a connection that the placer puts in window.
func (p *panePlacer) paneConn(t *testing.T, sp, window string) *verbConn {
	t.Helper()
	c := dialVerb(t, sp)
	p.mu.Lock()
	p.next = window
	p.mu.Unlock()
	result(t, callP(c, t, "hello", nil))
	p.mu.Lock()
	p.next = ""
	p.mu.Unlock()
	return c
}

func holderWindow(row map[string]any) string {
	h, _ := row["holder"].(map[string]any)
	w, _ := h["window"].(string)
	return w
}

func TestTaskClaimNextFromTwoPanesAtOnce(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	for _, title := range []string{"one", "two"} {
		result(t, callP(c, t, "add-task", map[string]any{"session": "work", "title": title}))
	}

	ca, cb := dialVerb(t, sp), dialVerb(t, sp)
	var wg sync.WaitGroup
	got := make([]map[string]any, 2)
	for i, pair := range []struct {
		c      *verbConn
		window string
	}{{ca, a}, {cb, b}} {
		wg.Go(func() {
			got[i] = callP(pair.c, t, "claim-task", map[string]any{"session": "work", "next": true, "window": pair.window})
		})
	}
	wg.Wait()
	ids := map[string]bool{}
	other := ""
	for i, resp := range got {
		res := result(t, resp)
		ids[res["id"].(string)] = true
		if res["id"] == "t1" {
			// The pane that did not take t1.
			other = []string{b, a}[i]
		}
	}
	if !ids["t1"] || !ids["t2"] {
		t.Fatalf("two concurrent claims took %v, want t1 and t2 one each", ids)
	}

	mustRefuse(t, callP(c, t, "claim-task", map[string]any{"session": "work", "next": true, "window": a}), ErrVerbNoTask, "claim --next with every card held")
	mustRefuse(t, callP(c, t, "claim-task", map[string]any{"session": "work", "id": "t1", "window": other}), ErrVerbTaskHeld, "claim of a card another pane holds")
	mustRefuse(t, callP(c, t, "get-task", map[string]any{"session": "work", "id": "t9"}), ErrVerbNoTask, "get-task of an unknown id")
}

func TestTaskOwnershipAndGrants(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "work")
	plain := dialVerb(t, sp)
	result(t, callP(plain, t, "add-task", map[string]any{"session": "work", "title": "one"}))

	setStrict(d, "read", "write", "task")
	p := placeConnections(d)
	ca, cb := p.paneConn(t, sp, a), p.paneConn(t, sp, b)

	wantForbidden(t, "a pane claiming for another pane", callP(cb, t, "claim-task", map[string]any{"session": "work", "id": "t1", "window": a}))
	claimed := result(t, callP(ca, t, "claim-task", map[string]any{"id": "t1"}))
	if holderWindow(claimed) != a || claimed["by"] != "shell" {
		t.Fatalf("claim from pane a = %v, want held by a", claimed)
	}
	wantForbidden(t, "pane b finishing a's card", callP(cb, t, "finish-task", map[string]any{"id": "t1"}))
	wantForbidden(t, "pane b releasing a's card", callP(cb, t, "release-task", map[string]any{"id": "t1"}))
	wantForbidden(t, "remove-task from a pane without admin", callP(ca, t, "remove-task", map[string]any{"id": "t1"}))
	added := result(t, callP(cb, t, "add-task", map[string]any{"title": "from b"}))
	if added["by"] != b {
		t.Errorf("a card added from pane b reads by %v, want %s", added["by"], b)
	}
	result(t, callP(ca, t, "finish-task", map[string]any{"id": "t1"}))

	setStrict(d, "read")
	cr := p.paneConn(t, sp, a)
	wantForbidden(t, "add-task under read alone", callP(cr, t, "add-task", map[string]any{"title": "nope"}))
	result(t, callP(cr, t, "list-tasks", nil))
}
