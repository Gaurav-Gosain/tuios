package agentproto

import (
	"context"
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
	handled  chan struct{}
	now      time.Time
	nowMu    sync.Mutex
}

func (h *harness) type_(t *testing.T, s string) {
	t.Helper()
	if _, err := io.WriteString(h.keys, s); err != nil {
		t.Fatal(err)
	}
}

func startSession(t *testing.T, agent *fakeAgent) *harness {
	t.Helper()
	in, keys := io.Pipe()
	h := &harness{agent: agent, reporter: newFakeReporter(), screen: &screen{}, keys: keys, exit: make(chan int, 1), handled: make(chan struct{}, 64), now: time.Unix(1000, 0)}
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
		keysHandled: func() {
			select {
			case h.handled <- struct{}{}:
			default:
			}
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

func runningTurn(t *testing.T, h *harness) {
	t.Helper()
	h.type_(t, "go\r")
	<-h.agent.prompts
	if rep := h.reporter.next(t); rep.state != "working" {
		t.Fatalf("report %+v", rep)
	}
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
