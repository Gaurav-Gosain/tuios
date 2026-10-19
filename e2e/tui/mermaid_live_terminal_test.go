package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
)

// ttyState is the pty's line discipline as stty -g writes it, read from the
// slave side.
func ttyState(t *testing.T, p xpty.Pty) string {
	t.Helper()
	name := p.Name()
	if up, ok := p.(*xpty.UnixPty); ok {
		name = up.SlaveName()
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("open the pty slave: %v", err)
	}
	defer func() { _ = f.Close() }()
	cmd := exec.Command("stty", "-g")
	cmd.Stdin = f
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stty: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

var (
	reMermaidFrame = regexp.MustCompile(`\x1b_Ga=T`)
	// The end of a frame: the cursor restored and synchronized output ended.
	mermaidFrameEnd = "\x1b8\x1b[?2026l"
)

// TestMermaidLiveGivesTheTerminalBack runs tuios mermaid --demo on a
// terminal with kitty graphics and ends it each way it can end. Each time the
// line discipline must be what it was before the program started, and after
// the last frame the program must delete its picture, show the cursor and
// leave the alternate screen. A terminal left in raw mode prints each later
// line one step to the right of the one before.
//
// The stalled case stops reading before it sends SIGTERM, so a frame is stuck
// in a write: termios must come back while the write still blocks, and the
// reset must not land inside that frame.
func TestMermaidLiveGivesTheTerminalBack(t *testing.T) {
	cases := []struct {
		name string
		// tags builds tuios from source with these build tags for the case.
		tags  string
		stall bool
		end   func(t *testing.T, ft *fakeTerm)
		code  int
	}{
		{name: "q", end: func(t *testing.T, ft *fakeTerm) { _, _ = ft.pty.Write([]byte("q")) }},
		{name: "ctrl+c", end: func(t *testing.T, ft *fakeTerm) { _, _ = ft.pty.Write([]byte{3}) }},
		{name: "SIGTERM", end: func(t *testing.T, ft *fakeTerm) { _ = ft.cmd.Process.Signal(syscall.SIGTERM) }},
		{name: "SIGHUP", end: func(t *testing.T, ft *fakeTerm) { _ = ft.cmd.Process.Signal(syscall.SIGHUP) }},
		{name: "panic", tags: "mermaidpanic", end: func(*testing.T, *fakeTerm) {}, code: 2},
		{name: "SIGTERM while stalled", stall: true, end: func(t *testing.T, ft *fakeTerm) { _ = ft.cmd.Process.Signal(syscall.SIGTERM) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			var before string
			opts := fakeTermOpts{bg: "rgb:0000/0000/0000", fg: "rgb:ffff/ffff/ffff", kitty: true,
				onPty: func(p xpty.Pty) { before = ttyState(t, p) }}
			if tc.tags != "" {
				opts.bin = buildTuiosWithTags(t, tc.tags)
			}
			ft := startFakeTerm(t, base, 120, 36, opts, "mermaid", "--demo", "--loop", "--theme", "flexoki")
			if _, ok := ft.waitFor(reMermaidFrame, 20*time.Second); !ok {
				t.Fatalf("no kitty frame; output:\n%q", tailString(ft.output(), 400))
			}
			if tc.stall {
				ft.hold.Store(true)
				time.Sleep(600 * time.Millisecond)
			}
			tc.end(t, ft)
			if tc.stall {
				time.Sleep(1500 * time.Millisecond)
				if got := ttyState(t, ft.pty); got != before {
					t.Errorf("while the terminal is stalled, termios is %s, want %s", got, before)
				}
				ft.hold.Store(false)
			}
			code, ok := ft.exitCode(15 * time.Second)
			if !ok {
				t.Fatal("tuios mermaid did not exit")
			}
			if code != tc.code {
				t.Errorf("exit code %d, want %d", code, tc.code)
			}
			if got := ttyState(t, ft.pty); got != before {
				t.Errorf("termios after exit is %s, want %s as before", got, before)
			}
			out := ft.output()
			after := out
			if i := strings.LastIndex(out, mermaidFrameEnd); i >= 0 {
				after = out[i:]
			}
			for _, want := range []struct{ seq, what string }{
				{"\x1b_Ga=d,d=I", "delete the picture"},
				{"\x1b[?25h", "show the cursor"},
				{"\x1b[?1049l", "leave the alternate screen"},
			} {
				if !strings.Contains(after, want.seq) {
					t.Errorf("after the last frame it does not %s; tail:\n%q", want.what, tailString(after, 300))
				}
			}
		})
	}
}

// buildTuiosWithTags builds tuios from this checkout with the given build
// tags. The panic case needs it: only a mermaidpanic build panics in the frame
// encoder, and a release build has no hook to make it. It ignores
// TUIOS_E2E_BIN, so a negative control of that case edits the source.
func buildTuiosWithTags(t *testing.T, tags string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tuios-"+tags)
	build := exec.Command("go", "build", "-tags", tags, "-o", bin, "./cmd/tuios")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tuios with -tags %s: %v\n%s", tags, err, out)
	}
	return bin
}

// TestMermaidLiveWritesNoTerminalControls watches a diagram whose labels,
// source lines and parse error hold terminal controls, with the source pane
// shown, in a kitty terminal and a text one. "#27;" in a label decodes to
// ESC, and the file also holds raw ESC bytes, so a view that wrote either
// as it is would set the window title and write the clipboard. The text of
// each must still be on screen, with U+FFFD in place of the control.
func TestMermaidLiveWritesNoTerminalControls(t *testing.T) {
	src := "flowchart LR\n  A[\"#27;]2;pwned#7;\"] --> B\n  %% \x1b]52;c;aGk=\x07 clip\n  C[\"\x1b]2;title\x07\"] --> D\n"
	broken := "flowchart LR\n  A[\x1b]2;broken\x07 -->\n"
	for _, tc := range []struct {
		name  string
		kitty bool
		src   string
	}{
		{"kitty", true, src},
		{"text", false, src},
		{"parse error", false, broken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			opts := fakeTermOpts{bg: "rgb:0000/0000/0000", fg: "rgb:ffff/ffff/ffff", kitty: tc.kitty}
			path := filepath.Join(t.TempDir(), "hostile.mmd")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			ft := startFakeTerm(t, base, 140, 40, opts, "mermaid", path, "--watch", "--split", "--theme", "flexoki")
			// The source pane colours the source, so a match is on a word of it.
			shown := regexp.MustCompile(`aGk=|broken`)
			if _, ok := ft.waitFor(shown, 20*time.Second); !ok {
				t.Fatalf("the source pane never showed; output:\n%q", tailString(ft.output(), 600))
			}
			time.Sleep(300 * time.Millisecond)
			_, _ = ft.pty.Write([]byte("q"))
			if _, ok := ft.exitCode(15 * time.Second); !ok {
				t.Fatal("tuios mermaid did not exit")
			}
			out := ft.output()
			for _, bad := range []string{"\x1b]2;pwned", "\x1b]2;title", "\x1b]2;broken", "\x1b]52;", "\x07"} {
				if strings.Contains(out, bad) {
					t.Errorf("the view wrote %q to the terminal", bad)
				}
			}
			if !strings.Contains(out, "\uFFFD") {
				t.Errorf("no U+FFFD where the control was; output:\n%q", tailString(out, 600))
			}
		})
	}
}
