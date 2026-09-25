package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// Mail for a machine whose link is down. See host_outbox.go.

// startHubAndGatedFar is a hub linked to a far daemon through the real proxy,
// with a gate: while down is set, every dial fails, which is a machine whose
// network is gone.
func startHubAndGatedFar(t *testing.T) (*Daemon, *farSide, *atomic.Bool) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))
	dir, err := os.MkdirTemp("", "far")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	far := &farSide{socket: filepath.Join(dir, "s")}
	far.daemon = NewDaemon(&DaemonConfig{Version: "far-build", SocketPath: far.socket, DisableAutoRestore: true})
	if err := far.daemon.Start(); err != nil {
		t.Fatalf("start the far daemon: %v", err)
	}
	t.Cleanup(far.daemon.Stop)
	down := &atomic.Bool{}
	inner := far.proxyDialer("")
	hub := NewDaemon(&DaemonConfig{
		Version:            "hub",
		DisableAutoRestore: true,
		Hosts:              []federation.Host{{Name: "build", Addr: "unused"}},
		HostDial: func(ctx context.Context, h federation.Host) (federation.Transport, error) {
			if down.Load() {
				return nil, errors.New("the network is down")
			}
			return inner(ctx, h)
		},
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("start the hub daemon: %v", err)
	}
	t.Cleanup(hub.Stop)
	return hub, far, down
}

// waitForHostDown blocks until the hub reports the host not up.
func waitForHostDown(t *testing.T, hub *Daemon, host string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := hub.federation.Report(host); ok && r.Status != federation.StatusUp {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the hub still reports %s up", host)
}

// farMessages reads the far session's ring.
func farMessages(t *testing.T, far *farSide, sessionName string) []map[string]any {
	t.Helper()
	return readAll(t, dialVerb(t, far.socket), sessionName)
}

func hubOutboxItem(t *testing.T, hub *Daemon) map[string]any {
	t.Helper()
	sp, err := GetSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range attentionItems(t, dialVerb(t, sp)) {
		if it["kind"] == AttentionOutbox {
			return it
		}
	}
	return nil
}

// TestARefusedDeliveryIsReportedAndDismissed: the far machine's answer to a
// queued message is final. The Inbox says it was refused and why, and
// dismissing the item forgets it.
func TestARefusedDeliveryIsReportedAndDismissed(t *testing.T) {
	hub, far, down := startHubAndGatedFar(t)
	makeSessionWithWindow(t, far.daemon, "far")
	waitForHostUp(t, hub, "build")
	down.Store(true)
	far.breakLink()
	waitForHostDown(t, hub, "build")
	sp, _ := GetSocketPath()
	local := dialVerb(t, sp)
	result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "text": "hi"}))
	far.daemon.SetLinkPolicies(map[string]config.HostConfig{"*": {Allow: []string{"list"}}})
	down.Store(false)

	deadline := time.Now().Add(30 * time.Second)
	var item map[string]any
	for {
		item = hubOutboxItem(t, hub)
		if item != nil && strings.Contains(item["summary"].(string), "refused") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Inbox never said the delivery was refused: %v", item)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(item["summary"].(string), "forbidden") {
		t.Errorf("the refusal does not say why: %v", item["summary"])
	}
	tui := attachTUI(t, sp, makeSessionWithWindow(t, hub, "here").Name)
	result(t, callVerb(t, local, "dismiss-attention", map[string]any{"id": item["id"], "human_nonce": tui.HumanNonce()}))
	if hubOutboxItem(t, hub) != nil {
		t.Error("the outbox item is still open after it was dismissed")
	}
}

// TestDismissingTheOutboxDiscardsWhatWaits.
func TestDismissingTheOutboxDiscardsWhatWaits(t *testing.T) {
	hub, far, down := startHubAndGatedFar(t)
	makeSessionWithWindow(t, far.daemon, "far")
	waitForHostUp(t, hub, "build")
	down.Store(true)
	far.breakLink()
	waitForHostDown(t, hub, "build")
	sp, _ := GetSocketPath()
	local := dialVerb(t, sp)
	result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "text": "never mind"}))
	item := hubOutboxItem(t, hub)
	if item == nil {
		t.Fatal("no outbox item")
	}
	tui := attachTUI(t, sp, makeSessionWithWindow(t, hub, "here").Name)
	res := result(t, callVerb(t, local, "dismiss-attention", map[string]any{"id": item["id"], "human_nonce": tui.HumanNonce()}))
	if res["discarded"] != float64(1) {
		t.Errorf("dismiss answered %v, want one message discarded", res)
	}
	if n := hub.outbox.count("build"); n != 0 {
		t.Errorf("%d messages still wait after the dismiss", n)
	}
}

// TestTheOutboxSurvivesARestartAndIsBounded.
func TestTheOutboxSurvivesARestartAndIsBounded(t *testing.T) {
	d, _ := startTestDaemon(t)
	path := filepath.Join(t.TempDir(), "mail.json")
	o := newHostOutbox(d)
	o.load(path)
	for i := range outboxMaxPerHost {
		if _, _, verr := o.enqueue("build", "far", "", json.RawMessage(`{"session":"far","text":"m`+string(rune('a'+i%26))+`"}`)); verr != nil {
			t.Fatalf("message %d was refused: %v", i, verr)
		}
	}
	if _, _, verr := o.enqueue("build", "far", "", json.RawMessage(`{"session":"far","text":"one too many"}`)); verr == nil || verr.Code != ErrVerbRateLimited {
		t.Fatalf("ASSERTION: the queue took more than its cap: %v", verr)
	}
	again := newHostOutbox(d)
	again.load(path)
	if n := again.count("build"); n != outboxMaxPerHost {
		t.Errorf("after a restart %d messages wait, want %d", n, outboxMaxPerHost)
	}
}

// TestAnOutboxItemOfAnotherMachineIsNotMirrored: mail waiting to leave a
// linked host is that host's business with its own links.
func TestAnOutboxItemOfAnotherMachineIsNotMirrored(t *testing.T) {
	in := AttentionItem{ID: "4", Kind: AttentionOutbox, ForHost: "desk", Session: "far", Window: "w1", Summary: "1 message waits", Since: time.Now().UnixNano(), Seq: 3}
	if _, ok := sanitizeHostItem("build", in, time.Now()); ok {
		t.Error("an outbox item from a linked host was mirrored")
	}
	in.Kind = AttentionErrored
	if _, ok := sanitizeHostItem("build", in, time.Now()); !ok {
		t.Error("an errored item from a linked host was not mirrored, so this proves nothing")
	}
}

// TestALinkCallerCannotQueueMailOnward: host on send-agent-message sends as
// this machine, so a caller that came over a link may not use it.
func TestALinkCallerCannotQueueMailOnward(t *testing.T) {
	_, sp := startTestDaemon(t)
	link := dialLink(t, sp)
	mustRefuse(t, callVerb(t, link, "send-agent-message", map[string]any{"host": "build", "session": "far", "text": "relay me"}),
		ErrVerbForbidden, "a link caller sending onward with host")
}

// failFirstCalls makes the hub's first n sends of text on a link fail as a
// timeout does, which leaves the link up. It counts those sends.
func failFirstCalls(hub *Daemon, n int32, text string) *atomic.Int32 {
	calls := &atomic.Int32{}
	hub.outbox.mu.Lock()
	hub.outbox.failCall = func(params json.RawMessage) error {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(params, &p)
		if p.Text != text {
			return nil
		}
		if calls.Add(1) <= n {
			return fmt.Errorf("send-agent-message on build: %w", context.DeadlineExceeded)
		}
		return nil
	}
	hub.outbox.mu.Unlock()
	return calls
}

// retryPending reports whether the hub waits to retry host's queue.
func retryPending(hub *Daemon, host string) bool {
	hub.outbox.mu.Lock()
	defer hub.outbox.mu.Unlock()
	return hub.outbox.retry[host] != nil && !hub.outbox.flushing[host]
}

// waitForFarTexts blocks until the far session's ring holds want, in order.
func waitForFarTexts(t *testing.T, far *farSide, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var got []string
		for _, m := range farMessages(t, far, "far") {
			got = append(got, m["text"].(string))
		}
		if strings.Join(got, "|") == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the far session holds %q, want %q", strings.Join(got, "|"), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestMailQueuedWithTheLinkUpIsRetried: a send that times out with the link up
// is queued, and delivered without waiting for the link to go down and come
// back. The first retry fails too, so the backoff retry is what delivers it.
func TestMailQueuedWithTheLinkUpIsRetried(t *testing.T) {
	hub, far, _ := startHubAndGatedFar(t)
	_, farWin, _ := twoWindowSession(t, far.daemon, "far")
	waitForHostUp(t, hub, "build")
	calls := failFirstCalls(hub, 2, "slow")
	sp, _ := GetSocketPath()
	local := dialVerb(t, sp)

	queued := result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "to": farWin, "from": "planner", "text": "slow"}))
	if queued["queued"] != true {
		t.Fatalf("a send that timed out was not queued: %v", queued)
	}
	waitForFarTexts(t, far, "slow")
	if r, ok := hub.federation.Report("build"); !ok || r.Status != federation.StatusUp {
		t.Fatalf("the link went down during the test, so this proves nothing: %v", r)
	}
	if n := calls.Load(); n < 3 {
		t.Errorf("delivered after %d calls, want the send, the kick and a retry", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for hubOutboxItem(t, hub) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the outbox item stayed open after the mail was delivered")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestASendWhileMailWaitsGoesBehindIt: with a message queued for a machine, a
// new send to it is queued behind it rather than sent at once, so the two
// arrive in the order they were sent.
func TestASendWhileMailWaitsGoesBehindIt(t *testing.T) {
	hub, far, _ := startHubAndGatedFar(t)
	_, farWin, _ := twoWindowSession(t, far.daemon, "far")
	waitForHostUp(t, hub, "build")
	// Only the first message fails, three times: the send, the kick after it,
	// and the first retry, so it still waits however late the second send is.
	// The second would go through at once if it were sent.
	failFirstCalls(hub, 3, "first")
	sp, _ := GetSocketPath()
	local := dialVerb(t, sp)

	first := result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "to": farWin, "from": "planner", "text": "first"}))
	if first["queued"] != true {
		t.Fatalf("the first send was not queued: %v", first)
	}
	// The kick after the first send fails as well, which leaves it waiting
	// for a backoff retry.
	deadline := time.Now().Add(5 * time.Second)
	for !retryPending(hub, "build") {
		if time.Now().After(deadline) {
			t.Fatal("the kick after the first send never ended")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hub.outbox.count("build") != 1 {
		t.Fatalf("the first message does not wait: %d queued", hub.outbox.count("build"))
	}
	second := result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "to": farWin, "from": "planner", "text": "second"}))
	if second["queued"] != true {
		t.Errorf("ASSERTION: a send while mail waits went at once: %v", second)
	}
	waitForFarTexts(t, far, "first|second")
}
