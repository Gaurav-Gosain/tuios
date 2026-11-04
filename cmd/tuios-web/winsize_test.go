package main

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/sip"

	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// sizedSession is a browser connection whose size the test sets the way
// sip's resize handler does: the size goes on the session first, and the
// message for it after.
type sizedSession struct {
	mu   sync.Mutex
	pty  sip.Pty
	keys io.Reader
}

func (s *sizedSession) Pty() sip.Pty {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pty
}

func (s *sizedSession) resize(cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pty.Width, s.pty.Height = cols, rows
}

func (s *sizedSession) Context() context.Context             { return context.Background() }
func (s *sizedSession) Read(p []byte) (int, error)           { return s.keys.Read(p) }
func (s *sizedSession) Write(p []byte) (int, error)          { return len(p), nil }
func (s *sizedSession) Fd() uintptr                          { return 0 }
func (s *sizedSession) PtySlave() *os.File                   { return nil }
func (s *sizedSession) WindowChanges() <-chan sip.WindowSize { return nil }

// TestWebSizeIsTheTerminalsAfterAStaleStartSize holds the web program to the
// size the browser's terminal has now, whatever order the size messages
// arrive in (issue 1017).
//
// Bubble Tea sends the start size from a goroutine of its own, and sip sends
// the browser's resize from its read loop. Nothing orders the two, so on a
// slow machine the start size can reach the event loop after the resize.
// This test builds the program the way the server does and sends the two in
// that order on purpose. tuios must end at the resized terminal: a frame laid
// out for the start size wraps in the smaller terminal and leaves old cells
// on screen.
func TestWebSizeIsTheTerminalsAfterAStaleStartSize(t *testing.T) {
	saved := webServerConfig
	t.Cleanup(func() { webServerConfig = saved })
	webServerConfig.ephemeral = true
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home+"/config")
	t.Setenv("XDG_STATE_HOME", home+"/state")
	t.Setenv("XDG_DATA_HOME", home+"/data")

	keys, keyWriter := io.Pipe()
	t.Cleanup(func() { _ = keyWriter.Close() })
	sess := &sizedSession{pty: sip.Pty{Width: 48, Height: 46}, keys: keys}
	program := createTUIOSProgram(sess)

	type result struct {
		model tea.Model
		err   error
	}
	done := make(chan result, 1)
	go func() {
		m, err := program.Run()
		done <- result{m, err}
	}()

	// The key bar takes the bottom rows: sip stores the size and sends it.
	sess.resize(47, 41)
	program.Send(tea.WindowSizeMsg{Width: 47, Height: 41})
	// The start size, sent by the goroutine that lost the race, lands last.
	program.Send(tea.WindowSizeMsg{Width: 48, Height: 46})
	program.Quit()

	var r result
	select {
	case r = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the program did not stop")
	}
	if r.err != nil {
		t.Fatalf("the program stopped with an error: %v", r.err)
	}
	model := r.model
	if start, ok := model.(*programStart); ok {
		model = start.Model
	}
	tuios, ok := model.(*app.OS)
	if !ok {
		t.Fatalf("the program ended with a %T, not an *app.OS", r.model)
	}
	if tuios.Width != 47 || tuios.Height != 41 {
		t.Fatalf("tuios was left at %dx%d, but the browser's terminal is 47x41", tuios.Width, tuios.Height)
	}
}
