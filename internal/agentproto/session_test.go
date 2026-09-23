package agentproto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// fakeAgent is an Agent a test drives: Prompt waits for the test to end the
// turn, and the test emits events through the session.
type fakeAgent struct {
	startErr error
	prompts  chan string
	ends     chan TurnResult
	cancels  chan struct{}
	done     chan struct{}
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{prompts: make(chan string, 8), ends: make(chan TurnResult, 8), cancels: make(chan struct{}, 8), done: make(chan struct{})}
}

func (a *fakeAgent) Start(context.Context, string) (Info, error) {
	return Info{Agent: "fake"}, a.startErr
}

func (a *fakeAgent) Prompt(ctx context.Context, text string) (TurnResult, error) {
	a.prompts <- text
	select {
	case r := <-a.ends:
		return r, nil
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	}
}

func (a *fakeAgent) Cancel()               { a.cancels <- struct{}{} }
func (a *fakeAgent) Done() <-chan struct{} { return a.done }

// report is one Report call.
type report struct{ state, kind, message, ifState string }

// fakeReporter records reports, and answers holds with what the test sends.
type fakeReporter struct {
	reports chan report
	holds   chan string
	answers chan [2]string
	ended   chan error
}

func newFakeReporter() *fakeReporter {
	return &fakeReporter{reports: make(chan report, 64), holds: make(chan string, 8), answers: make(chan [2]string, 8), ended: make(chan error, 8)}
}

func (r *fakeReporter) Report(_ context.Context, state, kind, message, ifState string) error {
	r.reports <- report{state, kind, message, ifState}
	return nil
}

func (r *fakeReporter) Hold(ctx context.Context, line string, options []string) (string, string, error) {
	r.holds <- line + " " + strings.Join(options, ",")
	select {
	case a := <-r.answers:
		r.ended <- nil
		return a[0], a[1], nil
	case <-ctx.Done():
		r.ended <- ctx.Err()
		return "", "", ctx.Err()
	}
}

func (r *fakeReporter) next(t *testing.T) report {
	t.Helper()
	select {
	case rep := <-r.reports:
		return rep
	case <-time.After(5 * time.Second):
		t.Fatal("no report")
		return report{}
	}
}

// screen collects what the session writes.
type screen struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *screen) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}

func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.buf.String())
}

func (s *screen) raw() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *screen) waitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.text(), text) {
		if time.Now().After(deadline) {
			t.Fatalf("%q never showed:\n%s", text, s.text())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// harness is a running session with its fakes.
type harness struct {
	s        *Session
	agent    *fakeAgent
	reporter *fakeReporter
	screen   *screen
	keys     *io.PipeWriter
	exit     chan int
	now      time.Time
	nowMu    sync.Mutex
}

func (h *harness) type_(t *testing.T, s string) {
	t.Helper()
	if _, err := io.WriteString(h.keys, s); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) advance(d time.Duration) {
	h.nowMu.Lock()
	h.now = h.now.Add(d)
	h.nowMu.Unlock()
}

func startSession(t *testing.T, agent *fakeAgent) *harness {
	t.Helper()
	in, keys := io.Pipe()
	h := &harness{agent: agent, reporter: newFakeReporter(), screen: &screen{}, keys: keys, exit: make(chan int, 1), now: time.Unix(1000, 0)}
	h.s = &Session{
		Agent:    agent,
		Events:   NewEvents(),
		In:       in,
		Out:      h.screen,
		Reporter: h.reporter,
		Header:   "acp: fake",
		Settle:   500 * time.Millisecond,
		Now: func() time.Time {
			h.nowMu.Lock()
			defer h.nowMu.Unlock()
			return h.now
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = keys.Close()
	})
	go func() { h.exit <- h.s.Run(ctx) }()
	return h
}

// ready starts a session and waits for its idle report.
func ready(t *testing.T) *harness {
	t.Helper()
	h := startSession(t, newFakeAgent())
	if rep := h.reporter.next(t); rep.state != "idle" {
		t.Fatalf("first report %+v, want idle", rep)
	}
	return h
}

// TestSessionTurn: a prompt typed the way tuios types one (a paste and a
// carriage return) is sent, the pane reports working, the reply is shown,
// and the pane reports done with the reply's first line.
func TestSessionTurn(t *testing.T) {
	h := ready(t)
	h.screen.waitFor(t, "connected to fake")
	h.type_(t, pasteStart+"fix the\nbug"+pasteEnd+"\r")
	select {
	case p := <-h.agent.prompts:
		if p != "fix the\nbug" {
			t.Errorf("prompt = %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the prompt was not sent")
	}
	if rep := h.reporter.next(t); rep.state != "working" {
		t.Errorf("report %+v, want working", rep)
	}
	h.screen.waitFor(t, "you  fix the")
	h.s.Emit(Text{Text: "Fixed it.\nDetails follow."})
	h.agent.ends <- TurnResult{Stop: StopFinished}
	if rep := h.reporter.next(t); rep.state != "done" || rep.message != "Fixed it." {
		t.Errorf("report %+v, want done with the reply's first line", rep)
	}
	h.screen.waitFor(t, "turn finished")

	// A turn that fails reports errored with why.
	h.type_(t, "again\r")
	<-h.agent.prompts
	h.reporter.next(t)
	h.agent.ends <- TurnResult{Stop: StopFailed, Detail: "quota exceeded"}
	if rep := h.reporter.next(t); rep.state != "errored" || rep.message != "quota exceeded" {
		t.Errorf("report %+v, want errored", rep)
	}
	// Ctrl+D on an empty line quits.
	h.type_(t, "\x04")
	if code := <-h.exit; code != 0 {
		t.Errorf("exit code %d", code)
	}
}

// TestSessionQueuesAPromptUntilReady: a prompt that arrives before the
// conversation is open is sent once it is.
func TestSessionQueuesAPromptUntilReady(t *testing.T) {
	agent := newFakeAgent()
	gate := make(chan struct{})
	slow := &slowStart{fakeAgent: agent, gate: gate}
	in, keys := io.Pipe()
	rep := newFakeReporter()
	s := &Session{Agent: slow, Events: NewEvents(), In: in, Out: &screen{}, Reporter: rep}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	_, _ = io.WriteString(keys, "early\r")
	select {
	case p := <-agent.prompts:
		t.Fatalf("%q was sent before the agent was ready", p)
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	select {
	case p := <-agent.prompts:
		if p != "early" {
			t.Errorf("prompt = %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued prompt was never sent")
	}
}

type slowStart struct {
	*fakeAgent
	gate chan struct{}
}

func (s *slowStart) Start(ctx context.Context, cwd string) (Info, error) {
	<-s.gate
	return s.fakeAgent.Start(ctx, cwd)
}

func runningTurn(t *testing.T, h *harness) {
	t.Helper()
	h.type_(t, "go\r")
	<-h.agent.prompts
	if rep := h.reporter.next(t); rep.state != "working" {
		t.Fatalf("report %+v", rep)
	}
}

func commandPermission() (*Permission, chan int) {
	chosen := make(chan int, 2)
	p := NewPermission(Tool{ID: "t", Title: "go test ./...", Kind: "execute", Input: map[string]string{"command": "go test ./..."}},
		[]Option{{Label: "Allow once", Decision: DecisionOnce}, {Label: "Always", Decision: ""}, {Label: "Reject", Decision: DecisionDeny}},
		func(i int) { chosen <- i }, func() { chosen <- -1 })
	return p, chosen
}

// TestSessionPermissionFromTheInbox: a permission the Inbox can show is
// reported as needs_input with its line, held with the decisions it offers,
// and the Inbox's answer answers the agent and moves the pane back to
// working.
func TestSessionPermissionFromTheInbox(t *testing.T) {
	h := ready(t)
	runningTurn(t, h)
	p, chosen := commandPermission()
	h.s.Emit(p)
	if rep := h.reporter.next(t); rep != (report{"needs_input", "approval", "approve execute: go test ./...", ""}) {
		t.Errorf("report %+v", rep)
	}
	if held := <-h.reporter.holds; held != "approve execute: go test ./... once,deny" {
		t.Errorf("held %q", held)
	}
	h.screen.waitFor(t, "1 Allow once   2 Always   3 Reject")
	h.reporter.answers <- [2]string{DecisionDeny, "client-7"}
	if i := <-chosen; i != 2 {
		t.Errorf("chose %d, want Reject", i)
	}
	if rep := h.reporter.next(t); rep != (report{"working", "", "", "needs_input"}) {
		t.Errorf("report %+v, want working if still needs_input", rep)
	}
	h.screen.waitFor(t, "answered: Reject (from the Inbox by client-7)")
}

// TestSessionPermissionInThePane: a number key answers once the question has
// been up for Settle, which ends the Inbox hold; a key before that, a paste
// and an Enter answer nothing.
func TestSessionPermissionInThePane(t *testing.T) {
	h := ready(t)
	runningTurn(t, h)
	p, chosen := commandPermission()
	h.s.Emit(p)
	h.reporter.next(t)
	<-h.reporter.holds

	h.type_(t, "1")
	h.type_(t, pasteStart+"1"+pasteEnd+"\r")
	h.advance(time.Second)
	h.type_(t, "9x")
	select {
	case i := <-chosen:
		t.Fatalf("answered %d before the settle, from a paste, Enter or a key with no option", i)
	case <-time.After(150 * time.Millisecond):
	}
	h.type_(t, "1")
	if i := <-chosen; i != 0 {
		t.Errorf("chose %d, want Allow once", i)
	}
	select {
	case err := <-h.reporter.ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the hold ended with %v, want cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the Inbox hold was left running")
	}
	h.screen.waitFor(t, "answered: Allow once (in the pane)")
}

// TestSessionPermissionPaneOnly: a permission the Inbox cannot show whole, a
// diff here, is reported so the Inbox lists it, but never held.
func TestSessionPermissionPaneOnly(t *testing.T) {
	h := ready(t)
	runningTurn(t, h)
	old := "a\n"
	chosen := make(chan int, 1)
	p := NewPermission(Tool{ID: "e", Title: "Edit a.go", Kind: "edit", Diffs: []Diff{{Path: "a.go", Old: &old, New: "b\n"}}},
		[]Option{{Label: "Allow", Decision: DecisionOnce}}, func(i int) { chosen <- i }, func() { chosen <- -1 })
	h.s.Emit(p)
	if rep := h.reporter.next(t); rep.state != "needs_input" || rep.message != "approve edit: Edit a.go" {
		t.Errorf("report %+v", rep)
	}
	h.screen.waitFor(t, "+b")
	select {
	case held := <-h.reporter.holds:
		t.Fatalf("a diff was held for the Inbox: %q", held)
	case <-time.After(100 * time.Millisecond):
	}
	// Ctrl+C answers cancelled and cancels the turn.
	h.type_(t, "\x03")
	if i := <-chosen; i != -1 {
		t.Errorf("Ctrl+C chose %d, want cancel", i)
	}
	select {
	case <-h.agent.cancels:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn was not cancelled")
	}
}

// TestSessionPermissionsQueue: a second permission waits for the first, and a
// turn that ends cancels what is still waiting.
func TestSessionPermissionsQueue(t *testing.T) {
	h := ready(t)
	runningTurn(t, h)
	first, c1 := commandPermission()
	second, c2 := commandPermission()
	h.s.Emit(first)
	h.s.Emit(second)
	h.reporter.next(t)
	<-h.reporter.holds
	h.reporter.answers <- [2]string{DecisionOnce, ""}
	if i := <-c1; i != 0 {
		t.Errorf("first chose %d", i)
	}
	h.reporter.next(t) // working
	if rep := h.reporter.next(t); rep.state != "needs_input" {
		t.Errorf("the second permission reported %+v", rep)
	}
	<-h.reporter.holds
	h.agent.ends <- TurnResult{Stop: StopCancelled}
	if i := <-c2; i != -1 {
		t.Errorf("second chose %d, want cancelled when the turn ended", i)
	}
	for {
		rep := h.reporter.next(t)
		if rep.state == "idle" {
			break
		}
	}
}

// TestSessionAgentExit: when the agent goes, the pane says so with what it
// wrote to stderr, reports errored, and stays until Enter.
func TestSessionAgentExit(t *testing.T) {
	agent := newFakeAgent()
	h := startSession(t, agent)
	h.s.Stderr = func() string { return "panic: \x1b[31mboom" }
	h.reporter.next(t)
	close(agent.done)
	h.screen.waitFor(t, "the agent exited")
	h.screen.waitFor(t, "panic: [31mboom")
	if rep := h.reporter.next(t); rep.state != "errored" {
		t.Errorf("report %+v", rep)
	}
	h.screen.waitFor(t, "Press Enter to close this pane.")
	select {
	case code := <-h.exit:
		t.Fatalf("exited %d before Enter", code)
	case <-time.After(50 * time.Millisecond):
	}
	h.type_(t, "\r")
	if code := <-h.exit; code != 1 {
		t.Errorf("exit code %d", code)
	}
	assertOnlyOwnSGR(t, strings.ReplaceAll(strings.ReplaceAll(h.screen.raw(), "\r\x1b[K", ""), "\r\n", "\n"))
}

func TestSessionStartFails(t *testing.T) {
	agent := newFakeAgent()
	agent.startErr = fmt.Errorf("initialize: bad")
	h := startSession(t, agent)
	if rep := h.reporter.next(t); rep.state != "errored" || !strings.Contains(rep.message, "initialize: bad") {
		t.Errorf("report %+v", rep)
	}
	h.screen.waitFor(t, "the agent did not start: initialize: bad")
}

// TestSessionEchoesNoTypedEscape: a paste with escape sequences in it, which
// anything that can type into the pane could send, is echoed on the prompt
// line and in the transcript without them, so typing into the pane cannot
// make the pane print a sequence its terminal acts on.
func TestSessionEchoesNoTypedEscape(t *testing.T) {
	h := ready(t)
	h.type_(t, pasteStart+"x\x1b]0;title\x07\x1b]9;4;1\x1b\\y"+pasteEnd)
	h.screen.waitFor(t, "x]0;title]9;4;1\\y")
	h.type_(t, "\r")
	<-h.agent.prompts
	h.screen.waitFor(t, "you  x]0;title")
	assertOnlyOwnSGR(t, strings.ReplaceAll(strings.ReplaceAll(h.screen.raw(), "\r\x1b[K", ""), "\r\n", "\n"))
}

// TestSessionRefusesAPromptDuringATurn: Enter during a turn keeps the text
// and says why, rather than starting a second turn.
func TestSessionRefusesAPromptDuringATurn(t *testing.T) {
	h := ready(t)
	runningTurn(t, h)
	h.type_(t, "more\r")
	h.screen.waitFor(t, "a turn is running")
	select {
	case p := <-h.agent.prompts:
		t.Fatalf("%q was sent during a turn", p)
	case <-time.After(50 * time.Millisecond):
	}
	h.agent.ends <- TurnResult{Stop: StopFinished}
	h.screen.waitFor(t, "turn finished")
	h.type_(t, "\r")
	if p := <-h.agent.prompts; p != "more" {
		t.Errorf("prompt = %q, want the kept text", p)
	}
}
