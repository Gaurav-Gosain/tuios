package session

import (
	"context"
	"encoding/json"
	"errors"
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

// TestMailForAMachineWhoseLinkIsDownWaitsAndGoes is the feature: a send while
// the link is up is delivered at once; a send while it is down is queued, said
// so in the reply, in list-hosts and in the Inbox, kept on disk, and delivered
// when the link is back.
func TestMailForAMachineWhoseLinkIsDownWaitsAndGoes(t *testing.T) {
	hub, far, down := startHubAndGatedFar(t)
	_, farWin, _ := twoWindowSession(t, far.daemon, "far")
	waitForHostUp(t, hub, "build")
	sp, err := GetSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	local := dialVerb(t, sp)

	sent := result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "to": farWin, "from": "planner", "text": "while up"}))
	if sent["queued"] != false || sent["host"] != "build" || sent["message_id"] == nil {
		t.Fatalf("a send with the link up was not delivered: %v", sent)
	}

	down.Store(true)
	far.breakLink()
	waitForHostDown(t, hub, "build")
	queued := result(t, callVerb(t, local, "send-agent-message", map[string]any{"host": "build", "session": "far", "to": farWin, "from": "planner", "text": "while down"}))
	if queued["queued"] != true || queued["waiting"] != float64(1) {
		t.Fatalf("ASSERTION: a send with the link down was not queued: %v", queued)
	}
	hosts := result(t, callVerb(t, local, "list-hosts", map[string]any{}))
	if h := hosts["hosts"].([]any)[0].(map[string]any); h["queued"] != float64(1) {
		t.Errorf("list-hosts does not count the queued message: %v", h)
	}
	item := hubOutboxItem(t, hub)
	if item == nil || item["for_host"] != "build" || !strings.Contains(item["summary"].(string), "1 message waits for the link to build") {
		t.Errorf("the Inbox does not say mail waits for build: %v", item)
	}
	if info, err := os.Stat(outboxPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the outbox is not on disk, owner only: %v %v", info, err)
	}

	down.Store(false)
	deadline := time.Now().Add(30 * time.Second)
	for {
		var got []string
		for _, m := range farMessages(t, far, "far") {
			got = append(got, m["text"].(string))
			if m["text"] == "while down" && m["origin"] != AgentOriginLink {
				t.Errorf("the queued message is not marked as from another machine: %v", m)
			}
		}
		if strings.Join(got, "|") == "while up|while down" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queued message never arrived, or out of order: %v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for hubOutboxItem(t, hub) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the outbox item stayed open after the mail was delivered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := hub.outbox.count("build"); n != 0 {
		t.Errorf("%d messages still wait after delivery", n)
	}
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
