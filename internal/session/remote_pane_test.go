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

// paneReader drains a pane from the moment it is opened and keeps everything
// it saw.
//
// One reader for the pane's whole life, rather than one per wait, is the point.
// A reader started per wait either stops mid-chunk and loses the rest or, when
// its wait times out, goes on running and takes the bytes the next wait is
// looking for. The second is a test that fails somewhere other than where it
// broke, which is the worst kind, and it is what happened here under a loaded
// machine before this existed.
type paneReader struct {
	mu   sync.Mutex
	seen strings.Builder
	done chan struct{}
}

func drainPane(p *remotePane) *paneReader {
	r := &paneReader{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		buf := make([]byte, 4096)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.seen.Write(buf[:n])
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *paneReader) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seen.String()
}

// waitFor returns once want has appeared, or the whole transcript when the
// budget runs out, so a failure can print what did arrive.
func (r *paneReader) waitFor(want string, budget time.Duration) string {
	deadline := time.Now().Add(budget)
	for {
		if got := r.text(); strings.Contains(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			return r.text()
		}
		time.Sleep(10 * time.Millisecond)
	}
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

// TestTheFarPaneIsSizedByTheAskingLayout.
//
// The size a pane is drawn at is decided by the layout that owns the window,
// which is on this side, and it has to reach the process on the other side or
// every full-screen program in it is wrong. This asks the shell what size its
// terminal is, resizes from here, and asks again.
//
// It also pins why resize is a verb on its own connection rather than bytes on
// the pane's: the pane's connection is carrying the shell's own output at the
// time, and nothing in it is framed.
//
// Negative control: with verbResizePane returning success without calling
// resize, the second answer is still the first size and this fails.
func TestTheFarPaneIsSizedByTheAskingLayout(t *testing.T) {
	_, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}

	p := openTestPane(t, fed, hostedPaneSpec{
		Width: 80, Height: 24,
		Command: []string{"/bin/sh"},
	})
	out := drainPane(p)

	if _, err := p.Write([]byte("stty size\n")); err != nil {
		t.Fatalf("ask the far shell for its size: %v", err)
	}
	if got := out.waitFor("24 80", paneBudget); !strings.Contains(got, "24 80") {
		t.Fatalf("the far pane did not start at the size it was asked for: %q", got)
	}

	if err := p.Resize(111, 37); err != nil {
		t.Fatalf("resize the far pane: %v", err)
	}
	if _, err := p.Write([]byte("stty size\n")); err != nil {
		t.Fatalf("ask the far shell again: %v", err)
	}
	if got := out.waitFor("37 111", paneBudget); !strings.Contains(got, "37 111") {
		t.Fatalf("the resize never reached the far pty: %q", got)
	}
}

// TestAHostedPaneBelongsToNoSessionOnTheMachineItRunsOn.
//
// This is the property the whole shape was chosen for, so it is pinned rather
// than left to the comments. A session's size is the minimum over its attached
// clients. Had a pane been borrowed from a session on the hosting machine, a
// layout on this machine would be setting the size of a session someone over
// there is working in, and their panes would shrink to fit a window they
// cannot see.
//
// Negative control: implementing open-pane by attaching a session and adding a
// window to it fails here with that session listed.
func TestAHostedPaneBelongsToNoSessionOnTheMachineItRunsOn(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}

	before := len(d.manager.ListSessions())
	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Command: []string{"/bin/sh"}})
	out := drainPane(p)
	// The shell printing its first prompt is proof the far side has finished
	// doing whatever hosting a pane makes it do.
	out.waitFor("$", paneBudget)

	if after := len(d.manager.ListSessions()); after != before {
		t.Errorf("hosting a pane created a session on the far machine: %d sessions, was %d", after, before)
	}
	for _, s := range d.manager.AllSessions() {
		if n := len(s.GetState().Windows); n != 0 {
			t.Errorf("the far machine put the hosted pane in session %q, which now has %d windows", s.Name, n)
		}
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

// TestAResizeForAPaneThatEndedSaysSo. The process exiting and a resize racing
// it is ordinary, and the caller has to be able to tell "gone" from "wrong
// parameter" to know whether to close the window or fix the call.
func TestAResizeForAPaneThatEndedSaysSo(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}

	p := openTestPane(t, fed, hostedPaneSpec{Width: 80, Height: 24, Command: []string{"/bin/sh"}})
	id := p.id
	_ = p.Close()
	// The close is heard on the far side's own goroutine, so the resize has to
	// come after that has happened rather than race it.
	waitGone(t, d, id, paneBudget)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := fed.Call(ctx, "build", "resize-pane", map[string]any{"pane": id, "width": 10, "height": 10})
	var verr *verbError
	if !errors.As(err, &verr) || verr.Code != ErrVerbUnknownPane {
		t.Fatalf("resizing a pane that ended answered %v, want code %q", err, ErrVerbUnknownPane)
	}
}

// TestADaemonTooOldToHostAPaneSaysWhatToDo. The far machine answers an unknown
// verb with an error rather than a closed socket, so the message can name the
// remedy instead of reporting a broken link.
func TestADaemonTooOldToHostAPaneSaysWhatToDo(t *testing.T) {
	reply := `{"id":1,"error":{"code":"` + ErrVerbUnknownVerb + `","message":"unknown verb"}}` + "\n"
	_, _, err := openPaneOn(&scriptedStream{reply: reply}, hostedPaneSpec{Width: 80, Height: 24})
	if err == nil {
		t.Fatal("an old daemon's refusal was read as a working pane")
	}
	if !strings.Contains(err.Error(), "too old") || !strings.Contains(err.Error(), "kill-server") {
		t.Errorf("the message does not say what to do about an old daemon: %v", err)
	}
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

// TestARestoredWindowStopsClaimingAnotherMachine.
//
// Resurrection respawns a shell on this machine from saved state. It does not
// dial a host, and it runs at daemon start when no link is up yet, so a window
// whose process used to be elsewhere comes back here.
//
// The record has to say so. The frame marks a pane with the machine its shell
// runs on, and the whole value of that mark is that it is believed: a restored
// pane still labelled with another machine is a local shell wearing a remote
// machine's name, which is worse than no mark at all.
//
// Negative control: without the reset in restoreSession the restored window
// still reports the host and this fails.
func TestARestoredWindowStopsClaimingAnotherMachine(t *testing.T) {
	d, _ := startTestDaemon(t)

	state := &SessionState{
		Name:   "restored-global",
		Width:  80,
		Height: 24,
		Windows: []WindowState{{
			ID:     "w1",
			Title:  "deploy",
			Width:  40,
			Height: 12,
			Host:   "build",
			Cwd:    "/srv/only-on-the-build-box",
		}},
	}
	sess, err := d.restoreSession(state)
	if err != nil {
		t.Fatalf("restore the session: %v", err)
	}

	windows := sess.GetState().Windows
	if len(windows) != 1 {
		t.Fatalf("the restored session has %d windows, want 1", len(windows))
	}
	if got := windows[0].Host; got != "" {
		t.Errorf("a window restored on this machine still says its process is on %q", got)
	}
	// The directory reported now is the live one of the shell that was just
	// respawned here, which GetState fills from the process itself. What must
	// not survive is the path from the other machine, which the restore would
	// otherwise have tried to start the shell in.
	if got := windows[0].Cwd; got == "/srv/only-on-the-build-box" {
		t.Errorf("the restored shell was started in the other machine's directory: %q", got)
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

// TestAnInteractiveShellEndingOnCtrlDClosesThePane.
//
// Reported: ctrl+D on a pane running on another machine printed "exit" and
// left the window open, and the window only went when another key was pressed.
//
// The exit of a pane elsewhere is heard where its bytes stop, so this is the
// shape that matters: a shell that is read from, told to end by its input
// rather than by its argv, and whose last act is to print something. The
// existing exit test runs `sh -c "exit 0"`, which never reads and never
// prints, and so cannot see this.
func TestAnInteractiveShellEndingOnCtrlDClosesThePane(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("ctrl-d", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	exited := make(chan string, 1)
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host:    "build",
		Command: []string{"/bin/sh", "-i"},
	}, func(ptyID string) { exited <- ptyID })
	if err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the window has no pane")
	}

	// Wait for the shell to be reading, so the end of file lands on a shell
	// that is listening rather than on one still starting up.
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(pty.CaptureContent(false, false), "$") {
		if time.Now().After(deadline) {
			t.Fatalf("the far shell never prompted:\n%s", pty.CaptureContent(false, false))
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := pty.Write([]byte{0x04}); err != nil {
		t.Fatalf("send end of file: %v", err)
	}

	select {
	case got := <-exited:
		if got != win.PTYID {
			t.Errorf("a different pane was reported as exited: %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the far shell ended and the window was never told:\n%s",
			pty.CaptureContent(false, false))
	}
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

// agentFederation answers pane-agent with a fixed process, so the near side's
// detection path can be driven without a second machine.
type agentFederation struct {
	mu      sync.Mutex
	calls   int
	comm    string
	argv    []string
	running bool
}

func (a *agentFederation) OpenConnection(context.Context, string) (io.ReadWriteCloser, error) {
	return nil, errors.New("not used")
}

func (a *agentFederation) Call(_ context.Context, _, verb string, _ any) (json.RawMessage, error) {
	if verb != "pane-agent" {
		return json.RawMessage(`{}`), nil
	}
	a.mu.Lock()
	a.calls++
	comm, argv, running := a.comm, a.argv, a.running
	a.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"pane": "p1", "running": running, "comm": comm, "argv": argv,
		"pid": 4242, "shell_pid": 4200,
	})
	return body, nil
}

func (a *agentFederation) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// TestWhatAPaneOnAnotherMachineIsRunningReachesTheDetector.
//
// This is the whole point of pane-agent. The daemon that owns the window holds
// the emulator and can read the pane's output all it likes, but the pid it
// would read to find out what is running means nothing on its own machine:
// there is no process there. Every tier of detection starts from the
// foreground process, so without an answer from the far machine a pane on
// another machine can never be identified as running an agent at all.
//
// Negative control: returning early from remoteForeground, so the resolver
// falls through to the local read, gives a shell pid of zero and this fails.
func TestWhatAPaneOnAnotherMachineIsRunningReachesTheDetector(t *testing.T) {
	fed := &agentFederation{comm: "claude", argv: []string{"claude", "--resume"}, running: true}
	rp := &remotePane{host: "build", id: "p1", fed: fed, stream: &scriptedStream{}, br: bufio.NewReader(&scriptedStream{})}
	pty := &PTY{host: "build", pty: rp}

	// The first look never waits: it reports what is cached, which is nothing
	// yet, and starts the ask. That is deliberate, because this runs on the
	// detector's tick and a round trip must not hold it up.
	if _, _, remote := pty.remoteForeground(); !remote {
		t.Fatal("a pane on another machine was not recognised as one")
	}
	waitUntil(t, func() bool { return fed.count() >= 1 }, "the far machine was never asked what the pane is running")

	// And once the answer has landed, it is what the detector sees.
	waitUntil(t, func() bool {
		info, running, _ := pty.remoteForeground()
		return running && info.comm == "claude"
	}, "the answer from the far machine never reached the resolver")

	info, running, _ := pty.remoteForeground()
	if !running {
		t.Fatal("the pane reports nothing running")
	}
	if info.comm != "claude" {
		t.Errorf("the process is %q, want claude", info.comm)
	}
	if len(info.argv) != 2 || info.argv[0] != "claude" {
		t.Errorf("the command line is %v", info.argv)
	}
	if info.shellPID != 4200 {
		t.Errorf("the shell pid is %d, want the one the far machine gave", info.shellPID)
	}
}

// TestThePaneIsNotAskedOncePerTick. The detector ticks every two seconds and
// the answer costs a round trip to another machine, so the cache has to hold
// between ticks.
func TestThePaneIsNotAskedOncePerTick(t *testing.T) {
	fed := &agentFederation{comm: "codex", running: true}
	rp := &remotePane{host: "build", id: "p1", fed: fed, stream: &scriptedStream{}, br: bufio.NewReader(&scriptedStream{})}
	pty := &PTY{host: "build", pty: rp}

	for range 20 {
		pty.remoteForeground()
	}
	waitUntil(t, func() bool { return fed.count() >= 1 }, "the far machine was never asked")
	if got := fed.count(); got > 1 {
		t.Errorf("twenty looks asked the far machine %d times, want one", got)
	}
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
