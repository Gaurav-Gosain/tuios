package federation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLinkListsRemoteSessions drives the whole stack once: manager, framing,
// proxy, unix socket, stub daemon. Everything below is a failure path off this
// one working case.
func TestLinkListsRemoteSessions(t *testing.T) {
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "9.9.9", PID: 77, Sessions: 2}, nil
		}
		if verb == "list-sessions" {
			return map[string]any{"sessions": []map[string]any{
				{"name": "api", "window_count": 3},
				{"name": "web", "window_count": 1},
			}}, nil
		}
		return nil, &RemoteError{Code: "unknown_verb", Message: verb}
	})
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reports := m.Reports(ctx)
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1", len(reports))
	}
	r := reports[0]
	if r.Status != StatusUp {
		t.Fatalf("host status is %q (%s / %s), want up", r.Status, r.Reason, r.Detail)
	}
	if r.DaemonVersion != "9.9.9" {
		t.Errorf("daemon version is %q, want 9.9.9", r.DaemonVersion)
	}
	if r.Protocol != 1 {
		t.Errorf("protocol is %d, want 1", r.Protocol)
	}
	if r.LastOK == 0 {
		t.Error("last_ok is zero on a host that answered")
	}

	raw, err := m.Call(ctx, "build", "list-sessions", nil)
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	var got struct {
		Sessions []struct {
			Name        string `json:"name"`
			WindowCount int    `json:"window_count"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Sessions) != 2 || got.Sessions[0].Name != "api" || got.Sessions[0].WindowCount != 3 {
		t.Fatalf("remote sessions came back as %+v, want api/3 and web/1", got.Sessions)
	}
}

// TestUnreachableHostReportsPromptly is the powered-off machine. The dial fails,
// and the listing has to come back with a reason rather than waiting on it.
func TestUnreachableHostReportsPromptly(t *testing.T) {
	opts := testOptions(func(context.Context, Host) (Transport, error) {
		return nil, errors.New("dial tcp 10.0.0.9:22: connect: no route to host")
	})
	m := managerFor(t, opts, Host{Name: "build", Addr: "buildbox"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	reports := m.Reports(ctx)
	elapsed := time.Since(start)

	if reports[0].Status != StatusUnreachable {
		t.Fatalf("status is %q, want unreachable", reports[0].Status)
	}
	if !strings.Contains(reports[0].Detail, "no route to host") {
		t.Errorf("detail does not carry the transport's reason: %q", reports[0].Detail)
	}
	if elapsed > 2*time.Second {
		t.Errorf("the listing took %v against a host that refuses instantly", elapsed)
	}
	// Nothing may be callable on it, and the refusal is immediate.
	if _, err := m.Call(ctx, "build", "list-sessions", nil); err == nil {
		t.Fatal("a call against an unreachable host succeeded")
	} else {
		var ue *UnreachableError
		if !errors.As(err, &ue) {
			t.Errorf("call returned %v, want an UnreachableError", err)
		}
	}
}

// TestHostThatAcceptsThenHangsDoesNotBlock is the failure the whole design
// worries about: a machine that answers the TCP connect and then says nothing.
// A build that waited on the preamble would park a goroutine forever and the
// listing would never return.
func TestHostThatAcceptsThenHangsDoesNotBlock(t *testing.T) {
	var mu sync.Mutex
	var transports []*silentTransport
	opts := testOptions(func(context.Context, Host) (Transport, error) {
		s := newSilentTransport()
		mu.Lock()
		transports = append(transports, s)
		mu.Unlock()
		return s, nil
	})
	opts.CallTimeout = 300 * time.Millisecond
	m := managerFor(t, opts, Host{Name: "build", Addr: "buildbox", ConnectTimeout: 400 * time.Millisecond})
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range transports {
			_ = s.Close()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	reports := m.Reports(ctx)
	elapsed := time.Since(start)

	if reports[0].Status != StatusUnreachable {
		t.Fatalf("status is %q (%s), want unreachable", reports[0].Status, reports[0].Reason)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the listing waited %v on a hung host; the connect timeout is 400ms", elapsed)
	}
	if !strings.Contains(reports[0].Reason, "did not answer") {
		t.Errorf("reason does not say the host did not answer: %q", reports[0].Reason)
	}
}

// TestHostThatAnswersThenStopsFailsTheCall covers the other half of hanging: the
// link comes up and the daemon behind it stops answering. The call must give up
// on its own deadline, and the link must be torn down rather than left holding
// a request that can never be matched to a reply.
func TestHostThatAnswersThenStopsFailsTheCall(t *testing.T) {
	// A stub that answers hello and then never answers anything else. The hang
	// is released at cleanup so the fixture's own goroutines can finish; a real
	// wedged daemon would simply never answer.
	hang := make(chan struct{})
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "1.0.0"}, nil
		}
		<-hang
		return nil, &RemoteError{Code: "internal", Message: "test over"}
	})
	// Registered after the stub, so cleanup (which runs last in, first out)
	// releases the hang before the stub waits for its own goroutines.
	t.Cleanup(func() { close(hang) })
	opts := testOptions(proxyDialer(t, stub))
	opts.CallTimeout = 400 * time.Millisecond
	// A long backoff so the supervisor's redial does not race the assertion
	// below about the link having been torn down.
	opts.InitialBackoff = 30 * time.Second
	opts.MaxBackoff = 30 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s / %s), want up before the hang", r.Status, r.Reason, r.Detail)
	}

	start := time.Now()
	_, err := m.Call(ctx, "build", "list-sessions", nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("the call against a wedged daemon succeeded")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the call waited %v on a wedged daemon; the call timeout is 400ms", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("call returned %v, want a deadline", err)
	}

	// The control stream is unusable once a request on it went unanswered: the
	// reply, if it ever comes, would be read as the answer to the next call. So
	// the link is torn down and the host stops reading as up until a redial.
	if r := m.Reports(ctx)[0]; r.Status == StatusUp {
		t.Error("the host still reads as up after a call on it timed out; the wedged link was not torn down")
	}
}

// TestIncompatibleDaemonIsReportedNotUsed is section 8: version skew is normal,
// so a peer outside the served range gets its own state with both numbers on
// it, and no call is made against it.
func TestIncompatibleDaemonIsReportedNotUsed(t *testing.T) {
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 9, MinProtocol: 9, DaemonVersion: "99.0.0"}, nil
		}
		return nil, &RemoteError{Code: "unknown_verb", Message: verb}
	})
	m := managerFor(t, testOptions(proxyDialer(t, stub)), Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := m.Reports(ctx)[0]
	if r.Status != StatusIncompatible {
		t.Fatalf("status is %q (%s), want incompatible", r.Status, r.Reason)
	}
	if r.Protocol != 9 {
		t.Errorf("report does not carry the remote protocol: %d", r.Protocol)
	}
	if !strings.Contains(r.Reason, "9") || !strings.Contains(r.Reason, "1") {
		t.Errorf("reason does not name both protocol versions: %q", r.Reason)
	}
	// The specific error matters. Any failure at all satisfied this once, which
	// would have been satisfied by the link being dead for an unrelated reason.
	_, err := m.Call(ctx, "build", "list-sessions", nil)
	if err == nil {
		t.Fatal("a call was made against an incompatible daemon")
	}
	var ue *UnreachableError
	if !errors.As(err, &ue) {
		t.Fatalf("call returned %v, want an UnreachableError naming the state", err)
	}
	if ue.Status != StatusIncompatible {
		t.Errorf("the call was refused as %q, want incompatible; the caller cannot tell a version problem from a dead machine", ue.Status)
	}
}

// TestReachableHostWithNoDaemonSaysSo is the machine that is up and simply is
// not running tuios. It is not a link failure and it must not read as one.
func TestReachableHostWithNoDaemonSaysSo(t *testing.T) {
	opts := testOptions(func(_ context.Context, _ Host) (Transport, error) {
		hub, remote := duplexPipe(t)
		go func() {
			_ = ServeProxy(remote, remote, func() (net.Conn, error) {
				return nil, errors.New("dial unix /run/user/1000/tuios/tuios.sock: connect: no such file or directory")
			})
		}()
		return hub, nil
	})
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := m.Reports(ctx)[0]
	if r.Status != StatusNoDaemon {
		t.Fatalf("status is %q (%s / %s), want no_daemon", r.Status, r.Reason, r.Detail)
	}
	if !strings.Contains(r.Reason, "daemon") {
		t.Errorf("reason does not mention the daemon: %q", r.Reason)
	}
}

// TestMissingSSHBinaryNamesIt covers the fourth failure the brief asks for: the
// ssh binary is not there. The message has to name the program, because "exec
// format error" against an unnamed path is unanswerable.
func TestMissingSSHBinaryNamesIt(t *testing.T) {
	opts := testOptions(SSHDialer(filepath.Join(t.TempDir(), "no-such-ssh")))
	m := managerFor(t, opts, Host{Name: "build", Addr: "buildbox"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := m.Reports(ctx)[0]
	if r.Status != StatusUnreachable {
		t.Fatalf("status is %q, want unreachable", r.Status)
	}
	if !strings.Contains(r.Detail, "no-such-ssh") {
		t.Errorf("detail does not name the missing program: %q", r.Detail)
	}
}

// TestOneDeadHostDoesNotFailTheListing is the degradation rule: hosts that
// answer are listed, hosts that do not are shown, and neither waits on the
// other.
func TestOneDeadHostDoesNotFailTheListing(t *testing.T) {
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "1.2.3", Sessions: 1}, nil
		}
		if verb == "list-sessions" {
			return map[string]any{"sessions": []map[string]any{{"name": "api"}}}, nil
		}
		return nil, &RemoteError{Code: "unknown_verb", Message: verb}
	})
	live := proxyDialer(t, stub)
	opts := testOptions(func(ctx context.Context, h Host) (Transport, error) {
		if h.Name == "dead" {
			return nil, errors.New("connect: connection refused")
		}
		return live(ctx, h)
	})
	m := managerFor(t, opts,
		Host{Name: "build", Addr: "a"},
		Host{Name: "dead", Addr: "b"},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	answers := m.CallAll(ctx, "list-sessions", nil)
	if len(answers) != 2 {
		t.Fatalf("got %d answers, want 2", len(answers))
	}
	byHost := map[string]Answer{}
	for _, a := range answers {
		byHost[a.Host] = a
	}
	good := byHost["build"]
	if good.Err != nil {
		t.Fatalf("the live host failed: %v", good.Err)
	}
	if !strings.Contains(string(good.Result), "api") {
		t.Errorf("the live host's listing is %q, want it to hold api", good.Result)
	}
	bad := byHost["dead"]
	if bad.Err == nil {
		t.Fatal("the dead host reported success")
	}
	var ue *UnreachableError
	if !errors.As(bad.Err, &ue) {
		t.Fatalf("the dead host failed with %v, want an UnreachableError naming it", bad.Err)
	}
	if ue.Host != "dead" {
		t.Errorf("the failure names host %q, want dead", ue.Host)
	}
	if bad.Report.Status != StatusUnreachable {
		t.Errorf("the dead host's status is %q, want unreachable", bad.Report.Status)
	}
	if len(bad.Result) != 0 {
		t.Errorf("a host that failed still carries a result: %q", bad.Result)
	}
}

// TestHubRefusesAStreamOpenedByTheRemote is invariant 1 of section 1: a remote
// daemon never gets a channel into the hub.
//
// The assertion this test used to make was that the remote's read on the stream
// ended with some error. That is satisfied by the link being torn down at the
// end of the test, so it passed whether the hub refused the stream or simply
// went away, and deleting the refusal left it green. Three things are asserted
// instead, and none of them survives teardown:
//
//  1. Nothing the remote writes into the stream is ever answered.
//  2. The stream ends inside a budget far shorter than the test's own life, so
//     a refusal that arrives only when the link dies is not a refusal.
//  3. The link is still up afterwards and still answers a real verb. That is
//     what separates "the hub closed one stream" from "everything collapsed",
//     and it is the assertion the earlier version was missing.
func TestHubRefusesAStreamOpenedByTheRemote(t *testing.T) {
	hub, remote := duplexPipe(t)

	// refusedIn bounds how long a refusal may take. The hub answers an inbound
	// open from its frame-reading loop, so the real number is microseconds;
	// this is loose enough for a loaded machine and far tighter than anything
	// teardown could produce, since teardown only happens after the assertions
	// below have run.
	const refusedIn = 2 * time.Second

	type refusal struct {
		err error
		// gotBytes is what the hub sent back on the refused stream. A refusal
		// carries no payload; a hub that served the stream would.
		gotBytes int
		// elapsed is how long the stream took to end.
		elapsed time.Duration
	}
	answered := make(chan refusal, 1)
	failed := make(chan error, 1)

	go func() {
		if _, err := io.WriteString(remote, LinkPreamble+"\n"); err != nil {
			failed <- err
			return
		}
		// The remote is the answering end, so it allocates even stream ids and
		// cannot collide with the hub's control stream.
		m := newMux(remote, func(s *Stream) {
			// The hub's control stream. Answer every request line with the
			// handshake or a listing, so the link stays usable.
			br := bufio.NewReader(s)
			for {
				line, err := br.ReadBytes('\n')
				if len(line) == 0 || err != nil {
					return
				}
				var req struct {
					ID   json.RawMessage `json:"id"`
					Verb string          `json:"verb"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				var result any = map[string]any{"sessions": []any{}}
				if req.Verb == "hello" {
					result = Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "1.0.0"}
				}
				resp, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
				if _, werr := s.Write(append(resp, '\n')); werr != nil {
					return
				}
			}
		}, answererFirstID)
		go func() { _ = m.run() }()

		// Wait for the hub's control stream to exist, then push back.
		time.Sleep(200 * time.Millisecond)
		s, err := m.Open()
		if err != nil {
			failed <- err
			return
		}
		// A stream a peer opened is worthless unless something reads it, so the
		// remote writes a request into it. A hub that served the stream would
		// have a handler on the other end of this write.
		_, _ = s.Write([]byte(`{"id":99,"verb":"list-sessions"}` + "\n"))

		started := time.Now()
		buf := make([]byte, 4096)
		n, rerr := s.Read(buf)
		answered <- refusal{err: rerr, gotBytes: n, elapsed: time.Since(started)}
	}()

	opts := testOptions(func(context.Context, Host) (Transport, error) { return hub, nil })
	mgr := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r := mgr.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s / %s), want up", r.Status, r.Reason, r.Detail)
	}

	var got refusal
	select {
	case err := <-failed:
		t.Fatalf("the remote half of the fixture failed: %v", err)
	case got = <-answered:
	case <-time.After(refusedIn):
		t.Fatalf("the hub did not refuse the remote's stream within %v; a stream a peer opened is still open", refusedIn)
	}

	if got.err == nil {
		t.Fatalf("the hub served a stream the remote opened: %d byte(s) came back on it", got.gotBytes)
	}
	if got.gotBytes != 0 {
		t.Errorf("the hub answered %d byte(s) on a refused stream; a refusal carries no payload", got.gotBytes)
	}
	if got.elapsed >= refusedIn {
		t.Errorf("the refusal took %v, which is not a refusal but a timeout", got.elapsed)
	}

	// The link survived the refusal. Without this the whole test is satisfied by
	// the fixture being torn down, which is how it used to pass with the
	// refusal deleted.
	if r := mgr.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("the link is %q after refusing one stream; refusing a stream must not cost the link", r.Status)
	}
	if _, err := mgr.Call(ctx, "build", "list-sessions", nil); err != nil {
		t.Errorf("the link stopped answering after refusing a stream: %v", err)
	}
}

// TestUnknownHostCallIsFinal keeps a mistyped qualifier from reaching a machine.
func TestUnknownHostCallIsFinal(t *testing.T) {
	m := managerFor(t, testOptions(func(context.Context, Host) (Transport, error) {
		return nil, errors.New("should not be dialed")
	}), Host{Name: "build", Addr: "a"})

	_, err := m.Call(context.Background(), "buildbox", "list-sessions", nil)
	if !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("call to an unconfigured host returned %v, want ErrUnknownHost", err)
	}
}

// TestOversizedRemoteResponseIsRefused bounds what an untrusted peer can make
// the hub allocate on the JSON plane, the way the frame test bounds it on the
// wire plane.
func TestOversizedRemoteResponseIsRefused(t *testing.T) {
	huge := strings.Repeat("a", maxResponseLine+16)
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "1.0.0"}, nil
		}
		return map[string]any{"blob": huge}, nil
	})
	opts := testOptions(proxyDialer(t, stub))
	opts.CallTimeout = 3 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if r := m.Reports(ctx)[0]; r.Status != StatusUp {
		t.Fatalf("status is %q (%s), want up", r.Status, r.Reason)
	}
	_, err := m.Call(ctx, "build", "list-sessions", nil)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("an oversized response returned %v, want ErrResponseTooLarge", err)
	}
}

// proxySocket is set only on the child process this file re-executes as the
// remote proxy. A flag rather than an environment variable, because setting one
// in the parent would leak into the parent's own run of the helper.
var proxySocket = flag.String("fed.proxysock", "", "helper process: serve the link proxy against this daemon socket")

// TestHelperStdioProxy is not a test. It is this binary re-executed as the
// remote proxy by TestCommandDialerRunsARealSubprocess, so the framing runs
// over a real process's real stdio.
func TestHelperStdioProxy(t *testing.T) {
	sock := *proxySocket
	if sock == "" {
		t.Skip("helper process only")
	}
	err := ServeProxy(os.Stdin, os.Stdout, func() (net.Conn, error) {
		return net.Dial("unix", sock)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestCommandDialerRunsARealSubprocess proves the transport itself, not just
// the framing: a child process, real pipes, the preamble, and a listing coming
// back through all of it. It is not ssh, and it deliberately touches no ssh
// configuration; ssh adds authentication and its own stdio, which this covers.
func TestCommandDialerRunsARealSubprocess(t *testing.T) {
	stub := startStubDaemon(t, func(verb string, _ json.RawMessage) (any, *RemoteError) {
		if verb == "hello" {
			return Handshake{Protocol: 1, MinProtocol: 1, DaemonVersion: "sub-1"}, nil
		}
		if verb == "list-sessions" {
			return map[string]any{"sessions": []map[string]any{{"name": "over-a-pipe"}}}, nil
		}
		return nil, &RemoteError{Code: "unknown_verb", Message: verb}
	})

	self, err := os.Executable()
	if err != nil {
		t.Skipf("no test executable path: %v", err)
	}
	opts := testOptions(CommandDialer(self,
		"-test.run=TestHelperStdioProxy", "-fed.proxysock="+stub.path))
	opts.CallTimeout = 3 * time.Second
	m := managerFor(t, opts, Host{Name: "build", Addr: "unused"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := m.Reports(ctx)[0]
	if r.Status != StatusUp {
		t.Fatalf("status is %q (%s / %s), want up", r.Status, r.Reason, r.Detail)
	}
	if r.DaemonVersion != "sub-1" {
		t.Errorf("daemon version is %q, want sub-1", r.DaemonVersion)
	}
	raw, err := m.Call(ctx, "build", "list-sessions", nil)
	if err != nil {
		t.Fatalf("list-sessions over a subprocess: %v", err)
	}
	if !strings.Contains(string(raw), "over-a-pipe") {
		t.Fatalf("listing came back as %q", raw)
	}
}
