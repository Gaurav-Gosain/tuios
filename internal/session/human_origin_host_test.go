//go:build !slim && (linux || darwin)

package session

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// helperAttachHost is the attach-host mode of TestHelperSocketCaller: attach
// to a session on another machine through the daemon at sock, the way tuios
// attach --host does. args is "host session".
func helperAttachHost(sock string) string {
	// args is "host session": attach to a session on another machine
	// through the daemon at sock, the way tuios attach --host does.
	host, sessName, _ := strings.Cut(os.Getenv(helperArgsEnv), " ")
	conn, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		return "dial: " + err.Error()
	}
	c := NewTUIClient()
	c.conn = conn
	if _, err := openHostConnectionOn(conn, c.reader(), host); err != nil {
		return "open: " + err.Error()
	}
	if err := c.handshake("test", 80, 24, nil); err != nil {
		return "handshake: " + err.Error()
	}
	if _, err := c.AttachSession(sessName, false, 80, 24); err != nil {
		return "attach: " + err.Error()
	}
	result := "nonce:" + c.HumanNonce()
	_ = c.Close()
	return result
}

// TestADetachedProcessWithAPaneEnvironmentCannotSendAsHuman covers an orphan
// that also left the pane's terminal. What still says where it came from is
// the environment the pane gave it, and a process without it is served as any
// caller outside a pane is.
func TestADetachedProcessWithAPaneEnvironmentCannotSendAsHuman(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "env")
	req := `{"id":1,"verb":"send-agent-message","params":{"session":"env","to":"` + a + `","from":"human","text":"yes"}}`

	run := func(envName, paneID string) map[string]any {
		t.Helper()
		out := filepath.Join(t.TempDir(), "out")
		// sh starts the helper in the background and exits at once, so the
		// helper is reparented away from this process, and setsid leaves it
		// with no controlling terminal.
		cmd := exec.Command("/bin/sh", "-c", helperCommand(t, sp, out, "send", req)+" &")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		env := []string{}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "TUIOS_PANE_ID=") && !strings.HasPrefix(kv, "TUIOS_WINDOW_ID=") && !strings.HasPrefix(kv, "TUIOS_SOCKET=") {
				env = append(env, kv)
			}
		}
		if paneID != "" {
			env = append(env, envName+"="+paneID)
		}
		env = append(env, helperDetachEnv+"=1")
		cmd.Env = env
		if err := cmd.Run(); err != nil {
			t.Fatalf("sh: %v", err)
		}
		var resp map[string]any
		raw := waitHelper(t, out)
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("helper said %q", raw)
		}
		return resp
	}

	// TUIOS_PANE_ID is what a pane's processes carry, TUIOS_WINDOW_ID what the
	// client's hook commands carry, and TUIOS_SOCKET what its dock components
	// carry. Each places the process with the automation, not the person.
	for name, value := range map[string]string{"TUIOS_PANE_ID": b, "TUIOS_WINDOW_ID": b, "TUIOS_SOCKET": sp} {
		resp := run(name, value)
		if e, _ := resp["error"].(map[string]any); e == nil || e["code"] != ErrVerbForbidden {
			t.Errorf("a detached process with %s=%s was not refused: %v", name, value, resp)
		}
	}
	// A window id this daemon does not hold is someone else's pane.
	if resp := run("TUIOS_PANE_ID", "not-a-window-here"); resp["error"] != nil {
		t.Errorf("a pane id this daemon does not hold was refused: %v", resp)
	}
	res := result(t, run("", ""))
	if res["claimed_human"] != true {
		t.Errorf("a process outside every pane was not stored as a claim: %v", res)
	}
}

// TestAPaneReadOfThePersonsInboxIsAPeek covers the quiet version of acting as
// the person: reading their mail marks it read, which clears the unread count
// on their rail. From a pane that read is served as a peek.
func TestAPaneReadOfThePersonsInboxIsAPeek(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "peekh")
	c := dialVerb(t, sp)
	c.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"peekh","to":"human","from":"`+a+`","text":"please look"}}`)

	out := filepath.Join(t.TempDir(), "out")
	req := `{"id":1,"verb":"read-agent-messages","params":{"session":"peekh","to":"human"}}`
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "send", req))
	var resp map[string]any
	if err := json.Unmarshal([]byte(waitHelper(t, out)), &resp); err != nil {
		t.Fatal(err)
	}
	if res := result(t, resp); res["peek_forced"] != true {
		t.Errorf("a pane's read of the person's inbox was not a peek: %v", res)
	}
	unread := result(t, c.call(t, `{"id":2,"verb":"read-agent-messages","params":{"session":"peekh","to":"human","unread":true,"peek":true}}`))
	if n, _ := unread["messages"].([]any); len(n) != 1 {
		t.Errorf("a pane's read marked the person's mail read: %v", unread)
	}
}

// TestAPaneCannotAskAsHuman: an ask records who asked, and an ask from human
// reads as the person asking.
func TestAPaneCannotAskAsHuman(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "askh")
	out := filepath.Join(t.TempDir(), "out")
	req := `{"id":1,"verb":"ask-agent","params":{"session":"askh","window":"` + a + `","from":"human","text":"echo hi"}}`
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "send", req))
	var resp map[string]any
	if err := json.Unmarshal([]byte(waitHelper(t, out)), &resp); err != nil {
		t.Fatal(err)
	}
	if code := errCode(t, resp); code != ErrVerbForbidden {
		t.Errorf("ask-agent from human from a pane: code %q, want %q", code, ErrVerbForbidden)
	}
}

// TestAPaneCannotDismissAttentionWithACopiedNonce holds dismiss-attention to
// the same check as a reply from human. The Inbox is the person's list of what
// waits for them, so emptying it is acting as the person. A pane that got hold
// of the live nonce of the person's client is still refused, and the item
// stays open.
func TestAPaneCannotDismissAttentionWithACopiedNonce(t *testing.T) {
	skipWithoutPeerPID(t)
	d, sp := startTestDaemon(t)
	sess, a, b := twoWindowSession(t, d, "dismissh")
	c := dialVerb(t, sp)
	setAgentState(t, c, "dismissh", a, "needs_input", "approval", "approve Bash: rm -rf build")
	items := waitAttention(t, c, "an approval", hasKind(AttentionApproval, a))
	id := items[0]["id"].(string)

	tui := attachTUI(t, sp, "dismissh")
	out := filepath.Join(t.TempDir(), "out")
	req := `{"id":1,"verb":"dismiss-attention","params":{"id":"` + id + `","human_nonce":"` + tui.HumanNonce() + `"}}`
	runInPane(t, d, sess, b, helperCommand(t, sp, out, "send", req))
	var resp map[string]any
	if err := json.Unmarshal([]byte(waitHelper(t, out)), &resp); err != nil {
		t.Fatal(err)
	}
	if code := errCode(t, resp); code != ErrVerbNotHuman {
		t.Fatalf("dismiss-attention from a pane with the person's nonce: code %q, want %q", code, ErrVerbNotHuman)
	}
	if items, _ := listAttention(t, c, ""); !hasKind(AttentionApproval, a)(items) {
		t.Errorf("a refused dismiss closed the item: %v", items)
	}
}

// startHubAndLinkedFar is startHubAndFar with the far side's proxy doing what
// tuios stdio-proxy does: dial the link-human socket for a stream the hub
// vouched for, and the plain link socket for any other.
func startHubAndLinkedFar(t *testing.T) (*Daemon, *farSide) {
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

	dial := func(_ context.Context, _ federation.Host) (federation.Transport, error) {
		hubR, remoteW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		remoteR, hubW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		hub := &pipeTransport{r: hubR, w: hubW}
		remote := &pipeTransport{r: remoteR, w: remoteW}
		far.mu.Lock()
		far.links = append(far.links, hub, remote)
		far.dials++
		far.mu.Unlock()
		go func() {
			_ = federation.ServeProxyFor(remote, remote, func(open federation.StreamOpen) (net.Conn, error) {
				path := LinkSocketPath(far.socket)
				if open.Human {
					path = LinkHumanSocketPath(far.socket)
				}
				return net.DialTimeout("unix", path, 3*time.Second)
			})
			_ = remote.Close()
		}()
		return hub, nil
	}
	hub := NewDaemon(&DaemonConfig{
		Version:            "hub",
		DisableAutoRestore: true,
		Hosts:              []federation.Host{{Name: "build", Addr: "unused"}},
		HostDial:           dial,
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("start the hub daemon: %v", err)
	}
	t.Cleanup(hub.Stop)
	return hub, far
}

// TestAHubVouchesOnlyForACallerOutsideItsPanes covers the link half of acting
// as the person. The far daemon cannot see who on the hub asked for a
// connection, so the hub says it in the stream's open frame. The person's
// client, outside every pane, attaches with a nonce and its reply verifies. An
// agent in one of the hub's panes that attaches to the same far session gets
// no nonce, so it cannot verify a reply there either.
func TestAHubVouchesOnlyForACallerOutsideItsPanes(t *testing.T) {
	skipWithoutPeerPID(t)
	hub, far := startHubAndLinkedFar(t)
	_, a, _ := twoWindowSession(t, far.daemon, "far")
	hubSess, _, hubPane := twoWindowSession(t, hub, "local")
	waitForHostUp(t, hub, "build")

	person, _ := connectThrough(t, "build", "far")
	nonce := person.HumanNonce()
	if nonce == "" {
		t.Fatal("the person's attach through the hub was issued no nonce")
	}
	vc, _, err := DialVerbClientThroughHost("build", "test")
	if err != nil {
		t.Fatalf("dial through host: %v", err)
	}
	t.Cleanup(func() { _ = vc.Close() })
	raw, err := vc.Call("send-agent-message", map[string]any{"session": "far", "to": a, "from": AgentInboxHuman, "text": "yes", "human_nonce": nonce})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var sent map[string]any
	_ = json.Unmarshal(raw, &sent)
	if sent["verified_human"] != true {
		t.Errorf("the person's reply over the link was not verified: %v", sent)
	}

	hubSock, err := GetSocketPath()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	runInPane(t, hub, hubSess, hubPane, helperCommand(t, hubSock, out, "attach-host", "build far"))
	if got := waitHelper(t, out); got != "nonce:" {
		t.Errorf("an attach from a hub pane through the link got %q, want no nonce", got)
	}
}
