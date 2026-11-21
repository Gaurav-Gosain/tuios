package tuie2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
)

type fakeTerm struct {
	t    *testing.T
	pty  xpty.Pty
	cmd  *exec.Cmd
	mu   sync.Mutex
	out  bytes.Buffer
	scan int
	opts fakeTermOpts
	done chan struct{}
	err  error
	hold atomic.Bool
}

type fakeTermOpts struct {
	fg, bg        string
	sixel         bool
	kitty         bool
	programStatus bool
	silent        bool
	env           []string
	onPty         func(xpty.Pty)
	bin           string
}

var fakeTermQuery = regexp.MustCompile(`\x1b\](10|11|12|4;\d+);\?(?:\x07|\x1b\\)|\x1b\]7501;\?\x1b\\|\x1b\[c|\x1b\[>q|\x1b_Gi=31,[^\x1b]*a=q[^\x1b]*\x1b\\`)

func startFakeTerm(t *testing.T, base string, cols, rows int, opts fakeTermOpts, args ...string) *fakeTerm {
	t.Helper()
	p, err := xpty.NewPty(cols, rows)
	if err != nil {
		t.Fatalf("new pty: %v", err)
	}
	bin := tuiosBin
	if opts.bin != "" {
		bin = opts.bin
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TERM=xterm-256color")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, opts.env...)
	if opts.onPty != nil {
		opts.onPty(p)
	}
	if err := p.Start(cmd); err != nil {
		t.Fatalf("start pane-view: %v", err)
	}
	ft := &fakeTerm{t: t, pty: p, cmd: cmd, opts: opts, done: make(chan struct{})}
	go ft.read()
	go func() {
		ft.err = xpty.WaitProcess(context.Background(), cmd)
		close(ft.done)
	}()
	t.Cleanup(func() {
		select {
		case <-ft.done:
		default:
			_ = cmd.Process.Kill()
			<-ft.done
		}
		_ = p.Close()
	})
	return ft
}

func (ft *fakeTerm) read() {
	buf := make([]byte, 32<<10)
	for {
		for ft.hold.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		n, err := ft.pty.Read(buf)
		if n > 0 {
			ft.mu.Lock()
			ft.out.Write(buf[:n])
			start := max(0, ft.scan-64)
			data := ft.out.Bytes()
			var replies []byte
			for _, loc := range fakeTermQuery.FindAllIndex(data[start:], -1) {
				if start+loc[1] <= ft.scan {
					continue
				}
				replies = append(replies, ft.answer(data[start+loc[0]:start+loc[1]])...)
			}
			ft.scan = len(data)
			ft.mu.Unlock()
			if len(replies) > 0 && !ft.opts.silent {
				_, _ = ft.pty.Write(replies)
			}
		}
		if err != nil {
			return
		}
	}
}

func (ft *fakeTerm) answer(q []byte) []byte {
	s := string(q)
	switch {
	case strings.HasPrefix(s, "\x1b]10;"):
		return []byte("\x1b]10;" + ft.opts.fg + "\x1b\\")
	case strings.HasPrefix(s, "\x1b]11;"):
		return []byte("\x1b]11;" + ft.opts.bg + "\x1b\\")
	case strings.HasPrefix(s, "\x1b]12;"):
		return []byte("\x1b]12;" + ft.opts.fg + "\x1b\\")
	case s == "\x1b]7501;?\x1b\\":
		if ft.opts.programStatus {
			return []byte(s)
		}
	case strings.HasPrefix(s, "\x1b]4;"):
		idx := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(s, "\x1b]4;"), "\x1b\\"), "\x07")
		idx = strings.TrimSuffix(idx, ";?")
		return []byte("\x1b]4;" + idx + ";rgb:8080/8080/8080\x1b\\")
	case s == "\x1b[c":
		if ft.opts.sixel {
			return []byte("\x1b[?62;4;22c")
		}
		return []byte("\x1b[?62;22c")
	case s == "\x1b[>q":
		return []byte("\x1bP>|faketerm 1.0\x1b\\")
	case strings.HasPrefix(s, "\x1b_G"):
		if ft.opts.kitty {
			return []byte("\x1b_Gi=31;OK\x1b\\")
		}
	}
	return nil
}

func (ft *fakeTerm) output() string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.out.String()
}

func (ft *fakeTerm) waitFor(re *regexp.Regexp, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m := re.FindString(ft.output()); m != "" {
			return m, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", false
}

func (ft *fakeTerm) exitCode(timeout time.Duration) (int, bool) {
	select {
	case <-ft.done:
	case <-time.After(timeout):
		return 0, false
	}
	if ft.err == nil {
		return 0, true
	}
	if ee, ok := errors.AsType[*exec.ExitError](ft.err); ok {
		if code := ee.ExitCode(); code >= 0 {
			return code, true
		}
		return 128 + signalOf(ee), true
	}
	return -1, true
}

func signalOf(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return int(ws.Signal())
	}
	return 0
}

func (ft *fakeTerm) ttyCooked(t *testing.T) bool {
	t.Helper()
	name := ft.pty.Name()
	if up, ok := ft.pty.(*xpty.UnixPty); ok {
		name = up.SlaveName()
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("open the pty slave: %v", err)
	}
	defer func() { _ = f.Close() }()
	cmd := exec.Command("stty", "-a")
	cmd.Stdin = f
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stty: %v: %s", err, out)
	}
	words := strings.Fields(string(out))
	has := func(w string) bool {
		return slices.Contains(words, w)
	}
	return has("icanon") && has("echo")
}

func pvPaneSize(t *testing.T, base, session, window string) string {
	t.Helper()
	file := fmt.Sprintf("%s/size-%d.txt", base, time.Now().UnixNano())
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", window, "stty size > "+file+"\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && strings.Count(string(b), " ") == 1 && strings.HasSuffix(string(b), "\n") {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the pane never wrote its size")
	return ""
}

func waitPaneSize(t *testing.T, base, session, window, want, why string) {
	t.Helper()
	got := ""
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got = pvPaneSize(t, base, session, window); got == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s: the pane is %q, want %q", why, got, want)
}
