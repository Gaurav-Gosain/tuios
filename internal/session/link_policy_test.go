package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// What a machine linked to this one may do here. See link_policy.go.

// TestEveryVerbHasALinkPolicy holds the capability table to the registry. A
// verb added without an entry would be refused over every link, and an entry
// for a verb that does not exist is a policy for nothing.
func TestEveryVerbHasALinkPolicy(t *testing.T) {
	for verb := range verbRegistry {
		if _, ok := verbCapabilities[verb]; !ok {
			t.Errorf("verb %s has no entry in verbCapabilities, so it is refused over every link", verb)
		}
	}
	for verb := range verbCapabilities {
		if _, ok := verbRegistry[verb]; !ok {
			t.Errorf("verbCapabilities names %s, which is not a verb", verb)
		}
	}
	for _, t2 := range []MessageType{
		MsgHello, MsgAttach, MsgDetach, MsgNew, MsgList, MsgKill, MsgResurrect, MsgInput, MsgResize,
		MsgCreatePTY, MsgReadDir, MsgClosePTY, MsgUpdateState, MsgSubscribePTY, MsgUnsubscribePTY,
		MsgGetTerminalState, MsgExecuteCommand, MsgCommandResult, MsgGetLogs,
	} {
		if _, ok := msgCapabilities[t2]; !ok {
			t.Errorf("message %d is handled by the daemon and has no link policy", t2)
		}
	}
	for verb, caps := range verbCapabilities {
		for _, c := range caps {
			if c != capRelay && !slices.Contains(config.LinkCapabilities, c) {
				t.Errorf("verb %s needs %q, which is not a capability", verb, c)
			}
		}
	}
}

// TestAPolicyOnlyHostIsNotDialled: [hosts."*"] and a table with a policy and
// no addr say what a machine linking in may do, and are neither dialled nor
// reported as a host with no addr.
func TestAPolicyOnlyHostIsNotDialled(t *testing.T) {
	uc := config.DefaultConfig()
	uc.Hosts = map[string]config.HostConfig{
		"*":      {Allow: []string{"list"}},
		"laptop": {Allow: []string{"list"}},
		"build":  {Addr: "build"},
		"broken": {},
	}
	var names []string
	for _, h := range HostsFromConfig(uc) {
		names = append(names, h.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"broken", "build"}) {
		t.Errorf("the hosts to dial are %v, want broken (to be reported) and build", names)
	}
	if got := DaemonConfigFromUser(uc).LinkPolicies; len(got) != 4 {
		t.Errorf("the daemon was given %d policy entries, want the whole table", len(got))
	}
}

// linkPeer runs the handshake on a link connection, as the proxy does.
func linkPeer(t *testing.T, c *verbConn, peer string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"peer": peer})
	return c.call(t, `{"id":0,"verb":"link-peer","params":`+string(raw)+`}`)
}

// TestTheDefaultLinkPolicyRefusesRespondAndAllowsTheRest is the default: a
// link may do everything it could before the policy existed except answer
// for the person.
func TestTheDefaultLinkPolicyRefusesRespondAndAllowsTheRest(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")

	link := dialLink(t, sp)
	result(t, link.call(t, `{"id":1,"verb":"list-sessions"}`))
	result(t, link.call(t, `{"id":2,"verb":"send-text","params":{"session":"work","window":"`+a+`","text":"x"}}`))
	resp := link.call(t, `{"id":3,"verb":"dismiss-attention","params":{"id":"nothing"}}`)
	mustRefuse(t, resp, ErrVerbForbidden, "dismiss-attention over a link with the default policy")
	hint, _ := resp["error"].(map[string]any)["hint"].(map[string]any)
	detail, _ := hint["detail"].(string)
	if !strings.Contains(detail, `"respond"`) || !strings.Contains(detail, `[hosts."*"]`) {
		t.Errorf("the refusal does not name the capability and the table that grants it: %q", detail)
	}
	// The same verb on the machine's own socket is not a link call.
	local := dialVerb(t, sp)
	if code := errCode(t, local.call(t, `{"id":4,"verb":"dismiss-attention","params":{"id":"nothing"}}`)); code == ErrVerbForbidden {
		t.Errorf("a local caller was refused by the link policy")
	}
}

// TestALinkIsHeldToItsPeersPolicy resolves the policy by the name the
// handshake gave: [hosts.laptop] for laptop, [hosts."*"] for anyone else.
func TestALinkIsHeldToItsPeersPolicy(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{
		"laptop": {Allow: []string{"list"}},
		"*":      {Allow: []string{"list", "write"}},
	})

	laptop := dialLink(t, sp)
	result(t, linkPeer(t, laptop, "laptop"))
	result(t, laptop.call(t, `{"id":1,"verb":"list-windows","params":{"session":"work"}}`))
	mustRefuse(t, laptop.call(t, `{"id":2,"verb":"send-text","params":{"session":"work","window":"`+a+`","text":"x"}}`),
		ErrVerbForbidden, "send-text from laptop, which may only list")

	desk := dialLink(t, sp)
	result(t, linkPeer(t, desk, "desk"))
	result(t, desk.call(t, `{"id":1,"verb":"send-text","params":{"session":"work","window":"`+a+`","text":"x"}}`))
	mustRefuse(t, desk.call(t, `{"id":2,"verb":"new-window","params":{"session":"work"}}`),
		ErrVerbForbidden, `new-window from desk, which [hosts."*"] does not let open`)

	// A link that gave no name is held to [hosts."*"] too.
	anon := dialLink(t, sp)
	mustRefuse(t, anon.call(t, `{"id":1,"verb":"send-agent-message","params":{"session":"work","text":"hi"}}`),
		ErrVerbForbidden, `mail from a link with no name, which [hosts."*"] does not allow`)
}

// TestAPolicyChangeAppliesToALinkAlreadyOpen: tightening the policy does not
// wait for the other machine to reconnect.
func TestAPolicyChangeAppliesToALinkAlreadyOpen(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	link := dialLink(t, sp)
	result(t, link.call(t, `{"id":1,"verb":"list-sessions"}`))
	d.SetLinkPolicies(map[string]config.HostConfig{"*": {Allow: []string{}}})
	mustRefuse(t, link.call(t, `{"id":2,"verb":"list-sessions"}`), ErrVerbForbidden, "list after the policy took list away")
	// hello and list-verbs need nothing, so a caller can still learn why.
	result(t, link.call(t, `{"id":3,"verb":"hello"}`))
}

// TestALinkNamesItsPeerOnceAndFirst: the bytes the hub relays after the
// handshake cannot rename the connection, and a connection that has been used
// cannot name itself afterwards. The machine's own socket cannot name a peer
// at all.
func TestALinkNamesItsPeerOnceAndFirst(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{
		"trusted": {Allow: config.LinkCapabilities},
		"*":       {Allow: []string{"list"}},
	})

	named := dialLink(t, sp)
	result(t, linkPeer(t, named, "laptop"))
	mustRefuse(t, linkPeer(t, named, "trusted"), ErrVerbForbidden, "a second link-peer on one connection")

	used := dialLink(t, sp)
	result(t, used.call(t, `{"id":1,"verb":"list-sessions"}`))
	mustRefuse(t, linkPeer(t, used, "trusted"), ErrVerbForbidden, "link-peer after the connection was used")

	local := dialVerb(t, sp)
	mustRefuse(t, linkPeer(t, local, "trusted"), ErrVerbForbidden, "link-peer on the machine's own socket")

	mustRefuse(t, linkPeer(t, dialLink(t, sp), "no spaces"), ErrVerbInvalidParams, "a peer name that is not a host name")
}

// TestRelayingOnNeedsEveryCapability: open-host-connection over a link would
// let the peer act as this machine toward its own hosts, so a peer allowed
// less than everything here cannot use it.
func TestRelayingOnNeedsEveryCapability(t *testing.T) {
	_, sp := startTestDaemon(t)
	link := dialLink(t, sp)
	mustRefuse(t, link.call(t, `{"id":1,"verb":"open-host-connection","params":{"host":"build"}}`),
		ErrVerbForbidden, "a relay from a link with the default policy")
}

// TestABinaryMessageOnALinkIsHeldToThePolicy covers the attach path: an
// attach needs list and write.
func TestABinaryMessageOnALinkIsHeldToThePolicy(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{"*": {Allow: []string{"list"}}})

	conn, err := net.DialTimeout("unix", LinkSocketPath(sp), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	msg, err := NewMessage(MsgAttach, &AttachPayload{SessionName: "work", Width: 80, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(conn, msg); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply, err := ReadMessage(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if reply.Type != MsgError {
		t.Fatalf("ASSERTION: an attach from a link that may only list was answered with message %d, want an error", reply.Type)
	}
	var ep ErrorPayload
	if err := reply.ParsePayload(&ep); err != nil {
		t.Fatal(err)
	}
	if ep.Code != ErrCodeForbidden || !strings.Contains(ep.Message, `"write"`) {
		t.Errorf("the refusal is %d %q, want forbidden naming write", ep.Code, ep.Message)
	}
	if n := len(d.manager.GetSession("work").GetState().Windows); n == 0 {
		t.Fatal("the session lost its window")
	}
}

// TestDialForLinkNamesThePeer is the proxy's half: the name the hub gave is
// the peer, and --as wins over it.
func TestDialForLinkNamesThePeer(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{
		"laptop": {Allow: []string{"list"}},
		"desk":   {Allow: []string{}},
	})

	conn, err := DialForLink(sp, federation.StreamOpen{From: "laptop"}, "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	vc := &verbConn{conn: conn, r: bufio.NewReader(conn)}
	result(t, vc.call(t, `{"id":1,"verb":"list-sessions"}`))
	mustRefuse(t, linkPeer(t, vc, "trusted"), ErrVerbForbidden, "the hub renaming a connection the proxy named")

	pinned, err := DialForLink(sp, federation.StreamOpen{From: "laptop"}, "desk")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = pinned.Close() })
	pc := &verbConn{conn: pinned, r: bufio.NewReader(pinned)}
	mustRefuse(t, pc.call(t, `{"id":1,"verb":"list-sessions"}`), ErrVerbForbidden,
		"a list from a proxy pinned to desk, which may do nothing, while the hub said laptop")
}

// TestDialForLinkDoesNotFallBackToTheMainSocketOfANewDaemon: a daemon that
// failed to open its link sockets is not reached on its own socket, which
// would give the hub everything.
func TestDialForLinkDoesNotFallBackToTheMainSocketOfANewDaemon(t *testing.T) {
	_, sp := startTestDaemon(t)
	_ = os.Remove(LinkSocketPath(sp))
	_ = os.Remove(LinkHumanSocketPath(sp))
	conn, err := DialForLink(sp, federation.StreamOpen{Human: true}, "")
	if err == nil {
		_ = conn.Close()
		t.Fatal("ASSERTION: the proxy fell back to the main socket of a daemon that holds links to a policy")
	}
	if !errors.Is(err, ErrLinkSocketMissing) {
		t.Errorf("the refusal is %v, want ErrLinkSocketMissing", err)
	}
}

// TestAHubIsHeldToThePolicyTheFarMachineHasForIt runs the whole path: the
// hub names itself on every stream, the far proxy passes the name on, and the
// far daemon holds the hub's calls to [hosts.NAME].
func TestAHubIsHeldToThePolicyTheFarMachineHasForIt(t *testing.T) {
	host, _ := os.Hostname()
	self := linkSelfName(host)
	if self == "" {
		t.Skip("this machine's host name cannot be a peer name")
	}
	hub, far := startHubAndPolicyFar(t, "")
	far.daemon.SetLinkPolicies(map[string]config.HostConfig{
		self: {Allow: []string{"list"}},
	})
	makeSessionWithWindow(t, far.daemon, "far")
	waitForHostUp(t, hub, "build")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := hub.federation.Call(ctx, "build", "list-sessions", nil); err != nil {
		t.Fatalf("a list from the hub was refused: %v", err)
	}
	_, err := hub.federation.Call(ctx, "build", "new-session", map[string]any{"name": "x"})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("ASSERTION: new-session from a hub the far machine lets only list: %v", err)
	}
	if far.daemon.manager.GetSession("x") != nil {
		t.Fatal("the refused call made a session")
	}
}

// startHubAndPolicyFar is startHubAndLinkedFar with the far proxy running
// DialForLink, as tuios stdio-proxy does, pinned to pinned when it is set.
func startHubAndPolicyFar(t *testing.T, pinned string) (*Daemon, *farSide) {
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
	hub := NewDaemon(&DaemonConfig{
		Version:            "hub",
		DisableAutoRestore: true,
		Hosts:              []federation.Host{{Name: "build", Addr: "unused"}},
		HostDial:           far.proxyDialer(pinned),
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("start the hub daemon: %v", err)
	}
	t.Cleanup(hub.Stop)
	return hub, far
}

// proxyDialer is the hub's transport to the far side with the real proxy and
// DialForLink on the other end.
func (f *farSide) proxyDialer(pinned string) federation.Dialer {
	return func(_ context.Context, _ federation.Host) (federation.Transport, error) {
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
		f.mu.Lock()
		f.links = append(f.links, hub, remote)
		f.dials++
		f.mu.Unlock()
		go func() {
			_ = federation.ServeProxyFor(remote, remote, func(open federation.StreamOpen) (net.Conn, error) {
				return DialForLink(f.socket, open, pinned)
			})
			_ = remote.Close()
		}()
		return hub, nil
	}
}

// TestTheIntegratedVerbsAreHeldToTheLinkPolicy covers the verbs the other
// units of the stage added after the policy was written: a machine that may
// only list cannot start an agent, read a worktree's files out, run a line
// at a prompt or answer a question for the person, and one that may open but
// not write still cannot read a worktree out. checkLinkPolicy, the call site
// inside the repository and bundle code, answers the same as the table.
func TestTheIntegratedVerbsAreHeldToTheLinkPolicy(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	d.SetLinkPolicies(map[string]config.HostConfig{
		"viewer": {Allow: []string{"list"}},
		"opener": {Allow: []string{"list", "open"}},
	})

	viewer := dialLink(t, sp)
	result(t, linkPeer(t, viewer, "viewer"))
	for verb, params := range map[string]string{
		"start-agent":     `{"session":"work","agent":"true"}`,
		"bundle-worktree": `{"session":"work"}`,
		"run":             `{"session":"work","window":"` + a + `","command":"true"}`,
		"answer-ask":      `{"request_id":"x","answer":"yes","human_nonce":"n"}`,
	} {
		mustRefuse(t, viewer.call(t, `{"id":1,"verb":"`+verb+`","params":`+params+`}`),
			ErrVerbForbidden, verb+" from a machine that may only list")
	}

	opener := dialLink(t, sp)
	result(t, linkPeer(t, opener, "opener"))
	mustRefuse(t, opener.call(t, `{"id":1,"verb":"bundle-worktree","params":{"session":"work"}}`),
		ErrVerbForbidden, "bundle-worktree from a machine that may open but not write")

	// The call site inside the handlers agrees with the table.
	cs := &connState{viaLink: true, linkPeer: "opener", linkPeerSet: true}
	if verr := d.checkLinkPolicy(cs, linkCapSpawn, "fan"); verr != nil {
		t.Errorf("a clone from a machine that may open was refused: %v", verr.Message)
	}
	if verr := d.checkLinkPolicy(cs, linkCapFiles, "bundle-worktree"); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("reading files out from a machine that may not write was allowed")
	}
	if verr := d.checkLinkPolicy(&connState{}, linkCapFiles, "bundle-worktree"); verr != nil {
		t.Errorf("a caller on this machine was held to a link policy: %v", verr.Message)
	}
}
