package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	for verb := range verbRegistry() {
		if _, ok := verbCapabilities[verb]; !ok {
			t.Errorf("verb %s has no entry in verbCapabilities, so it is refused over every link", verb)
		}
	}
	for verb := range verbCapabilities {
		if _, ok := verbRegistry()[verb]; !ok {
			t.Errorf("verbCapabilities names %s, which is not a verb", verb)
		}
	}
	for _, t2 := range []MessageType{
		MsgHello, MsgAttach, MsgDetach, MsgNew, MsgList, MsgKill, MsgResurrect, MsgInput, MsgResize,
		MsgCreatePTY, MsgReadDir, MsgClosePTY, MsgUpdateState, MsgSubscribePTY, MsgUnsubscribePTY,
		MsgGetTerminalState, MsgExecuteCommand, MsgCommandResult, MsgGetLogs, MsgClientFocus, MsgLayoutTree,
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
	if verr := d.checkLinkPolicy(cs, linkCapReadOut, "bundle-worktree"); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("reading files out from a machine that may not write was allowed")
	}
	if verr := d.checkLinkPolicy(&connState{}, linkCapReadOut, "bundle-worktree"); verr != nil {
		t.Errorf("a caller on this machine was held to a link policy: %v", verr.Message)
	}
}

// linkBinary sends one binary message on a link connection and returns the
// connection, for a test that reads what comes back.
func linkBinary(t *testing.T, sp string, typ MessageType, payload any) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", LinkSocketPath(sp), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	msg, err := NewMessage(typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(conn, msg); err != nil {
		t.Fatal(err)
	}
	return conn
}

// TestAResurrectOnALinkStaysInTheStateFolder is the binary MsgResurrect from
// another machine. The session name is also the name of the state file, so a
// name with ".." would make the daemon read, archive (move), or restore a
// .json file anywhere the user can reach.
//
// The ways this can fail, written down first:
//   - the name is joined onto the state folder unchecked, so a file outside
//     it is read, and moved to the archive when it is not a saved state;
//   - a file shaped like a saved state is restored, with the grants and the
//     folder it names;
//   - a name with a separator in the middle ("x/../../y") gets past a check
//     that looks only at the start.
func TestAResurrectOnALinkStaysInTheStateFolder(t *testing.T) {
	d, sp := startTestDaemon(t)
	outside := t.TempDir()
	const notState = "not a saved state\n"
	if err := os.WriteFile(filepath.Join(outside, "victim.json"), []byte(notState), 0o600); err != nil {
		t.Fatal(err)
	}
	shaped, err := json.Marshal(SessionState{Name: "planted", Windows: []WindowState{{ID: "w1", Title: "planted", Cwd: outside, Grants: []string{"respond"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "planted.json"), shaped, 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(getResurrectionDir(), outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		filepath.Join(rel, "victim"),
		filepath.Join(rel, "planted"),
		"x/../" + filepath.Join(rel, "victim"),
		// On Windows "..." is the state folder itself.
		"...",
	} {
		conn := linkBinary(t, sp, MsgResurrect, &ResurrectPayload{SessionName: name})
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		reply, err := ReadMessage(conn)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var ep ErrorPayload
		if reply.Type != MsgError || reply.ParsePayload(&ep) != nil || ep.Code != ErrCodeInvalidMessage {
			t.Errorf("ASSERTION: a resurrect of %q from a link was answered with message %d %+v, want invalid_message", name, reply.Type, ep)
		}
	}
	if got, err := os.ReadFile(filepath.Join(outside, "victim.json")); err != nil || string(got) != notState {
		t.Errorf("ASSERTION: a file outside the state folder was moved or changed: %q, %v", got, err)
	}
	if d.manager.GetSession("planted") != nil {
		t.Errorf("ASSERTION: a link restored a session from a file outside the state folder")
	}
	// Names Windows folds onto another file: all dots, a dot at the end, a
	// device. They are refused on every system, so a name moves between
	// machines. The controls are names that only look close.
	for _, name := range []string{"...", "foo.", "con", "NUL.txt", "com1", "Lpt9"} {
		if ValidateSessionName(name) == nil {
			t.Errorf("ASSERTION: the session name %q is allowed", name)
		}
	}
	for _, name := range []string{"foo.bar", "console", "com10", ".hidden"} {
		if err := ValidateSessionName(name); err != nil {
			t.Errorf("the session name %q is refused: %v", name, err)
		}
	}
}

// TestAWatchOnALinkKeepsToTheReadRules is the binary MsgWatchDir from another
// machine. A watch tells when a folder's names change, so it keeps to the
// read rules wait-dir and MsgReadDir keep to.
//
// The ways this can fail, written down first:
//   - the watch on a denied folder starts, and MsgDirChanged tells the link
//     when a key appears in ~/.ssh;
//   - the check refuses every watch, so the rail's files section of a session
//     on another machine stops following its folder (the control below).
func TestAWatchOnALinkKeepsToTheReadRules(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetDenyKeysCache(t)
	_, sp := startTestDaemon(t)
	for _, dir := range []string{".ssh", "work"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	changed := func(dir string) bool {
		conn := linkBinary(t, sp, MsgWatchDir, &WatchDirPayload{Dir: dir})
		// The watch starts off the connection's goroutine. A change made
		// before it is up is not seen, so changes are made until one is or
		// the time is up.
		deadline := time.Now().Add(3 * time.Second)
		for i := 0; time.Now().Before(deadline); i++ {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			if reply, err := ReadMessage(conn); err == nil && reply.Type == MsgDirChanged {
				return true
			}
		}
		return false
	}
	if !changed(filepath.Join(home, "work")) {
		t.Fatal("a watch of an ordinary folder from a link saw no change")
	}
	if changed(filepath.Join(home, ".ssh")) {
		t.Errorf("ASSERTION: a link watched ~/.ssh and was told its names changed")
	}
}

// TestALinkReadsNoCredentialCarrier holds the read deny list to the write
// deny list's credential carriers, and a walk to the read deny list.
//
// The ways this can fail, written down first:
//   - a file the link may not write because it can hold a token
//     (~/.gitconfig, a shell start file, git's credential store) is readable;
//   - a walk of the home folder names a denied file, so a folder copy lists
//     it;
//   - the lists grow so wide that a public key or an ordinary file is refused.
func TestALinkReadsNoCredentialCarrier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetDenyKeysCache(t)
	realHome := realPath(home)
	for _, rel := range linkDenyCredentialHome {
		p := filepath.Join(realHome, filepath.FromSlash(rel))
		if !linkReadDenied(p, realHome, linkPrivate{}) || !linkReadDenied(filepath.Join(p, "x"), realHome, linkPrivate{}) {
			t.Errorf("ASSERTION: ~/%s is in the write deny list as a credential carrier, and a link may read it", rel)
		}
	}

	_, sp := startTestDaemon(t)
	files := map[string]string{
		".ssh/id_ed25519":                   "dummy private key\n",
		".ssh/id_ed25519.pub":               "ssh-ed25519 AAAA dummy\n",
		".gitconfig":                        "[http]\n\textraheader = dummy\n",
		".config/git/credentials":           "https://dummy:dummy@example.invalid\n",
		".config/environment.d/10-app.conf": "TOKEN=dummy\n",
		".bashrc":                           "export TOKEN=dummy\n",
		"notes.txt":                         "ordinary\n",
	}
	for rel, body := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := dialLink(t, sp)
	read := func(id int, rel string) map[string]any {
		raw, _ := json.Marshal(map[string]any{"path": filepath.Join(home, filepath.FromSlash(rel))})
		return link.call(t, fmt.Sprintf(`{"id":%d,"verb":"file-read","params":%s}`, id, raw))
	}
	for i, rel := range []string{".ssh/id_ed25519", ".gitconfig", ".config/git/credentials", ".config/environment.d/10-app.conf", ".bashrc"} {
		mustRefuse(t, read(10+i, rel), ErrVerbForbidden, "a link read ~/"+rel)
	}
	result(t, read(20, ".ssh/id_ed25519.pub"))
	result(t, read(21, "notes.txt"))

	raw, _ := json.Marshal(map[string]any{"path": home})
	walk := result(t, link.call(t, `{"id":30,"verb":"file-walk","params":`+string(raw)+`}`))
	entries, _ := walk["entries"].([]any)
	var names []string
	for _, e := range entries {
		rel, _ := e.(map[string]any)["rel"].(string)
		names = append(names, rel)
	}
	for _, denied := range []string{".ssh", ".ssh/id_ed25519", ".gitconfig", ".config/git/credentials", ".bashrc"} {
		if slices.Contains(names, denied) {
			t.Errorf("ASSERTION: a walk of the home folder over a link named %s: %v", denied, names)
		}
	}
	if !slices.Contains(names, "notes.txt") {
		t.Errorf("a walk of the home folder over a link left out an ordinary file: %v", names)
	}
	// .ssh, .gitconfig, .bashrc, .config/git and .config/environment.d: a
	// denied folder counts once, and nothing is named.
	if n, _ := walk["denied"].(float64); n != 5 {
		t.Errorf("ASSERTION: the walk counts %v entries left out, want 5", walk["denied"])
	}
}
