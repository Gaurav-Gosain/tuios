package tuie2e

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPprofServedByLongRunningCommands starts each command that keeps running
// and served nothing for --pprof, once with --pprof on a bare :PORT and once
// without it. With the flag, the heap profile answers on 127.0.0.1:PORT and
// the process holds that port on IPv4 loopback only: an address with no host
// must not listen on every interface. Without the flag, once the command is
// running and again a second later, it listens on what it listened on with
// the flag, less the profile port, so no profile listener opens unasked.
//
// Negative controls: see NEGATIVE_CONTROLS.md.
func TestPprofServedByLongRunningCommands(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads the command's listeners from /proc")
	}

	// launched is a started command: its pid, and the ports of its own that
	// change from one run to the next (pair's link port).
	type launched struct {
		pid int
		own []int
	}

	// piped starts tuios with args, stdin an open pipe held until cleanup,
	// and returns the pid, the stdin pipe, the stdout lines and whether the
	// process has ended.
	piped := func(t *testing.T, base string, args ...string) (int, *os.File, <-chan string, <-chan struct{}) {
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
			sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
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
		return cmd.Process.Pid, stdin, lines, done
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

	// framed starts tuios with args on a fake terminal and waits for the
	// first painted frame, which frame matches.
	framed := func(t *testing.T, base string, cols, rows int, frame *regexp.Regexp, args ...string) launched {
		t.Helper()
		opts := fakeTermOpts{bg: "rgb:0000/0000/0000", fg: "rgb:ffff/ffff/ffff"}
		ft := startFakeTerm(t, base, cols, rows, opts, args...)
		if _, ok := ft.waitFor(frame, bootTimeout); !ok {
			t.Fatalf("tuios %s painted no frame; output:\n%q", strings.Join(args, " "), tailString(ft.output(), 600))
		}
		return launched{pid: ft.cmd.Process.Pid}
	}

	// daemonRun starts a daemon under a fresh isolation root.
	daemonRun := func(t *testing.T) string {
		t.Helper()
		base := t.TempDir()
		startDetached(t, base, "pprof")
		return base
	}

	cases := []struct {
		name string
		// setup prepares what both runs share and returns the start recipe,
		// which takes the extra flags and returns once the command is ready.
		setup func(t *testing.T) func(t *testing.T, extra ...string) launched
	}{
		{"mcp", func(t *testing.T) func(*testing.T, ...string) launched {
			base := daemonRun(t)
			return func(t *testing.T, extra ...string) launched {
				pid, stdin, lines, _ := piped(t, base, append([]string{"mcp"}, extra...)...)
				req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n"
				if _, err := io.WriteString(stdin, req); err != nil {
					t.Fatalf("write initialize: %v", err)
				}
				if line := firstLine(t, lines, "tuios mcp"); !strings.Contains(line, `"id":1`) {
					t.Fatalf("tuios mcp answered initialize with %q", line)
				}
				return launched{pid: pid}
			}
		}},
		{"stdio-proxy", func(t *testing.T) func(*testing.T, ...string) launched {
			base := daemonRun(t)
			return func(t *testing.T, extra ...string) launched {
				pid, _, _, done := piped(t, base, append([]string{"stdio-proxy"}, extra...)...)
				// It dials the daemon socket only for a stream opened on its
				// input, so it holds no daemon socket before input: ready is
				// two seconds after start with the process still running.
				time.Sleep(2 * time.Second)
				select {
				case <-done:
					t.Fatal("tuios stdio-proxy ended within two seconds of its start")
				default:
				}
				return launched{pid: pid}
			}
		}},
		{"pair", func(t *testing.T) func(*testing.T, ...string) launched {
			base, hostPub, _ := pairSetup(t)
			ak := filepath.Join(base, "authorized_keys")
			return func(t *testing.T, extra ...string) launched {
				run := startPair(t, base, nil, pairArgs(ak, hostPub, extra...)...)
				addr := run.phone(t).addr
				_, portText, _ := strings.Cut(addr, ":")
				port, err := strconv.Atoi(portText)
				if err != nil {
					t.Fatalf("tuios pair names %q as its address", addr)
				}
				return launched{pid: run.cmd.Process.Pid, own: []int{port}}
			}
		}},
		{"subscribe", func(t *testing.T) func(*testing.T, ...string) launched {
			base := daemonRun(t)
			return func(t *testing.T, extra ...string) launched {
				pid, _, lines, _ := piped(t, base, append([]string{"subscribe"}, extra...)...)
				if line := firstLine(t, lines, "tuios subscribe"); !strings.Contains(line, "subscribed") {
					t.Fatalf("tuios subscribe printed %q first, not its ack", line)
				}
				return launched{pid: pid}
			}
		}},
		{"agent-proto", func(t *testing.T) func(*testing.T, ...string) launched {
			base := t.TempDir()
			fake := buildFakeACP(t)
			return func(t *testing.T, extra ...string) launched {
				args := append([]string{"agent-proto", "--protocol", "acp"}, extra...)
				pid, _, lines, _ := piped(t, base, append(args, "--", fake)...)
				firstLine(t, lines, "tuios agent-proto")
				return launched{pid: pid}
			}
		}},
		{"tape play", func(t *testing.T) func(*testing.T, ...string) launched {
			base := t.TempDir()
			tapeFile := filepath.Join(t.TempDir(), "hold.tape")
			if err := os.WriteFile(tapeFile, []byte("Sleep 10m\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T, extra ...string) launched {
				// The fake terminal does not report synchronized output, so the
				// frame is the alternate screen entered, cleared and drawn on.
				frame := regexp.MustCompile(`\x1b\[\?1049h[\s\S]*\x1b\[2J[^\x1b]*\x1b\[[0-9;]*m[^\x1b]+`)
				return framed(t, base, 120, 36, frame, append([]string{"tape", "play", tapeFile}, extra...)...)
			}
		}},
		{"mermaid --watch", func(t *testing.T) func(*testing.T, ...string) launched {
			base := t.TempDir()
			src := filepath.Join(t.TempDir(), "flow.mmd")
			if err := os.WriteFile(src, []byte("flowchart LR\n  A --> B\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return func(t *testing.T, extra ...string) launched {
				return framed(t, base, 140, 40, regexp.MustCompile(regexp.QuoteMeta(mermaidFrameEnd)), append([]string{"mermaid", src, "--watch", "--split", "--theme", "flexoki"}, extra...)...)
			}
		}},
	}

	// loopbackRows reports, among the LISTEN rows whose socket inode the pid
	// holds, whether /proc/PID/net/tcp has one on 127.0.0.1:port and whether
	// /proc/PID/net/tcp6 has one on port.
	loopbackRows := func(t *testing.T, pid, port int) (v4Loopback, v6 bool) {
		t.Helper()
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			t.Fatalf("read %s: %v", fdDir, err)
		}
		inodes := map[string]bool{}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if inode, ok := strings.CutPrefix(link, "socket:["); ok {
				inodes[strings.TrimSuffix(inode, "]")] = true
			}
		}
		hexPort := fmt.Sprintf("%04X", port)
		for _, table := range []string{"tcp", "tcp6"} {
			path := fmt.Sprintf("/proc/%d/net/%s", pid, table)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			for _, row := range strings.Split(string(data), "\n")[1:] {
				fields := strings.Fields(row)
				if len(fields) < 10 || fields[3] != "0A" || !inodes[fields[9]] {
					continue
				}
				host, p, _ := strings.Cut(fields[1], ":")
				switch {
				case table == "tcp" && host == "0100007F" && p == hexPort:
					v4Loopback = true
				case table == "tcp6" && p == hexPort:
					v6 = true
				}
			}
		}
		return v4Loopback, v6
	}

	heap := func(addr string) (int, int, error) {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + addr + "/debug/pprof/heap")
		if err != nil {
			return 0, 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		return resp.StatusCode, len(body), err
	}

	without := func(ports []int, drop ...int) []int {
		return slices.DeleteFunc(slices.Clone(ports), func(p int) bool { return slices.Contains(drop, p) })
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := tc.setup(t)

			pprofPort := freePort(t)
			addr := fmt.Sprintf("127.0.0.1:%d", pprofPort)
			with := start(t, "--pprof", fmt.Sprintf(":%d", pprofPort))
			deadline := time.Now().Add(10 * time.Second)
			for {
				status, size, err := heap(addr)
				if err == nil && status == http.StatusOK && size > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Errorf("ASSERTION: %s --pprof :%d never served the heap profile on %s: status %d, %d bytes, %v",
						tc.name, pprofPort, addr, status, size, err)
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			v4, v6 := loopbackRows(t, with.pid, pprofPort)
			if !v4 {
				t.Errorf("ASSERTION: %s --pprof :%d holds no listener on 127.0.0.1:%d", tc.name, pprofPort, pprofPort)
			}
			if v6 {
				t.Errorf("ASSERTION: %s --pprof :%d listens on port %d over IPv6, not on loopback only", tc.name, pprofPort, pprofPort)
			}
			withPorts := tcpListenPorts(t, with.pid)
			t.Logf("%s with --pprof listens on %v", tc.name, withPorts)

			plain := start(t)
			want := append(without(withPorts, append(with.own, pprofPort)...), plain.own...)
			slices.Sort(want)
			for i := range 2 {
				if i > 0 {
					time.Sleep(time.Second)
				}
				ports := tcpListenPorts(t, plain.pid)
				t.Logf("%s without --pprof listens on %v", tc.name, ports)
				if !slices.Equal(ports, want) {
					t.Fatalf("ASSERTION: %s without --pprof listens on %v, want %v", tc.name, ports, want)
				}
			}
		})
	}
}
