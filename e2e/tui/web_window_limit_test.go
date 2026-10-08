package tuie2e

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// tuios-web passes sip a window limit: 1200x500 and 250000 cells. Each cell
// costs tuios-web about 1.3 KB, so one resize message with no limit can ask for
// gigabytes. sip clamps a resize past the limit, and the session goes on at the
// clamped size.

var (
	webBinOnce sync.Once
	webBinPath string
	webBinErr  error
)

// tuiosWebBin is the tuios-web binary under test: TUIOS_E2E_WEB_BIN, or one
// built from this checkout once per run, next to the short runtime roots that
// runE2E removes at the end.
func tuiosWebBin(t *testing.T) string {
	t.Helper()
	webBinOnce.Do(func() {
		if bin := os.Getenv("TUIOS_E2E_WEB_BIN"); bin != "" {
			webBinPath, webBinErr = filepath.Abs(bin)
			return
		}
		dir := filepath.Join(shortRuntimeRoot, "bin")
		if webBinErr = os.MkdirAll(dir, 0o700); webBinErr != nil {
			return
		}
		webBinPath = filepath.Join(dir, "tuios-web")
		build := exec.Command("go", "build", "-o", webBinPath, "./cmd/tuios-web")
		build.Dir = "../.."
		if out, err := build.CombinedOutput(); err != nil {
			webBinErr = fmt.Errorf("build tuios-web: %v\n%s", err, out)
		}
	})
	if webBinErr != nil {
		t.Fatal(webBinErr)
	}
	return webBinPath
}

// startTuiosWeb runs tuios-web on a loopback port with the isolation root's
// XDG directories, and stops it by its process group when the test ends.
func startTuiosWeb(t *testing.T, base string, args ...string) (addr string, pid int) {
	t.Helper()
	bin := tuiosWebBin(t)
	port := freePort(t)
	addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	cmd := exec.Command(bin, append([]string{"--host", "127.0.0.1", "--port", strconv.Itoa(port)}, args...)...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "ENV=", "PS1=$ ")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	logPath := filepath.Join(t.TempDir(), "tuios-web.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create the tuios-web log: %v", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start tuios-web: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		_ = logFile.Close()
		if t.Failed() {
			if data, err := os.ReadFile(logPath); err == nil {
				t.Logf("tuios-web log:\n%s", tailString(string(data), 4000))
			}
		}
	})
	deadline := time.Now().Add(bootTimeout)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = c.Close()
			return addr, cmd.Process.Pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("tuios-web never listened on %s: %v", addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// webClient is the smallest WebSocket client sip's protocol needs: binary
// messages whose first byte is the message type. It keeps every output byte.
type webClient struct {
	conn net.Conn
	br   *bufio.Reader

	mu  sync.Mutex
	out []byte
	err error
}

func dialWeb(t *testing.T, addr string) *webClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", addr, base64.StdEncoding.EncodeToString(nonce))
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read the WebSocket handshake: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("WebSocket handshake answered %s", resp.Status)
	}
	c := &webClient{conn: conn, br: br}
	go c.readLoop()
	return c
}

// send writes one masked binary frame, as a client must.
func (c *webClient) send(t *testing.T, kind byte, payload string) {
	t.Helper()
	data := append([]byte{kind}, payload...)
	frame := []byte{0x82}
	switch n := len(data); {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n <= 0xffff:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	frame = append(frame, mask...)
	for i, b := range data {
		frame = append(frame, b^mask[i%4])
	}
	if _, err := c.conn.Write(frame); err != nil {
		t.Fatalf("send a %q message: %v", kind, err)
	}
}

func (c *webClient) readLoop() {
	var msg []byte
	for {
		var head [2]byte
		if _, err := io.ReadFull(c.br, head[:]); err != nil {
			c.fail(err)
			return
		}
		n := uint64(head[1] & 0x7f)
		switch n {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				c.fail(err)
				return
			}
			n = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				c.fail(err)
				return
			}
			n = binary.BigEndian.Uint64(ext[:])
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			c.fail(err)
			return
		}
		if op := head[0] & 0x0f; op == 0x8 {
			c.fail(errors.New("the server closed the connection"))
			return
		} else if op >= 0x8 {
			continue
		}
		msg = append(msg, payload...)
		if head[0]&0x80 == 0 {
			continue
		}
		if len(msg) > 0 && msg[0] == '1' {
			c.mu.Lock()
			c.out = append(c.out, msg[1:]...)
			c.mu.Unlock()
		}
		msg = nil
	}
}

func (c *webClient) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

// paneSizeRe reads the size the shell in the pane reports, tagged with the
// number of the question so an older answer cannot match.
var paneSizeRe = regexp.MustCompile(`SZ(\d+)_(\d+)x(\d+)`)

// paneSize asks the shell for its window size and waits for the answer. The
// tag is computed by the shell, so the echo of the typed command cannot match.
func (c *webClient) paneSize(t *testing.T, tag int, timeout time.Duration) (cols, rows int) {
	t.Helper()
	cols, rows, err := c.tryPaneSize(t, tag, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return cols, rows
}

// tryPaneSize is paneSize that reports a missing answer instead of failing.
func (c *webClient) tryPaneSize(t *testing.T, tag int, timeout time.Duration) (cols, rows int, err error) {
	t.Helper()
	c.send(t, '0', fmt.Sprintf("echo SZ$((%d+0))_$(tput cols)x$(tput lines)\r", tag))
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		out, err := string(c.out), c.err
		c.mu.Unlock()
		for _, m := range paneSizeRe.FindAllStringSubmatch(out, -1) {
			if m[1] == strconv.Itoa(tag) {
				cols, _ = strconv.Atoi(m[2])
				rows, _ = strconv.Atoi(m[3])
				return cols, rows, nil
			}
		}
		if err != nil {
			return 0, 0, fmt.Errorf("the connection ended before answer %d: %v", tag, err)
		}
		if time.Now().After(deadline) {
			return 0, 0, fmt.Errorf("the shell never answered size question %d in %s; last output %q",
				tag, timeout, tailString(out, 300))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// rssMB is the resident set of a process in MB, from /proc.
func rssMB(t *testing.T, pid int) int {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatalf("read the status of %d: %v", pid, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(rest), " kB"))
			return kb / 1024
		}
	}
	t.Fatalf("no VmRSS for %d", pid)
	return 0
}

// TestWebClampsAWindowPastTheLimit sends tuios-web a resize past its window
// limit after one inside it.
//
// The resize inside the limit has to reach the pane, which is the positive
// half: without it, a size that never changes would pass for the wrong reason.
// The resize to 1500x900 has to reach the pane clamped to 1200 columns and
// 208 rows (250000 cells), keep the session answering, and keep the memory
// near the cost of that many cells. See NEGATIVE_CONTROLS.md for the run
// without the limit.
func TestWebClampsAWindowPastTheLimit(t *testing.T) {
	const maxCols, maxRows = 1200, 250_000 / 1200
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nstart_in_terminal_mode = true\n")
	addr, pid := startTuiosWeb(t, base, "--ephemeral")
	client := dialWeb(t, addr)
	client.send(t, '2', `{"cols":120,"rows":40}`)

	// The first answer also says the shell is up.
	cols, rows := client.paneSize(t, 1, shellTimeout)
	t.Logf("at 120x40 the pane is %dx%d", cols, rows)

	client.send(t, '2', `{"cols":300,"rows":60}`)
	deadline := time.Now().Add(uiTimeout)
	for tag := 2; ; tag++ {
		cols, rows = client.paneSize(t, tag, uiTimeout)
		if cols > 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a resize to 300x60 left the pane at %dx%d", cols, rows)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("at 300x60 the pane is %dx%d", cols, rows)
	before := 0
	if runtime.GOOS == "linux" {
		before = rssMB(t, pid)
	}

	client.send(t, '2', `{"cols":1500,"rows":900}`)
	// The pane is smaller than the window by what the border and the chrome
	// take, measured at 300x60.
	chromeCols, chromeRows := 300-cols, 60-rows
	wantCols, wantRows := maxCols-chromeCols, maxRows-chromeRows
	var gotCols, gotRows int
	deadline = time.Now().Add(uiTimeout)
	for tag := 100; ; tag++ {
		var err error
		gotCols, gotRows, err = client.tryPaneSize(t, tag, uiTimeout)
		if err != nil {
			t.Fatalf("after a resize to 1500x900: %v", err)
		}
		if gotCols > cols {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a resize to 1500x900 left the pane at %dx%d, want it clamped near %dx%d",
				gotCols, gotRows, maxCols, maxRows)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("at 1500x900 the pane is %dx%d", gotCols, gotRows)
	if gotCols != wantCols || gotRows != wantRows {
		t.Errorf("a resize to 1500x900 gave a %dx%d pane, want %dx%d (the %dx%d limit less the chrome)",
			gotCols, gotRows, wantCols, wantRows, maxCols, maxRows)
	}
	if runtime.GOOS == "linux" {
		after := rssMB(t, pid)
		t.Logf("tuios-web resident set: %d MB before the oversized resize, %d MB after", before, after)
		// 250000 cells at about 1.3 KB is about 325 MB. 1500x900 with no
		// limit is about 1.7 GB.
		if after-before > 600 {
			t.Errorf("a resize past the limit grew tuios-web from %d MB to %d MB", before, after)
		}
	}
}
