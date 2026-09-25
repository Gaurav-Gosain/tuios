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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// A connection through a host, proved with two real daemons in one process.
//
// The hub is the daemon this machine's clients dial. The far daemon stands in
// for the daemon on host "build": it listens on its own socket, holds its own
// sessions, and is reached only over the hub's link, which runs the real
// stdio proxy over an in-process pipe instead of ssh. Nothing here reads the
// developer's ssh configuration or touches a network.

// pipeTransport is one end of an os.Pipe pair, as the hub's transport.
type pipeTransport struct {
	r    *os.File
	w    *os.File
	once sync.Once
}

func (p *pipeTransport) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeTransport) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeTransport) Close() error {
	p.once.Do(func() { _ = p.r.Close(); _ = p.w.Close() })
	return nil
}
func (p *pipeTransport) Diagnostic() string { return "" }

// farSide is the daemon on host "build" and the handle that breaks the link
// to it.
type farSide struct {
	daemon *Daemon
	socket string

	mu    sync.Mutex
	links []*pipeTransport
	// dials counts every transport the hub has opened to this side. It is
	// what tells a redial from the link it replaced: the hub's status reads
	// up for the old link until its supervisor has noticed the drop, so a
	// test that wants the new link waits for the dial first.
	dials int
}

// dialCount is how many times the hub has dialed this side so far.
func (f *farSide) dialCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dials
}

// waitForDial blocks until the hub has dialed this side more than n times.
func (f *farSide) waitForDial(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if f.dialCount() > n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the hub never dialed the far side again: %d dial(s), had %d", f.dialCount(), n)
}

// breakLink closes every link transport, which is the ssh child dying.
func (f *farSide) breakLink() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.links {
		_ = l.Close()
	}
	f.links = nil
}

// dialer is the hub's transport to the far side: a pipe pair with the real
// proxy on the other end, dialing the far daemon's socket per stream.
func (f *farSide) dialer(dialFar func() (net.Conn, error)) federation.Dialer {
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
			_ = federation.ServeProxy(remote, remote, dialFar)
			_ = remote.Close()
		}()
		return hub, nil
	}
}

// startHubAndFar starts the two daemons. The hub is the one GetSocketPath
// names, so every client in the test dials it the way a real client would.
func startHubAndFar(t *testing.T) (hub *Daemon, far *farSide) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))

	// A unix socket path is short by law, so the far socket does not go under
	// t.TempDir, whose path carries the test name.
	dir, err := os.MkdirTemp("", "far")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	far = &farSide{socket: filepath.Join(dir, "s")}
	far.daemon = NewDaemon(&DaemonConfig{Version: "far-build", SocketPath: far.socket, DisableAutoRestore: true})
	if err := far.daemon.Start(); err != nil {
		t.Fatalf("start the far daemon: %v", err)
	}
	t.Cleanup(far.daemon.Stop)

	hub = NewDaemon(&DaemonConfig{
		Version:            "hub",
		DisableAutoRestore: true,
		Hosts:              []federation.Host{{Name: "build", Addr: "unused"}},
		HostDial: far.dialer(func() (net.Conn, error) {
			return net.DialTimeout("unix", far.socket, 3*time.Second)
		}),
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("start the hub daemon: %v", err)
	}
	t.Cleanup(hub.Stop)
	return hub, far
}

// waitForHostUp blocks until the hub reports the host up.
func waitForHostUp(t *testing.T, hub *Daemon, host string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		reports := hub.federation.Reports(ctx)
		cancel()
		for _, r := range reports {
			if r.Host == host && r.Status == federation.StatusUp {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	t.Fatalf("the hub never reported %s up: %+v", host, hub.federation.Reports(ctx))
}

// connectThrough is a TUI client attached to a far session through the hub.
func connectThrough(t *testing.T, host, sessionName string) (*TUIClient, *SessionState) {
	t.Helper()
	c := NewTUIClient()
	if _, err := c.ConnectThroughHost(host, "test", 80, 24, nil); err != nil {
		t.Fatalf("connect through %s: %v", host, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	state, err := c.AttachSession(sessionName, false, 80, 24)
	if err != nil {
		t.Fatalf("attach %s through %s: %v", sessionName, host, err)
	}
	c.StartReadLoop()
	return c, state
}

func TestAConnectionToAHostThatIsDownIsRefusedByName(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))
	hub := NewDaemon(&DaemonConfig{
		Version:            "hub",
		DisableAutoRestore: true,
		Hosts:              []federation.Host{{Name: "offline", Addr: "unused"}},
		HostDial: func(context.Context, federation.Host) (federation.Transport, error) {
			return nil, errors.New("ssh: connect to host poweredoff port 22: No route to host")
		},
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("start the hub daemon: %v", err)
	}
	t.Cleanup(hub.Stop)

	started := time.Now()
	c := NewTUIClient()
	_, err := c.ConnectThroughHost("offline", "test", 80, 24, nil)
	var hostErr *HostConnectError
	if !errors.As(err, &hostErr) {
		t.Fatalf("ASSERTION: connecting through a down host returned %v, want HostConnectError", err)
	}
	if hostErr.Code != ErrVerbHostUnreachable || hostErr.Host != "offline" {
		t.Errorf("ASSERTION: the error is %q for %q, want host_unreachable for offline", hostErr.Code, hostErr.Host)
	}
	if !strings.Contains(hostErr.Message, "offline") {
		t.Errorf("ASSERTION: the message does not name the machine: %q", hostErr.Message)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("the refusal took %v; a down host must not be waited on", took)
	}

	_, err = c.ConnectThroughHost("nowhere", "test", 80, 24, nil)
	if !errors.As(err, &hostErr) || hostErr.Code != ErrVerbUnknownHost {
		t.Errorf("ASSERTION: an unknown name returned %v, want unknown_host", err)
	}
}

// TestARoutedCommandFromAHostCannotChangeThisMachine is the untrusted fence on
// the one message a daemon sends that can act on the client's machine. The far
// daemon routes a config write to the attached client, and the client refuses
// it; a keystroke for the session it owns goes through.
func TestARoutedCommandFromAHostCannotChangeThisMachine(t *testing.T) {
	hub, far := startHubAndFar(t)
	makeSessionWithWindow(t, far.daemon, "far-only")
	waitForHostUp(t, hub, "build")

	c, _ := connectThrough(t, "build", "far-only")
	handled := make(chan string, 8)
	c.OnRemoteCommand(func(p *RemoteCommandPayload) error {
		handled <- p.CommandType + ":" + p.TapeCommand
		// The app answers a send_keys itself once the keys are typed; here
		// the answer is immediate so the verb returns.
		return c.SendCommandResult(p.RequestID, true, "typed")
	})

	// The far daemon is asked directly, as an agent on that machine would ask.
	vc := dialVerb(t, far.socket)
	resp := vc.call(t, `{"id":1,"verb":"set-option","params":{"key":"appearance.theme","value":"dracula"}}`)
	res := result(t, resp)
	if applied, _ := res["applied"].(bool); applied {
		t.Fatalf("ASSERTION: a config write from host build was applied on this machine: %v", res)
	}
	reason, _ := res["reason"].(string)
	if !strings.Contains(reason, "refused") || !strings.Contains(reason, "build") {
		t.Errorf("ASSERTION: the refusal does not say what happened and which host: %q", reason)
	}
	select {
	case got := <-handled:
		t.Fatalf("ASSERTION: the client ran a command the fence should have refused: %s", got)
	case <-time.After(300 * time.Millisecond):
	}

	// Positive control: a command about the session is allowed through.
	resp = vc.call(t, `{"id":2,"verb":"send-keys","params":{"keys":"ls"}}`)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("send-keys through the fence failed: %v", resp["error"])
	}
	select {
	case got := <-handled:
		if !strings.HasPrefix(got, "send_keys") {
			t.Errorf("the handler ran %q, want send_keys", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("ASSERTION: an allowed command never reached the handler")
	}
}

// hostCommandFenceNames lists the commands hostCommandAllowed claims to refuse,
// joined by commas.
func hostCommandFenceNames() string {
	refused := []tape.CommandType{
		tape.CommandTypeSetConfig, tape.CommandTypeSetTheme, tape.CommandTypeSetDockbarPosition,
		tape.CommandTypeSetBorderStyle, tape.CommandTypeSet, tape.CommandTypeScreenshot,
		tape.CommandTypeOutput, tape.CommandTypeSaveLayout, tape.CommandTypeLoadLayout,
		tape.CommandTypeSource,
	}
	names := make([]string, 0, len(refused))
	for _, r := range refused {
		names = append(names, string(r))
	}
	return strings.Join(names, ",")
}

func TestTheHostCommandFenceRefusesEveryCommandItNames(t *testing.T) {
	for _, name := range strings.Split(hostCommandFenceNames(), ",") {
		if hostCommandAllowed(&RemoteCommandPayload{CommandType: "tape_command", TapeCommand: name}) {
			t.Errorf("ASSERTION: %s is allowed through a host and the fence says it is refused", name)
		}
	}
	if hostCommandAllowed(&RemoteCommandPayload{CommandType: "set_config", ConfigPath: "appearance.theme"}) {
		t.Errorf("ASSERTION: set_config is allowed through a host")
	}
	if hostCommandAllowed(&RemoteCommandPayload{CommandType: "tape_command", TapeCommand: "SomethingNew"}) {
		t.Errorf("ASSERTION: a command the fence has never heard of is allowed through a host")
	}
	if !hostCommandAllowed(&RemoteCommandPayload{CommandType: "send_keys", Keys: "ls"}) {
		t.Errorf("send_keys is refused, so an agent on the host cannot type into its own session")
	}
	// The fence is only for connections through a host.
	c := NewTUIClient()
	if why := c.refuseHostCommand(&RemoteCommandPayload{CommandType: "set_config"}); why != "" {
		t.Errorf("a local connection refused a command: %s", why)
	}
}

// TestAHostileFarDaemonCannotSizeThisClientsBuffer is the bound on what a
// remote daemon's bytes can make this side allocate. The far side answers the
// hello with a frame header claiming a gigabyte, and the client refuses the
// message rather than reading it in.
func TestAHostileFarDaemonCannotSizeThisClientsBuffer(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Cleanup(useResurrectionDir(t.TempDir()))

	// The "far daemon" is a goroutine on the other end of the proxy's dial. It
	// answers the link's control stream honestly, so the link comes up, and
	// answers a binary client's hello with a frame header claiming a gigabyte.
	far := &farSide{}
	hostile := func() (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			br := bufio.NewReader(server)
			first, err := br.Peek(1)
			if err != nil {
				return
			}
			if first[0] == '{' {
				for {
					line, err := br.ReadBytes('\n')
					if err != nil {
						return
					}
					var req struct {
						ID json.RawMessage `json:"id"`
					}
					_ = json.Unmarshal(line, &req)
					reply, _ := json.Marshal(map[string]any{"id": req.ID, "result": map[string]any{
						"protocol": VerbProtocolVersion, "min_protocol": MinVerbProtocolVersion,
						"daemon_version": "hostile", "sessions": 0,
					}})
					if _, err := server.Write(append(reply, '\n')); err != nil {
						return
					}
				}
			}
			// A binary client. Take its hello, then answer with a length no
			// honest message has, and keep the connection open the way a slow
			// attacker would.
			if _, err := ReadMessageBuffered(server, br, 5*time.Second, 5*time.Second); err != nil {
				return
			}
			hdr := []byte{0x40, 0x00, 0x00, 0x00, byte(MsgWelcome), 0}
			_, _ = server.Write(hdr)
			_, _ = br.Read(make([]byte, 16))
		}()
		return client, nil
	}
	hub := NewDaemon(&DaemonConfig{
		Version:            "hub",
		DisableAutoRestore: true,
		Hosts:              []federation.Host{{Name: "build", Addr: "unused"}},
		HostDial:           far.dialer(hostile),
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("start the hub daemon: %v", err)
	}
	t.Cleanup(hub.Stop)
	waitForHostUp(t, hub, "build")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	c := NewTUIClient()
	_, err := c.ConnectThroughHost("build", "test", 80, 24, nil)
	if err == nil {
		t.Fatalf("ASSERTION: the client accepted a gigabyte frame from host build")
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 64<<20 {
		t.Fatalf("ASSERTION: the client allocated %d bytes for a frame the far side sized", grew)
	}
	if !strings.Contains(err.Error(), "build") || !strings.Contains(err.Error(), "too large") {
		t.Errorf("ASSERTION: the error does not name the machine and the refused frame: %v", err)
	}
	t.Logf("refused with: %v", err)
}

// TestOpenHostConnectionSendsNothingBeforeTheReply pins the client half of the
// protocol: the request line goes out, and the connection is silent until the
// daemon's reply is read. The relay reads whatever the client buffered after
// the request as bytes for the far side, so an early hello would be delivered
// to the far daemon out of order.
func TestOpenHostConnectionSendsNothingBeforeTheReply(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close(); _ = server.Close() }()

	sent := make(chan string, 1)
	go func() {
		br := bufio.NewReader(server)
		line, _ := br.ReadString('\n')
		// Nothing else may be in flight: a second read must block until the
		// reply below is written.
		extra := make(chan int, 1)
		go func() {
			n, _ := br.Read(make([]byte, 1))
			extra <- n
		}()
		// The reply is written either way, so a client that spoke early is
		// reported by the assertion below rather than by its own timeout.
		select {
		case n := <-extra:
			line += " and " + string(rune('0'+n)) + " early byte(s)"
		case <-time.After(200 * time.Millisecond):
		}
		sent <- line
		_, _ = io.WriteString(server, `{"id":1,"result":{"type":"host_connection","host":"build","daemon_version":"x","protocol":1}}`+"\n")
	}()

	info, err := openHostConnectionOn(client, bufio.NewReader(client), "build")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if info.Host != "build" {
		t.Errorf("info = %+v", info)
	}
	line := <-sent
	if strings.Contains(line, "early") {
		t.Fatalf("ASSERTION: the client wrote before the reply: %s", line)
	}
	var req verbRequest
	if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &req); err != nil || req.Verb != "open-host-connection" {
		t.Fatalf("ASSERTION: the request line is %q", line)
	}
}
