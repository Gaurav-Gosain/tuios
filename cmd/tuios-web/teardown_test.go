package main

import (
	"io"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// frameSink is the terminal a test program renders to. It records every write
// and whether one landed after the teardown ran.
type frameSink struct {
	mu    sync.Mutex
	torn  bool
	late  int
	wrote int
}

func (f *frameSink) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wrote++
	if f.torn {
		f.late++
	}
	return len(b), nil
}

// tearDown marks the point the session's Cleanup would run.
func (f *frameSink) tearDown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torn = true
}

func (f *frameSink) counts() (wrote, late int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wrote, f.late
}

// plainModel is a model with nothing in it but a view to render.
type plainModel struct{}

func (plainModel) Init() tea.Cmd { return nil }

// Update returns the model itself, the way app.OS does, so the programStart
// wrapper drops away after the first message.
func (plainModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return plainModel{}, nil }

func (plainModel) View() tea.View { return tea.NewView("teardown test") }

// TestWebTeardownRunsAfterTheProgramHasStopped holds the web server's teardown
// to its one rule: Cleanup runs after the program has written its last frame.
//
// The test builds the program the way createTUIOSProgram does and then starts
// Run the way sip does, on a goroutine of its own and only after the factory
// has returned. That order is what lets a Wait started in the factory read the
// program's finished channel before Run writes it, so this test is also where
// the race detector sees that bug if the wait for running is ever dropped.
func TestWebTeardownRunsAfterTheProgramHasStopped(t *testing.T) {
	// A pipe stands in for the browser's keyboard: it blocks until the test
	// closes it, so the input reader behaves like a real one.
	input, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })

	sink := &frameSink{}
	running := make(chan struct{})
	program := tea.NewProgram(
		&programStart{Model: plainModel{}, start: sync.OnceFunc(func() { close(running) })},
		tea.WithInput(input),
		tea.WithOutput(sink),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
		tea.WithColorProfile(colorprofile.Ascii),
		tea.WithWindowSize(80, 24),
		tea.WithoutSignalHandler(),
	)

	torn := make(chan struct{})
	go func() {
		cleanupAfterProgram(program, running, sink.tearDown)
		close(torn)
	}()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if _, err := program.Run(); err != nil {
			t.Errorf("the program stopped with an error: %v", err)
		}
	}()

	<-running
	program.Quit()
	<-stopped
	<-torn

	wrote, late := sink.counts()
	if wrote == 0 {
		t.Fatal("the program wrote no frames, so this test proves nothing about when Cleanup runs")
	}
	if late != 0 {
		t.Fatalf("the program wrote %d times after Cleanup ran; Cleanup has to wait for the last frame", late)
	}
}
