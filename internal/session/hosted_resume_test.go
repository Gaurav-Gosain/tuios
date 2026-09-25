package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A pane this machine runs for another machine, outliving a dropped link. See
// hosted_resume.go for the far half and remotePane.reconnect for the near one.

// droppableFederation is socketFederation with a link that can be cut: drop
// closes every connection it opened and refuses new ones until up.
type droppableFederation struct {
	socketFederation
	mu    sync.Mutex
	down  bool
	conns []net.Conn
}

var errLinkDown = errors.New("the link is down")

func (f *droppableFederation) OpenConnection(ctx context.Context, host string) (io.ReadWriteCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errLinkDown
	}
	c, err := net.DialTimeout("unix", f.socketPath, 3*time.Second)
	if err != nil {
		return nil, err
	}
	f.conns = append(f.conns, c)
	return c, nil
}

func (f *droppableFederation) Call(ctx context.Context, host, verb string, params any) (json.RawMessage, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()
	if down {
		return nil, errLinkDown
	}
	return f.socketFederation.Call(ctx, host, verb, params)
}

func (f *droppableFederation) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = true
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.conns = nil
}

func (f *droppableFederation) up() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = false
}

// waitHostedDetached blocks until the far pane has noticed its connection went.
func waitHostedDetached(t *testing.T, d *Daemon, id string) {
	t.Helper()
	deadline := time.Now().Add(paneBudget)
	for time.Now().Before(deadline) {
		hp := d.lookupHostedPane(id)
		if hp == nil {
			t.Fatalf("ASSERTION: the far machine ended pane %s when its link dropped", id)
		}
		hp.connMu.Lock()
		detached := hp.conn == nil
		hp.connMu.Unlock()
		if detached {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the far machine never noticed the link to pane %s dropped", id)
}

// TestAPaneNotReattachedWithinItsGraceEnds: the far machine keeps the process
// for its own hosted_grace and not a second longer.
func TestAPaneNotReattachedWithinItsGraceEnds(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	d.SetLinkPolicies(map[string]config.HostConfig{"*": {HostedGrace: "1s"}})
	fed := &droppableFederation{socketFederation: socketFederation{socketPath: socketPath}}
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Resumable: true, Command: []string{"/bin/sh"}})
	if p.grace != time.Second {
		t.Fatalf("grace %v, want the far machine's 1s", p.grace)
	}
	r := drainPane(p)
	fed.drop()
	waitGone(t, d, p.id, paneBudget)
	select {
	case <-r.done:
	case <-time.After(paneBudget):
		t.Fatal("the pane here never ended after the far grace ran out")
	}
}

// TestAPaneWithNoGraceEndsWithTheLink: hosted_grace = "0" is the old
// behaviour, and so is an owner that does not ask.
func TestAPaneWithNoGraceEndsWithTheLink(t *testing.T) {
	for name, setup := range map[string]struct {
		policy    map[string]config.HostConfig
		resumable bool
	}{
		"policy of zero":    {policy: map[string]config.HostConfig{"*": {HostedGrace: "0"}}, resumable: true},
		"owner did not ask": {resumable: false},
	} {
		t.Run(name, func(t *testing.T) {
			d, socketPath := startTestDaemon(t)
			if setup.policy != nil {
				d.SetLinkPolicies(setup.policy)
			}
			fed := &droppableFederation{socketFederation: socketFederation{socketPath: socketPath}}
			p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Resumable: setup.resumable, Command: []string{"/bin/sh"}})
			if p.resumeToken != "" {
				t.Fatalf("a pane with no grace got a resume token")
			}
			fed.drop()
			waitGone(t, d, p.id, paneBudget)
		})
	}
}

// TestAReattachNeedsItsToken: the pane id alone does not reattach a pane, so a
// caller that learned it cannot take the process over.
func TestAReattachNeedsItsToken(t *testing.T) {
	_, socketPath := startTestDaemon(t)
	fed := &droppableFederation{socketFederation: socketFederation{socketPath: socketPath}}
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Resumable: true, Command: []string{"/bin/sh"}})

	c := dialVerb(t, socketPath)
	resp := callVerb(t, c, "open-pane", map[string]any{"resume": map[string]any{"pane": p.id, "token": "not-it", "offset": 0}})
	mustRefuse(t, resp, ErrVerbForbidden, "a reattach with the wrong token")
	resp = callVerb(t, dialVerb(t, socketPath), "open-pane", map[string]any{"resume": map[string]any{"pane": "nope", "token": p.resumeToken}})
	mustRefuse(t, resp, ErrVerbUnknownPane, "a reattach of a pane that does not exist")
}

// TestClosingAResumablePaneEndsItAtOnce: a window closed on purpose does not
// leave its process waiting out a grace.
func TestClosingAResumablePaneEndsItAtOnce(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	fed := &droppableFederation{socketFederation: socketFederation{socketPath: socketPath}}
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Resumable: true, Command: []string{"/bin/sh"}})
	if p.grace <= 0 {
		t.Fatal("no grace was given, so this proves nothing")
	}
	_ = p.Close()
	waitGone(t, d, p.id, 10*time.Second)
}

// TestAFarDaemonFromBeforeResumablePanesStillOpensAPane: a far daemon whose
// open-pane has no resumable refuses it by name, and the owner asks again
// without it and gets the pane that ends with the link, as before.
func TestAFarDaemonFromBeforeResumablePanesStillOpensAPane(t *testing.T) {
	old := verbRegistry["open-pane"]
	old.params = slices.DeleteFunc(slices.Clone(old.params), func(p verbParam) bool { return p.Name == "resumable" || p.Name == "resume" })

	owner, far := net.Pipe()
	t.Cleanup(func() { _ = owner.Close(); _ = far.Close() })
	var seen []string
	farDone := make(chan struct{})
	go func() {
		defer close(farDone)
		br := bufio.NewReader(far)
		for {
			line, err := br.ReadBytes('\n')
			if err != nil {
				return
			}
			var req verbRequest
			if err := json.Unmarshal(line, &req); err != nil {
				return
			}
			seen = append(seen, string(req.Params))
			verr := checkParamNames(req.Verb, old, req.Params)
			reply := []byte(`{"id":1,"result":{"type":"pane","pane":"p1","resume_token":"ignored","grace":600}}`)
			if verr != nil {
				reply, _ = json.Marshal(verbResponse{ID: req.ID, Error: verr})
			}
			if _, err := far.Write(append(reply, '\n')); err != nil || verr == nil {
				return
			}
		}
	}()

	opened, _, err := openPaneReply(owner, hostedPaneSpec{Width: 80, Height: 24, Resumable: true})
	if err != nil {
		t.Fatalf("a far daemon from before resumable panes refused the pane: %v", err)
	}
	<-farDone
	if opened.Pane != "p1" || opened.ResumeToken != "" || opened.Grace != 0 {
		t.Errorf("opened %+v, want p1 with no resume token", opened)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], `"resumable"`) || strings.Contains(seen[1], `"resumable"`) {
		t.Errorf("the requests were %q, want one with resumable and a retry without it", seen)
	}
}

// TestReplayIsWhatWasMissedOrTheWholeRing covers the ring's arithmetic.
func TestReplayIsWhatWasMissedOrTheWholeRing(t *testing.T) {
	hp := &hostedPane{}
	hp.ring = []byte("0123456789")
	hp.outSeq = 110 // the ring holds bytes 100 to 109
	for _, tc := range []struct {
		from int64
		want string
		gap  bool
	}{
		{110, "", false},
		{105, "56789", false},
		{100, "0123456789", false},
		{50, "0123456789", true},
		{200, "0123456789", true},
	} {
		got, gap := hp.replayFromLocked(tc.from)
		if string(got) != tc.want || gap != tc.gap {
			t.Errorf("from %d: %q gap %v, want %q gap %v", tc.from, got, gap, tc.want, tc.gap)
		}
	}
}
