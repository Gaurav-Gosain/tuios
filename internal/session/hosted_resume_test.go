package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
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

// TestAPaneOutlivesADroppedLinkAndIsReattached is the feature: the link drops,
// the process goes on, what it printed meanwhile arrives once the link is
// back, exactly once, and typing reaches it again.
func TestAPaneOutlivesADroppedLinkAndIsReattached(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	fed := &droppableFederation{socketFederation: socketFederation{socketPath: socketPath}}
	flag := filepath.Join(t.TempDir(), "go")
	script := "echo ready; while [ ! -f " + flag + " ]; do sleep 0.05; done; echo during-the-drop; exec cat"
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Resumable: true, Command: []string{"/bin/sh", "-c", script}})
	if p.resumeToken == "" || p.grace != config.DefaultHostedGrace {
		t.Fatalf("the far machine gave no grace: token %q grace %v", p.resumeToken, p.grace)
	}
	r := drainPane(p)
	if got := r.waitFor("ready", paneBudget); !strings.Contains(got, "ready") {
		t.Fatalf("the pane never started: %q", got)
	}

	fed.drop()
	waitHostedDetached(t, d, p.id)
	// The process prints while nobody is attached.
	if err := os.WriteFile(flag, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(paneBudget)
	for state, _ := p.linkState(); state != remotePaneLinkReconnecting; state, _ = p.linkState() {
		if time.Now().After(deadline) {
			t.Fatal("the pane never said it was reconnecting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := p.Write([]byte("x")); !errors.Is(err, errPaneReconnecting) {
		t.Errorf("a write while reconnecting answered %v, want errPaneReconnecting", err)
	}
	// Give the far pump time to read the output into its ring.
	time.Sleep(300 * time.Millisecond)
	fed.up()

	if got := r.waitFor("during-the-drop", paneBudget); !strings.Contains(got, "during-the-drop") {
		t.Fatalf("ASSERTION: what the process printed during the drop never arrived: %q", got)
	}
	for state, _ := p.linkState(); state != ""; state, _ = p.linkState() {
		if time.Now().After(deadline) {
			t.Fatal("the pane still says it is reconnecting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := p.Write([]byte("typed-after\n")); err != nil {
		t.Fatalf("write after the reattach: %v", err)
	}
	got := r.waitFor("typed-after\r\ntyped-after", paneBudget)
	if !strings.Contains(got, "typed-after") {
		t.Fatalf("typing after the reattach did not reach the process: %q", got)
	}
	if n := strings.Count(got, "during-the-drop"); n != 1 {
		t.Errorf("the output from the drop arrived %d times, want once: %q", n, got)
	}
	if n := strings.Count(got, "ready"); n != 1 {
		t.Errorf("output from before the drop was replayed: %q", got)
	}
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

// TestAWindowSaysItsLinkIsLostAndComesBack is the session's view: while the
// link is down the window stays, with host_link reconnecting and when the far
// grace ends, and clears when the pane is reattached.
func TestAWindowSaysItsLinkIsLostAndComesBack(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("global-link", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	fed := &droppableFederation{socketFederation: socketFederation{socketPath: socketPath}}
	sess.SetFederation(fed)
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Host: "build", Command: []string{"/bin/sh", "-c", "echo up; exec cat"}}, func(string) {})
	if err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}
	pty := sess.GetPTY(win.PTYID)
	if got := waitForPaneText(t, pty, "up", paneBudget); !strings.Contains(got, "up") {
		t.Fatalf("the pane never started: %q", got)
	}

	linkOf := func() (string, int64) {
		for _, w := range sess.GetState().Windows {
			if w.ID == win.ID {
				return w.HostLink, w.HostLinkUntil
			}
		}
		t.Fatal("ASSERTION: the window went away when its link dropped")
		return "", 0
	}
	fed.drop()
	deadline := time.Now().Add(paneBudget)
	for {
		state, until := linkOf()
		if state == remotePaneLinkReconnecting {
			if until < time.Now().Unix() {
				t.Errorf("the grace ends at %d, which is in the past", until)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the window never said its link was lost")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A listing says so too, which is what an agent or a script reads.
	listing := buildWindowListData(sess.GetState())
	found := false
	for _, w := range listing["windows"].([]map[string]any) {
		if w["window_id"] == win.ID && w["host_link"] == remotePaneLinkReconnecting && w["host_link_until"] != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("list-windows does not say the window's link is lost: %v", listing["windows"])
	}
	_, verr := d.verbSendText(nil, mustJSON(map[string]string{"session": "global-link", "window": win.ID, "text": "x"}))
	if verr == nil || verr.Code != ErrVerbHostUnreachable {
		t.Errorf("send-text while reconnecting answered %v, want %s", verr, ErrVerbHostUnreachable)
	}
	fed.up()
	for {
		state, _ := linkOf()
		if state == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the window still says its link is lost after it came back")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := pty.Write([]byte("back-again\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := waitForPaneText(t, pty, "back-again", paneBudget); !strings.Contains(got, "back-again") {
		t.Errorf("typing after the reattach did not reach the far process: %q", got)
	}
}
