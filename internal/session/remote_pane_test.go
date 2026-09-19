package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
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

// TestAPaneOnAnotherMachineCarriesBytesBothWays is the whole feature in one
// test: a process is started over there, what it prints arrives here, and what
// is typed here reaches it.
//
// Negative control: with the takeover in verbOpenPane removed the open still
// succeeds and this fails with nothing read, because the reply is the last
// thing that connection would ever carry.
func TestAPaneOnAnotherMachineCarriesBytesBothWays(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	_ = d
	fed := &socketFederation{socketPath: socketPath}

	p := openTestPane(t, fed, hostedPaneSpec{
		Width: 80, Height: 24,
		Command: []string{"/bin/sh", "-c", "echo pane-is-up; exec cat"},
	})
	out := drainPane(p)

	if got := out.waitFor("pane-is-up", paneBudget); !strings.Contains(got, "pane-is-up") {
		t.Fatalf("nothing the far process printed arrived: %q", got)
	}
	if _, err := p.Write([]byte("typed-here\n")); err != nil {
		t.Fatalf("write to the far pane: %v", err)
	}
	if got := out.waitFor("typed-here", paneBudget); !strings.Contains(got, "typed-here") {
		t.Fatalf("what was typed here never came back from the far process: %q", got)
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

// TestASessionWithNoLinksRefusesAWindowElsewhere, naming the machine and the
// table it is missing from rather than failing at the spawn.
func TestASessionWithNoLinksRefusesAWindowElsewhere(t *testing.T) {
	s := &Session{Name: "work"}
	_, err := s.openRemotePaneFor("build", 80, 24, "", nil)
	if err == nil {
		t.Fatal("a daemon with no links opened a window on another machine")
	}
	if !strings.Contains(err.Error(), "build") || !strings.Contains(err.Error(), "hosts") {
		t.Errorf("the refusal names neither the machine nor where to add it: %v", err)
	}
}

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

// TestAWindowOnAnotherMachineIsAnOrdinaryWindow walks the whole path a person
// takes: ask a session for a window on another machine, and get a window.
//
// It is the claim the design rests on. The window is in this session's state,
// it records where its process is, and the pane behind it is a PTY like any
// other, which is what lets every surface above the paneIO seam stay unaware
// that the process is not here.
func TestAWindowOnAnotherMachineIsAnOrdinaryWindow(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("global", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host:    "build",
		Command: []string{"/bin/sh", "-c", "echo running-elsewhere; exec cat"},
	}, func(string) {})
	if err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}
	if win.Host != "build" {
		t.Errorf("the window does not record the machine its process is on: host %q", win.Host)
	}

	found := false
	for _, w := range sess.GetState().Windows {
		if w.ID == win.ID {
			found = true
			if w.Host != "build" {
				t.Errorf("the session's own state lost the window's machine: %q", w.Host)
			}
		}
	}
	if !found {
		t.Fatal("the window is not in the session it was created in")
	}

	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the window has no pane")
	}
	if got := waitForPaneText(t, pty, "running-elsewhere", 10*time.Second); !strings.Contains(got, "running-elsewhere") {
		t.Errorf("the far process's output never reached this session's emulator: %q", got)
	}
}

// TestAPaneWhoseFarProcessExitsClosesItsWindow.
//
// A pane on another machine has no process here, so nothing here can wait on
// one. monitorExit returns at once for it, and without a second path the
// window outlived its shell: it stayed on screen, took keystrokes and answered
// nothing, and wait-for window-exit never resolved.
//
// The stream ending is the only notice that crosses, and it covers all three
// ways a far pane can end: the process exits, the owner hangs up, or the link
// drops.
//
// Negative control: removing the noteExit call from readOutput fails here,
// waiting out the budget with the window still open.
func TestAPaneWhoseFarProcessExitsClosesItsWindow(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("global-exit", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	exited := make(chan string, 1)
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host:    "build",
		Command: []string{"/bin/sh", "-c", "exit 0"},
	}, func(ptyID string) { exited <- ptyID })
	if err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}

	select {
	case got := <-exited:
		if got != win.PTYID {
			t.Errorf("a different pane was reported as exited: %q, want %q", got, win.PTYID)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the far process exited and the window was never told, so it would stay open around nothing")
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

// TestTheFilesOfAPaneOnAnotherMachineAreNotThisMachines.
//
// The rail's file section asks the daemon that owns the pane, which was the
// whole fix for a pane reached over a link: the client was listing its own
// disk and reporting that the pane's directory did not exist. A window whose
// process is elsewhere brings the same mistake back one level up, because now
// the daemon that owns the window is not the machine that owns the files
// either. It says so instead of answering about the wrong disk.
//
// Negative control: without the host check this lists the temporary directory
// and reports a successful listing of a path the pane has never been in.
func TestTheFilesOfAPaneOnAnotherMachineAreNotThisMachines(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("global-files", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	// A directory that does exist here, so a daemon that listed its own disk
	// would succeed rather than fail, which is the failure being guarded.
	here := t.TempDir()
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host:    "build",
		Command: []string{"/bin/sh"},
	}, func(string) {})
	if err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}

	if got := d.windowHost(sess.ID, win.ID); got != "build" {
		t.Fatalf("ASSERTION: the window does not report a host (%q), so this proves nothing", got)
	}
	if got := d.windowHost(sess.ID, "no-such-window"); got != "" {
		t.Errorf("an unknown window reported host %q", got)
	}

	// The local half still answers, so the guard is about the pane's machine
	// rather than about turning the section off.
	if out := listDir(here, 0); out.Err != "" {
		t.Errorf("a directory on this machine no longer lists: %s", out.Err)
	}
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
	_, _ = sess.openRemotePaneFor("build", 80, 24, "", nil)
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

// TestAPaneOnAnotherMachineReportsWhereItIs.
//
// The rail's file section needs a directory before it can ask for a listing,
// and a pane on another machine has none of the usual sources: there is no
// process here to read, and a shell that never emits OSC 7, which bash and zsh
// mostly do not, announces nothing. So the machine running it is asked, and it
// is the only one that can answer.
//
// Without this the section said "no directory yet" for every remote pane, for
// its whole life.
func TestAPaneOnAnotherMachineReportsWhereItIs(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	fed := &socketFederation{socketPath: socketPath}

	home := t.TempDir()
	p := openTestPane(t, fed, hostedPaneSpec{
		Width: 80, Height: 24, Cwd: home,
		Command: []string{"/bin/sh"},
	})
	_ = d

	// Compared after resolving links: the kernel reports the real path, and on
	// macOS the temp root reaches it through a symlink.
	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(paneBudget)
	for {
		if cwd, ok := p.Cwd(); ok {
			got, err := filepath.EvalSymlinks(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("the far machine says the pane is in %q, want %q", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the far machine never said where the pane's process is")
		}
		time.Sleep(20 * time.Millisecond)
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

// TestTheFarMachineListsItsOwnDirectory is the other half: the machine with
// the process is the machine with the files.
func TestTheFarMachineListsItsOwnDirectory(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	_ = d
	fed := &socketFederation{socketPath: socketPath}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "only-over-there.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := fed.Call(ctx, "build", "read-dir", map[string]any{"dir": dir})
	if err != nil {
		t.Fatalf("ask the far machine to list a directory: %v", err)
	}
	var out DirListingPayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("read the listing: %v", err)
	}
	found := false
	for _, e := range out.Entries {
		if e.Name == "only-over-there.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("the listing does not hold the file that is there: %+v", out)
	}
}

// TestAPushCarriesARemotePanesDirectory.
//
// The clients are given a snapshot, and for a pane on another machine that
// snapshot is the only place its directory can come from: there is no process
// here to read and its shell announces nothing. So the push has to carry what
// a verb would read, and for a while it did not. GetState filled the directory
// in and publishState did not, so `tuios list-windows` reported it and the
// client drawing the same pane was never told.
//
// Negative control: removing fillLiveFacts from publishState fails here with
// an empty directory on every push.
func TestAPushCarriesARemotePanesDirectory(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("pushes", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	var mu sync.Mutex
	seen := ""
	sess.SetStateSink(func(state *SessionState) {
		mu.Lock()
		defer mu.Unlock()
		for _, w := range state.Windows {
			if w.Host != "" && w.Cwd != "" {
				seen = w.Cwd
			}
		}
	})

	home := t.TempDir()
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host:    "build",
		Cwd:     home,
		Command: []string{"/bin/sh"},
	}, func(string) {}); err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}

	want, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(paneBudget)
	for {
		mu.Lock()
		got := seen
		mu.Unlock()
		if got != "" {
			resolved, err := filepath.EvalSymlinks(got)
			if err != nil {
				t.Fatal(err)
			}
			if resolved != want {
				t.Fatalf("a push carried directory %q, want %q", resolved, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no push ever carried the remote pane's directory, so no client could learn it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAPaneThatChangesDirectoryOnAnotherMachineSaysSo.
//
// The first answer used to be the only one. The ask was made from the snapshot
// path, so it only happened when something else caused a snapshot, and nothing
// does when a shell somewhere else runs cd: the window set, the layout and the
// names are all as they were. The rail showed the directory the pane started
// in for as long as it lived.
//
// What is watched here is the pushes, not GetState. Calling GetState is itself
// an ask, so a test that polls it drives the very refresh it is checking for
// and passes with the feature removed. That is exactly what the first version
// of this did.
//
// This does not fail when the explicit refresh is removed, and that is worth
// saying rather than hiding: other work on the output path mutates the session
// and every mutation asks, so the answer arrives by accident. Accident is the
// problem. A pane whose output causes no mutation kept its first directory for
// as long as it lived, which is what was reported. The hook is pinned where it
// can fail, in TestOutputAsksWhereARemotePaneIsAtMostOncePerSecond.
func TestAPaneThatChangesDirectoryOnAnotherMachineSaysSo(t *testing.T) {
	d, socketPath := startTestDaemon(t)
	sess, err := d.manager.CreateSession("cd-elsewhere", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session: %v", err)
	}
	sess.SetFederation(&socketFederation{socketPath: socketPath})

	var mu sync.Mutex
	pushed := map[string]bool{}
	sess.SetStateSink(func(state *SessionState) {
		mu.Lock()
		defer mu.Unlock()
		for _, w := range state.Windows {
			if w.Host != "" && w.Cwd != "" {
				if got, err := filepath.EvalSymlinks(w.Cwd); err == nil {
					pushed[got] = true
				}
			}
		}
	})
	sawPushed := func(want string) bool {
		mu.Lock()
		defer mu.Unlock()
		return pushed[want]
	}

	home := t.TempDir()
	sub := filepath.Join(home, "inner")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Host: "build", Cwd: home, Command: []string{"/bin/sh"},
	}, func(string) {})
	if err != nil {
		t.Fatalf("create a window on another machine: %v", err)
	}
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("the window has no pane")
	}

	wantHome, _ := filepath.EvalSymlinks(home)
	waitUntil(t, func() bool { return sawPushed(wantHome) },
		"no push ever carried where the pane started")

	// cd, then print. The print is the only thing that crosses, and it is what
	// tells this machine to ask again.
	if _, err := pty.Write([]byte("cd " + sub + "\npwd\n")); err != nil {
		t.Fatalf("send cd: %v", err)
	}

	wantSub, _ := filepath.EvalSymlinks(sub)
	waitUntil(t, func() bool { return sawPushed(wantSub) },
		"the pane changed directory and no push ever said so")
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

// TestALocalPaneIsNotAskedAnotherMachine. The resolver has to tell the two
// apart, or every ordinary pane would take a round trip it does not need.
func TestALocalPaneIsNotAskedAnotherMachine(t *testing.T) {
	pty := &PTY{}
	if _, _, remote := pty.remoteForeground(); remote {
		t.Error("a pane on this machine was treated as one on another")
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
