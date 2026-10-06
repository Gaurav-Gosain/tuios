package app

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestSetProgramFPS pins the one thing frame_rate.go assumes about Bubble Tea
// that its API does not promise: an unexported int field fps that the frame
// ticker starts from, which NewProgram clamps to 120. A release that renames it
// would leave every max_fps above 120 drawing at 120 again, with nothing else
// failing, so this is the test that notices. The same holds for the ticker
// field a change while running resets.
func TestSetProgramFPS(t *testing.T) {
	p := tea.NewProgram(nil, tea.WithFPS(240), tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard))
	fps := reflect.ValueOf(p).Elem().FieldByName("fps")
	if !fps.IsValid() {
		t.Fatal("tea.Program has no fps field; setProgramFPS cannot lift the 120 clamp")
	}
	if got := fps.Int(); got != 120 {
		t.Fatalf("NewProgram left fps at %d for WithFPS(240); the clamp moved, so re-check MaxFPSCap and setProgramFPS", got)
	}
	// The ticker is what a change while running resets.
	if tick := reflect.ValueOf(p).Elem().FieldByName("ticker"); !tick.IsValid() || tick.Type() != reflect.TypeFor[*time.Ticker]() {
		t.Fatal("tea.Program has no *time.Ticker field ticker; a max_fps change would wait for the next start")
	}
	if !setProgramFPS(p, 240) {
		t.Fatal("setProgramFPS did not reach the fps field")
	}
	if got := fps.Int(); got != 240 {
		t.Fatalf("fps is %d after setProgramFPS(240)", got)
	}
}

// kickModel shows the text its last message carried.
type kickModel struct{ text string }

func (k kickModel) Init() tea.Cmd { return nil }

func (k kickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := msg.(string); ok {
		k.text = s
	}
	return k, nil
}

func (k kickModel) View() tea.View { return tea.NewView(k.text) }

// syncBuffer is a bytes.Buffer two goroutines can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestKickFlushWritesTheFrame pins what kickFlush assumes about Bubble Tea:
// that one value sent on the frame ticker's channel makes the renderer write
// the current view at once. A release that renders some other way would leave
// every frame waiting for the next tick again, with nothing else failing.
func TestKickFlushWritesTheFrame(t *testing.T) {
	in, inW := io.Pipe()
	defer func() { _ = inW.Close() }()
	out := &syncBuffer{}
	p := tea.NewProgram(kickModel{}, tea.WithInput(in), tea.WithOutput(out),
		tea.WithoutSignalHandler(), tea.WithWindowSize(80, 24),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}))
	if !setProgramFPS(p, 1) {
		t.Fatal("setProgramFPS did not reach the fps field")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Run()
	}()
	defer func() {
		p.Quit()
		<-done
	}()

	// Wait for Run to start the ticker, and for its first frame to go out.
	deadline := time.Now().Add(3 * time.Second)
	for programTicker(p) == nil || out.String() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the program never started its frame ticker")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// At one frame a second, the next tick is up to a second away.
	time.Sleep(50 * time.Millisecond)
	p.Send("kicked-frame")
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	sendTick(programTicker(p))
	for !strings.Contains(out.String(), "kicked-frame") {
		if time.Since(start) > 300*time.Millisecond {
			t.Fatalf("one value on the ticker's channel did not write the frame within %v; output so far %q",
				time.Since(start), out.String())
		}
		time.Sleep(time.Millisecond)
	}
}
