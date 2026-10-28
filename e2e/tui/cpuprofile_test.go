package tuie2e

import (
	"bufio"
	"compress/gzip"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCPUProfileWrittenByLongRunningCommands starts each command that keeps
// running with --cpuprofile FILE, waits until it is ready, and ends it the
// clean way it ends for a person: its signal, or the end of its input. Once
// the process has exited, FILE holds a whole gzip stream with a non-empty
// body: a profile that was never started is missing, and one that was not
// stopped before the exit is empty or cut short. For the daemon, the bare
// client, the standalone TUI and agent-proto, a run without the flag, started
// and ended the same way, leaves no new file in the directory it ran in.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "Long-running commands write
// --cpuprofile".
func TestCPUProfileWrittenByLongRunningCommands(t *testing.T) {
	// launch starts tuios with args in workDirIn(base), stdin an open pipe,
	// and returns the process, its stdin, its stdout lines and a channel
	// closed once it has exited.
	launch := func(t *testing.T, base string, args ...string) (*exec.Cmd, *os.File, <-chan string, <-chan struct{}) {
		t.Helper()
		cmd := exec.Command(tuiosBin, args...)
		cmd.Dir = workDirIn(t, base)
		cmd.Env = cliEnv(base)
		in, stdin, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdin = in
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		logf, err := os.Create(filepath.Join(t.TempDir(), "stderr.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = logf
		if err := cmd.Start(); err != nil {
			t.Fatalf("start tuios %s: %v", strings.Join(args, " "), err)
		}
		_ = in.Close()
		lines := make(chan string, 64)
		done := make(chan struct{})
		go func() {
			sc := bufio.NewScanner(stdout)
			for sc.Scan() {
				lines <- sc.Text()
			}
			// A read error ends the lines as EOF does.
			_ = sc.Err()
			close(lines)
			_ = cmd.Wait()
			close(done)
		}()
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			<-done
			_ = stdin.Close()
			_ = logf.Close()
		})
		t.Logf("tuios %s, stderr: %s", strings.Join(args, " "), logf.Name())
		return cmd, stdin, lines, done
	}

	firstLine := func(t *testing.T, lines <-chan string, what string) string {
		t.Helper()
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("%s ended before its first line", what)
			}
			return line
		case <-time.After(bootTimeout):
			t.Fatalf("%s printed no line in %s", what, bootTimeout)
		}
		return ""
	}

	exited := func(t *testing.T, done <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(bootTimeout):
			t.Fatalf("ASSERTION: %s did not exit within %s of its stop", what, bootTimeout)
		}
	}

	signal := func(t *testing.T, p *os.Process, sig os.Signal) {
		t.Helper()
		if err := p.Signal(sig); err != nil {
			t.Fatalf("signal pid %d: %v", p.Pid, err)
		}
	}

	// framedThenSignal starts tuios with args on a fake terminal, waits for
	// the first frame, which frame matches, sends sig and waits for the exit.
	framedThenSignal := func(t *testing.T, base string, cols, rows int, frame *regexp.Regexp, sig os.Signal, args ...string) {
		t.Helper()
		ft := startFakeTerm(t, base, cols, rows, fakeTermOpts{bg: "rgb:0000/0000/0000", fg: "rgb:ffff/ffff/ffff"}, args...)
		if _, ok := ft.waitFor(frame, bootTimeout); !ok {
			t.Fatalf("tuios %s painted no frame; output:\n%q", strings.Join(args, " "), tailString(ft.output(), 600))
		}
		signal(t, ft.cmd.Process, sig)
		exited(t, ft.done, "tuios "+strings.Join(args, " "))
	}

	cases := []struct {
		name string
		// clean also runs the command without the flag and checks its
		// directory.
		clean bool
		// setup prepares what the runs share and returns the run, which
		// starts the command with the extra flags under base, waits until it
		// is ready, stops it and waits for it to exit.
		setup func(t *testing.T) func(t *testing.T, base string, extra ...string)
	}{
		{"daemon", true, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				killDaemon(t, base)
				cmd, _, _, done := launch(t, base, append([]string{"daemon"}, extra...)...)
				deadline := time.Now().Add(bootTimeout)
				for {
					if _, err := tuiosCLI(t, base, "ls"); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("tuios daemon never answered ls in %s", bootTimeout)
					}
					time.Sleep(100 * time.Millisecond)
				}
				signal(t, cmd.Process, syscall.SIGTERM)
				exited(t, done, "tuios daemon")
			}
		}},
		{"ssh --ephemeral", false, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				port := freePort(t)
				args := []string{"ssh", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--no-auth",
					"--key-path", filepath.Join(base, "hostkey"), "--ephemeral"}
				cmd, _, _, done := launch(t, base, append(args, extra...)...)
				addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
				deadline := time.Now().Add(bootTimeout)
				for {
					c, err := net.DialTimeout("tcp", addr, time.Second)
					if err == nil {
						_ = c.Close()
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("tuios ssh never listened on %s", addr)
					}
					time.Sleep(100 * time.Millisecond)
				}
				signal(t, cmd.Process, syscall.SIGTERM)
				exited(t, done, "tuios ssh")
			}
		}},
		{"bare tuios", true, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				term := startIn(t, base, startOpts{daemonDefault: true, args: extra})
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 0 }, bootTimeout); err != nil {
					t.Fatalf("bare tuios painted no first screen: %v", err)
				}
				if err := syscall.Kill(term.Pid(), syscall.SIGTERM); err != nil {
					t.Fatalf("signal the client: %v", err)
				}
				if _, err := term.WaitExit(bootTimeout); err != nil {
					t.Fatalf("ASSERTION: bare tuios did not exit within %s of its stop: %v", bootTimeout, err)
				}
			}
		}},
		{"standalone", true, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				term := startIn(t, base, startOpts{args: extra})
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 0 }, bootTimeout); err != nil {
					t.Fatalf("standalone tuios painted no first screen: %v", err)
				}
				if err := syscall.Kill(term.Pid(), syscall.SIGTERM); err != nil {
					t.Fatalf("signal tuios: %v", err)
				}
				if _, err := term.WaitExit(bootTimeout); err != nil {
					t.Fatalf("ASSERTION: standalone tuios did not exit within %s of its stop: %v", bootTimeout, err)
				}
			}
		}},
		{"pair", false, func(t *testing.T) func(*testing.T, string, ...string) {
			base, hostPub, _ := pairSetup(t)
			ak := filepath.Join(base, "authorized_keys")
			return func(t *testing.T, _ string, extra ...string) {
				run := startPair(t, base, nil, pairArgs(ak, hostPub, extra...)...)
				signal(t, run.cmd.Process, syscall.SIGTERM)
				_, _ = run.wait(t, bootTimeout)
			}
		}},
		{"agent-proto", true, func(t *testing.T) func(*testing.T, string, ...string) {
			fake := buildFakeACP(t)
			return func(t *testing.T, base string, extra ...string) {
				args := append([]string{"agent-proto", "--protocol", "acp"}, extra...)
				_, stdin, lines, done := launch(t, base, append(args, "--", fake)...)
				firstLine(t, lines, "tuios agent-proto")
				_ = stdin.Close()
				exited(t, done, "tuios agent-proto")
			}
		}},
		{"mcp", false, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				startDetached(t, base, "cpu")
				_, stdin, lines, done := launch(t, base, append([]string{"mcp"}, extra...)...)
				req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
				if _, err := io.WriteString(stdin, req); err != nil {
					t.Fatalf("write initialize: %v", err)
				}
				if line := firstLine(t, lines, "tuios mcp"); !strings.Contains(line, `"id":1`) {
					t.Fatalf("tuios mcp answered initialize with %q", line)
				}
				_ = stdin.Close()
				exited(t, done, "tuios mcp")
			}
		}},
		{"stdio-proxy", false, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				_, stdin, lines, done := launch(t, base, append([]string{"stdio-proxy"}, extra...)...)
				if line := firstLine(t, lines, "tuios stdio-proxy"); line != "TUIOS-LINK 1" {
					t.Fatalf("tuios stdio-proxy printed %q first, not its link preamble", line)
				}
				_ = stdin.Close()
				exited(t, done, "tuios stdio-proxy")
			}
		}},
		{"gui-bridge", false, func(*testing.T) func(*testing.T, string, ...string) {
			return func(t *testing.T, base string, extra ...string) {
				b := startBridgeWith(t, base, bridgeOpts{args: append([]string{"--session", "cpu"}, extra...), cols: 80, rows: 24, name: "cpu"})
				_ = b.in.Close()
				b.waitFor(func() bool { return b.closed }, bootTimeout, "tuios gui-bridge stop after its input closed")
			}
		}},
		{"tape play", false, func(t *testing.T) func(*testing.T, string, ...string) {
			tapeFile := filepath.Join(t.TempDir(), "hold.tape")
			if err := os.WriteFile(tapeFile, []byte("Sleep 10m\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			// The fake terminal does not report synchronized output, so the
			// frame is the alternate screen entered, cleared and drawn on.
			frame := regexp.MustCompile(`\x1b\[\?1049h[\s\S]*\x1b\[2J[^\x1b]*\x1b\[[0-9;]*m[^\x1b]+`)
			return func(t *testing.T, base string, extra ...string) {
				framedThenSignal(t, base, 120, 36, frame, syscall.SIGTERM, append([]string{"tape", "play", tapeFile}, extra...)...)
			}
		}},
		{"mermaid --watch", false, func(t *testing.T) func(*testing.T, string, ...string) {
			src := filepath.Join(t.TempDir(), "flow.mmd")
			if err := os.WriteFile(src, []byte("flowchart LR\n  A --> B\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			frame := regexp.MustCompile(regexp.QuoteMeta(mermaidFrameEnd))
			return func(t *testing.T, base string, extra ...string) {
				framedThenSignal(t, base, 140, 40, frame, syscall.SIGINT, append([]string{"mermaid", src, "--watch", "--split", "--theme", "flexoki"}, extra...)...)
			}
		}},
	}

	// files lists every path under dir, relative to it, sorted.
	files := func(t *testing.T, dir string) []string {
		t.Helper()
		var out []string
		err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if rel, _ := filepath.Rel(dir, path); rel != "." {
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("list %s: %v", dir, err)
		}
		slices.Sort(out)
		return out
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := tc.setup(t)

			prof := filepath.Join(t.TempDir(), "cpu.prof")
			// Read once every process the run started has been waited for,
			// gui-bridge's among them, which its fixture waits for in its
			// cleanup.
			t.Cleanup(func() {
				f, err := os.Open(prof)
				if err != nil {
					t.Errorf("ASSERTION: %s --cpuprofile %s wrote no profile: %v", tc.name, prof, err)
					return
				}
				defer func() { _ = f.Close() }()
				zr, err := gzip.NewReader(f)
				if err != nil {
					t.Errorf("ASSERTION: %s --cpuprofile wrote %s, which is not a gzip stream: %v", tc.name, prof, err)
					return
				}
				n, err := io.Copy(io.Discard, zr)
				if err != nil || n == 0 {
					t.Errorf("ASSERTION: %s --cpuprofile: %s reads %d bytes and ends in %v, want a non-empty body and io.EOF",
						tc.name, prof, n, err)
				}
			})
			run(t, t.TempDir(), "--cpuprofile", prof)

			if !tc.clean {
				return
			}
			base := t.TempDir()
			dir := workDirIn(t, base)
			before := files(t, dir)
			run(t, base)
			if after := files(t, dir); !slices.Equal(after, before) {
				t.Errorf("ASSERTION: %s without --cpuprofile left %v in %s, which held %v before it started", tc.name, after, dir, before)
			}
		})
	}
}
