package localclient

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/procsetup"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// termioRaw is the output of a terminal in raw mode, as a pty gives one.
var termioRaw = termio.Output{Raw: true}

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated.
func TestMain(m *testing.M) { os.Exit(testutil.RunIsolated(m)) }

// frames is a terminal output that says when a write arrives.
type frames struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	wrote chan struct{}
}

func (f *frames) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.buf.Write(p)
	f.mu.Unlock()
	select {
	case f.wrote <- struct{}{}:
	default:
	}
	return len(p), nil
}

// waitFor waits until the output holds want.
func (f *frames) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		f.mu.Lock()
		got := bytes.Contains(f.buf.Bytes(), []byte(want))
		f.mu.Unlock()
		if got {
			return
		}
		select {
		case <-f.wrote:
		case <-deadline:
			t.Fatalf("the client wrote no %q in 10s", want)
		}
	}
}

// TestClientRunsOnAPipe runs the client the binary runs on a terminal that is
// not a file: keys from a pipe, frames to a buffer, sizes from a channel. It
// draws, takes the new size, and quits.
func TestClientRunsOnAPipe(t *testing.T) {
	procsetup.Setup(procsetup.Options{})
	t.Cleanup(func() { app.SetInputHandler(nil) })

	userConfig, settings := LoadConfig(func(string) string { return "" }, config.Overrides{NoAnimations: true})
	in, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() })
	out := &frames{wrote: make(chan struct{}, 1)}
	resize := make(chan Size)
	term := Pipe{
		In: in, Out: out,
		Env:   []string{"TERM=xterm-256color"},
		Width: 80, Height: 24,
		Resize:  resize,
		Profile: colorprofile.ANSI256,
		Output:  termioRaw,
	}
	c := New(term, app.OSOptions{
		Client:          app.ClientLocal,
		KeybindRegistry: config.NewKeybindRegistry(userConfig),
		UserConfig:      userConfig,
		Overrides:       config.Overrides{NoAnimations: true},
		Settings:        settings,
		Width:           80,
		Height:          24,
	}, nil)

	type result struct {
		model any
		err   error
	}
	ran := make(chan result, 1)
	go func() {
		m, err := c.Run()
		ran <- result{m, err}
	}()

	// The first frame, in the synchronized update the writer puts round it.
	out.waitFor(t, "\x1b[?2026h")

	// Sent twice: the client reads the second only after the event loop has
	// taken the first, so the quit below lands after the new size.
	for range 2 {
		select {
		case resize <- Size{Width: 100, Height: 30}:
		case <-time.After(10 * time.Second):
			t.Fatal("the client did not read the new size in 10s")
		}
	}
	c.Program.Quit()

	var r result
	select {
	case r = <-ran:
	case <-time.After(10 * time.Second):
		c.Program.Kill()
		t.Fatal("the client did not quit in 10s")
	}
	c.Close()
	if r.err != nil {
		t.Fatalf("run: %v", r.err)
	}
	final, ok := r.model.(*app.OS)
	if !ok {
		t.Fatalf("the program ended on %T, want *app.OS", r.model)
	}
	c.Finish(final)
	if final.Width != 100 || final.Height != 30 {
		t.Errorf("the client ended at %dx%d, want the 100x30 the terminal sent", final.Width, final.Height)
	}
}
