package session

import (
	"bufio"
	"bytes"
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
)

// paneBudget is how long a wait on a far pane is given. It is generous because
// these run beside the rest of the suite, each test of which starts a daemon
// and a shell of its own, and a budget tuned to an idle machine is a test that
// fails on a busy one for no reason of its own.
const paneBudget = 30 * time.Second

// A window whose process runs on another machine, proved against a real daemon
// playing the other machine.
//
// The link is not in the picture on purpose. federation carries the bytes and
// has its own tests; what is unproved without these is the pair of verbs at
// each end and the claim that a paneIO built on them is interchangeable with a
// pty. So the fake federation below dials the far daemon's socket directly,
// which is exactly what the link does after ssh has been taken out of it.

// socketFederation is a paneFederation whose one host is a daemon listening on
// a unix socket in this test.
type socketFederation struct {
	socketPath string
	// openErr, when set, is returned instead of a connection, which is what a
	// host that is down looks like from here.
	openErr error
}

func (f *socketFederation) OpenConnection(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return net.DialTimeout("unix", f.socketPath, 3*time.Second)
}

func (f *socketFederation) Call(_ context.Context, _, verb string, params any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", f.socketPath, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := json.Marshal(verbRequest{ID: json.RawMessage(`1`), Verb: verb, Params: raw})
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	line, err := readLimitedLine(bufio.NewReader(conn), maxRemotePaneReply)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *verbError      `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// openTestPane opens a pane on the daemon d is, through fed.
func openTestPane(t *testing.T, fed paneFederation, spec hostedPaneSpec) *remotePane {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := openRemotePane(ctx, fed, "build", spec)
	if err != nil {
		t.Fatalf("open a pane on the other machine: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestTheFirstBytesOfTheProcessAreNotLost.
//
// The reply line and the process's first output can arrive in one read. The
// reader that consumed the reply is the one that holds them, so it is the one
// the pane must go on reading through; a pane that read the raw stream instead
// would lose whatever the shell printed before anyone looked.
//
// Negative control: returning the bare stream from openPaneOn instead of br
// fails here, missing the banner.
func TestTheFirstBytesOfTheProcessAreNotLost(t *testing.T) {
	reply := `{"id":1,"result":{"type":"pane","pane":"p1"}}` + "\n" + "banner-from-the-shell"
	id, br, err := openPaneOn(&scriptedStream{reply: reply}, hostedPaneSpec{Width: 80, Height: 24})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if id != "p1" {
		t.Errorf("pane id %q, want p1", id)
	}
	p := &remotePane{host: "build", id: id, stream: &scriptedStream{}, br: br}
	buf := make([]byte, 64)
	n, _ := p.Read(buf)
	if string(buf[:n]) != "banner-from-the-shell" {
		t.Errorf("the process's first bytes were dropped with the reply: got %q", string(buf[:n]))
	}
}

// scriptedStream is a far side that answers with a fixed reply and swallows
// everything written to it.
type scriptedStream struct {
	reply string
	read  int
}

func (s *scriptedStream) Read(p []byte) (int, error) {
	if s.read >= len(s.reply) {
		return 0, io.EOF
	}
	n := copy(p, s.reply[s.read:])
	s.read += n
	return n, nil
}
func (s *scriptedStream) Write(p []byte) (int, error) { return len(p), nil }
func (s *scriptedStream) Close() error                { return nil }

// TestASizeFromAnotherMachineIsTreatedAsInput. The number crossed a machine
// boundary and nothing between there and here checked it.
func TestASizeFromAnotherMachineIsTreatedAsInput(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 80},                     // an omitted field, which is the common case
		{-1, 80},                    // nonsense
		{120, 120},                  // ordinary
		{1 << 20, hostedPaneMaxDim}, // a number a pty cannot be asked for
	} {
		if got := clampHostedDim(c.in); got != c.want {
			t.Errorf("clampHostedDim(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestALocalWindowSerialisesWithNoHostField. Every window that existed before
// this feature is a window on this machine, and an older client reading a state
// with a field it does not know is a compatibility question rather than a
// cosmetic one.
func TestALocalWindowSerialisesWithNoHostField(t *testing.T) {
	raw, err := json.Marshal(WindowState{ID: "w1", Title: "sh"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "host") {
		t.Errorf("a window on this machine carries a host field: %s", raw)
	}

	raw, err = json.Marshal(WindowState{ID: "w1", Title: "sh", Host: "build"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"host":"build"`) {
		t.Errorf("a window on another machine does not say which: %s", raw)
	}
}

// waitForPaneText reads the pane's emulator until want shows up on it. It goes
// through the emulator rather than the stream on purpose: what is being proved
// is that a far pane's bytes reach the same screen a local pane's do.
func waitForPaneText(t *testing.T, pty *PTY, want string, budget time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(budget)
	var last string
	for time.Now().Before(deadline) {
		last = pty.CaptureContent(false, false)
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

// TestAskingWhereAPaneIsDoesNotBlockTheCaller.
//
// Cwd is read from GetState, which is on the render path. A call over a link
// takes about as long as a frame does when the link is healthy and much longer
// when it is not, so this answers from the last reply and asks for a fresher
// one in the background. A rail one frame behind on a directory is not a fault
// anybody can see; a rail that stops drawing while it asks is.
//
// Negative control: making Cwd wait for the reply fails here on the budget.
func TestAskingWhereAPaneIsDoesNotBlockTheCaller(t *testing.T) {
	_, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Command: []string{"/bin/sh"}})

	start := time.Now()
	for range 50 {
		p.Cwd()
	}
	// Fifty calls, every one of them answered from what was already known.
	// Even one round trip over the fake link would be slower than this.
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("fifty reads of the pane's directory took %v, so they were waiting on the far machine", took)
	}
}

// waitUntil blocks until cond holds, or fails with why.
func waitUntil(t *testing.T, cond func() bool, why string) {
	t.Helper()
	deadline := time.Now().Add(paneBudget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(why)
}

// TestOutputAsksWhereARemotePaneIsAtMostOncePerSecond pins the explicit ask
// and its throttle.
//
// Both halves matter. Without the ask a pane whose output causes no mutation
// never reports a cd. Without the throttle a pane printing a build log asks
// the far machine once per chunk, which is a network call per frame.
//
// Negative control: dropping the staleness check in Cwd makes the second count
// rise with every call.
func TestOutputAsksWhereARemotePaneIsAtMostOncePerSecond(t *testing.T) {
	counter := &countingFederation{}
	p := &remotePane{host: "build", id: "p1", fed: counter, stream: &scriptedStream{}, br: bufio.NewReader(&scriptedStream{})}
	pty := &PTY{host: "build", pty: p}

	for range 20 {
		pty.refreshRemoteCwdOnOutput()
	}
	// The asks are made on their own goroutines, so this waits for the first
	// rather than assuming it has landed.
	waitUntil(t, func() bool { return counter.calls() >= 1 }, "output never asked where the pane is")
	if got := counter.calls(); got > 1 {
		t.Errorf("twenty chunks asked the far machine %d times, want one", got)
	}
}

// countingFederation records how many times it was asked.
type countingFederation struct {
	mu sync.Mutex
	n  int
}

func (c *countingFederation) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *countingFederation) OpenConnection(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, errors.New("not used")
}

func (c *countingFederation) Call(_ context.Context, _, verb string, _ any) (json.RawMessage, error) {
	if verb == "pane-cwd" {
		c.mu.Lock()
		c.n++
		c.mu.Unlock()
	}
	return json.RawMessage(`{"cwd":"/somewhere"}`), nil
}

// TestAFarDaemonFromBeforeReportsStillOpensAPane is the version skew the other
// way: a far daemon built before reports from hosted panes checks every verb
// line against its schema, which has no window param for open-pane, and refuses
// it with invalid_params. The owner asks again on the same stream without the
// window, the pane opens as it did before, and no report channel is promised.
//
// Negative control: without the retry in openPaneReply, the open fails with
// "verb open-pane has no parameter window".
func TestAFarDaemonFromBeforeReportsStillOpensAPane(t *testing.T) {
	old := verbRegistry["open-pane"]
	old.params = slices.DeleteFunc(slices.Clone(old.params), func(p verbParam) bool { return p.Name == "window" })

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
			var reply []byte
			if verr := checkParamNames(req.Verb, old, req.Params); verr != nil {
				reply, _ = json.Marshal(verbResponse{ID: req.ID, Error: verr})
			} else {
				reply = []byte(`{"id":1,"result":{"type":"pane","pane":"p1"}}`)
			}
			if _, err := far.Write(append(reply, '\n')); err != nil {
				return
			}
			if verr := checkParamNames(req.Verb, old, req.Params); verr == nil {
				return
			}
		}
	}()

	opened, _, err := openPaneReply(owner, hostedPaneSpec{Width: 80, Height: 24, Window: "win-1"})
	if err != nil {
		t.Fatalf("a far daemon from before reports refused the pane: %v", err)
	}
	<-farDone
	if opened.Pane != "p1" {
		t.Errorf("pane %q, want p1", opened.Pane)
	}
	if opened.CallsToken != "" {
		t.Errorf("a pane opened without the window has calls token %q", opened.CallsToken)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], `"window"`) || strings.Contains(seen[1], `"window"`) {
		t.Errorf("the requests were %q, want one with the window and a retry without it", seen)
	}
}

// TestOnlyARefusalOfTheWindowIsRetried: any other open-pane error is the
// answer, and a request that sent no window is not sent again.
func TestOnlyARefusalOfTheWindowIsRetried(t *testing.T) {
	for _, tc := range []struct {
		name string
		verr *verbError
		want bool
	}{
		{"window by hint", hintedVerbError(ErrVerbInvalidParams, "verb open-pane has no parameter window", &VerbHint{Param: "window"}), true},
		{"window by message", newVerbError(ErrVerbInvalidParams, "verb open-pane has no parameter window"), true},
		{"another param", hintedVerbError(ErrVerbInvalidParams, "verb open-pane has no parameter shell", &VerbHint{Param: "shell"}), false},
		{"another code", newVerbError(ErrVerbForbidden, "no parameter window"), false},
		{"no error", nil, false},
	} {
		if got := refusesParam(tc.verr, "window"); got != tc.want {
			t.Errorf("%s: refusesParam(window) = %v, want %v", tc.name, got, tc.want)
		}
	}

	reply := `{"id":1,"error":{"code":"` + ErrVerbInvalidParams + `","message":"verb open-pane has no parameter window","hint":{"param":"window"}}}` + "\n"
	if _, _, err := openPaneReply(&scriptedStream{reply: reply}, hostedPaneSpec{Width: 80, Height: 24}); err == nil {
		t.Error("a request with no window was retried past a refusal of the window")
	}
}

// TestTheAskingMachinesShellDoesNotTravel.
//
// TERM and COLORTERM describe the emulator the program talks to, and that is
// here, so this session's answer is right wherever the process runs. The shell
// is a file that has to exist on the machine running it.
//
// Found by deploying: a laptop running zsh asked a Linux host for a pane and
// the host tried to exec /bin/zsh, which it does not have. Both ends were the
// same machine in every test until then, so the path existed and the fault was
// invisible.
//
// Negative control: putting s.config.Shell back into the spec fails here.
func TestTheAskingMachinesShellDoesNotTravel(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("shell-travel", &SessionConfig{
		Term:      "xterm-256color",
		ColorTerm: "truecolor",
		Shell:     "/bin/zsh-that-is-only-here",
	}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	spec := captureOpenPaneSpec(t, sess)
	if spec.Shell != "" {
		t.Errorf("the asking machine's shell was sent to the far machine: %q", spec.Shell)
	}
	if spec.Term != "xterm-256color" || spec.ColorTerm != "truecolor" {
		t.Errorf("the terminal type did not travel: term=%q colorterm=%q", spec.Term, spec.ColorTerm)
	}
}

// TestClosingAPaneEndsTheProcessOnTheOtherMachine. A shell left running on a
// pty whose owner has gone is a leak no one on either machine can see.
//
// Negative control: without the hp.close in relayHostedPane's read path the
// pane stays in the registry and this fails.
func TestClosingAPaneEndsTheProcessOnTheOtherMachine(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := openRemotePane(ctx, fed, "build", hostedPaneSpec{Width: 80, Height: 24, Command: []string{"/bin/sh"}})
	if err != nil {
		t.Fatalf("open a pane: %v", err)
	}
	if d.lookupHostedPane(p.id) == nil {
		t.Fatalf("ASSERTION: the far daemon is not running the pane it said it opened, so this proves nothing")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("close the pane: %v", err)
	}
	waitGone(t, d, p.id, paneBudget)
}

// captureOpenPaneSpec opens a pane against a federation that records the spec
// instead of a daemon, so the request can be read as it would cross.
func captureOpenPaneSpec(t *testing.T, sess *Session) hostedPaneSpec {
	t.Helper()
	rec := &specRecorder{}
	sess.SetFederation(rec)
	_, _ = sess.openRemotePaneFor("win-1", "build", 80, 24, "", nil)
	if !rec.seen {
		t.Fatal("ASSERTION: no open-pane request was made, so this proves nothing")
	}
	return rec.spec
}

// waitGone blocks until the far daemon has let go of the pane.
//
// Closing a pane is not synchronous and cannot be: the notice is the stream
// ending, the far side hears it on its own goroutine, and only then does it
// kill the process and drop the registration. A test that asserts immediately
// after a close is racing that, which is a property of the design rather than
// of the test.
func waitGone(t *testing.T, d *Daemon, id string, budget time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for d.lookupHostedPane(id) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the far machine still holds pane %s after %v", id, budget)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// specRecorder is a paneFederation that reads the open-pane request and then
// refuses it, which is all this needs: the question is what was asked for.
type specRecorder struct {
	spec hostedPaneSpec
	seen bool
}

func (r *specRecorder) OpenConnection(context.Context, string) (io.ReadWriteCloser, error) {
	return &specStream{rec: r}, nil
}

func (r *specRecorder) Call(context.Context, string, string, any) (json.RawMessage, error) {
	return nil, nil
}

// specStream reads the request line, records its params, and answers with an
// error so the open ends there.
type specStream struct {
	rec   *specRecorder
	reply string
	read  int
}

func (s *specStream) Write(p []byte) (int, error) {
	var req struct {
		Params hostedPaneSpec `json:"params"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(p), &req); err == nil {
		s.rec.spec, s.rec.seen = req.Params, true
	}
	s.reply = `{"id":1,"error":{"code":"internal","message":"recorded"}}` + "\n"
	return len(p), nil
}

func (s *specStream) Read(p []byte) (int, error) {
	if s.read >= len(s.reply) {
		return 0, io.EOF
	}
	n := copy(p, s.reply[s.read:])
	s.read += n
	return n, nil
}

func (s *specStream) Close() error { return nil }
