//go:build unix

package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// These arm the handler for real and signal the test process itself. That is
// only safe while the handler is registered: a SIGTERM or SIGINT arriving
// after signal.Stop takes its default action and ends the test binary, which
// is exactly the failure the cleanup test exists to catch.

// floodModel redraws a screenful of changing text as fast as it is asked to,
// so the renderer always has a frame to write.
type floodModel struct{ n int }

type floodTick struct{}

func (m floodModel) Init() tea.Cmd { return tick() }

func (m floodModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case floodTick:
		m.n++
		return m, tick()
	case tea.QuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m floodModel) View() tea.View {
	line := strings.Repeat(string(rune('a'+m.n%26)), 200)
	return tea.NewView(strings.Repeat(line+"\n", 100))
}

func tick() tea.Cmd {
	return tea.Tick(time.Millisecond, func(time.Time) tea.Msg { return floodTick{} })
}

// TestSignalQuitEndsAWedgedProgram is the case the handler exists for: a real
// program whose output is a pipe nobody reads. Once the pipe fills, the frame
// write blocks and the event loop never reads the quit, so Run does not
// return; SIGTERM must still end the process, with SIGTERM's exit code, once
// the grace elapses.
func TestSignalQuitEndsAWedgedProgram(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	p := tea.NewProgram(floodModel{},
		tea.WithInput(nil),
		tea.WithOutput(w),
		tea.WithWindowSize(200, 100),
		tea.WithoutSignalHandler(),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
	)
	exited := make(chan int, 1)
	finish := armSignalQuitWith(p, 200*time.Millisecond, func(code int) { exited <- code })

	returned := make(chan struct{})
	go func() {
		_, _ = p.Run()
		close(returned)
	}()

	// Nothing reads r, so the program fills the pipe and wedges on its write.
	// Give it time to get there.
	time.Sleep(500 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case code := <-exited:
		if want := 128 + int(syscall.SIGTERM); code != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	case <-returned:
		t.Fatal("the program returned on its own; it was never wedged, so this proves nothing")
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not end a wedged program")
	}

	// The exit was a stand-in, so the program is still stuck. Unwedge it by
	// draining the pipe, and let it finish before the next test.
	go func() {
		buf := make([]byte, 64<<10)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()
	p.Kill()
	<-returned
	finish()
	_ = w.Close()
	_ = r.Close()
}

// TestSignalQuitStaysArmedThroughCleanup pins that a signal after Program.Run
// has returned, while the caller is still cleaning up, is handled by the
// policy rather than taking its default action. A first Ctrl+C there waits for
// the cleanup; a second exits through the policy's exit, which restores the
// terminal, instead of killing the process with nothing restored.
func TestSignalQuitStaysArmedThroughCleanup(t *testing.T) {
	p := tea.NewProgram(floodModel{}, tea.WithInput(nil), tea.WithoutRenderer())
	exited := make(chan int, 1)
	finish := armSignalQuitWith(p, time.Hour, func(code int) { exited <- code })
	defer finish()

	// Program.Run has returned and the cleanup is running: nothing reads the
	// message channel any more.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		t.Fatalf("exited with %d on the first signal; it should wait for the cleanup", code)
	case <-time.After(100 * time.Millisecond):
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-exited:
		if want := 128 + int(syscall.SIGINT); code != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second signal during cleanup was not handled")
	}
}

// TestSignalQuitFinishReturnsWhenCleanupEnds pins that finish retires the
// handler without a signal, and that calling it twice is harmless: each entry
// point calls it once, but a deferred call beside it must not deadlock.
func TestSignalQuitFinishReturnsWhenCleanupEnds(t *testing.T) {
	p := tea.NewProgram(floodModel{}, tea.WithInput(nil), tea.WithoutRenderer())
	finish := armSignalQuitWith(p, time.Hour, func(code int) {
		t.Errorf("exited with %d without a signal", code)
	})
	done := make(chan struct{})
	go func() {
		finish()
		finish()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("finish did not return")
	}
}
