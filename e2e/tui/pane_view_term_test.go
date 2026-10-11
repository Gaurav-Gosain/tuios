package tuie2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
)

// fakeTerm is a terminal for pane-view that answers what pane-view asks it
// at start (colours, DA1, XTVERSION, kitty graphics) the way a real one does,
// and records every byte pane-view writes. tuitest answers some of those
// requests with fixed values, and the theme test needs to choose them.
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
}

type fakeTermOpts struct {
	fg, bg string // X11 rgb:rrrr/gggg/bbbb
	sixel  bool
	kitty  bool
	// silent answers nothing, as a terminal with no reports does.
	silent bool
}

var fakeTermQuery = regexp.MustCompile(`\x1b\](10|11|12|4;\d+);\?(?:\x07|\x1b\\)|\x1b\[c|\x1b\[>q|\x1b_Gi=31,[^\x1b]*a=q[^\x1b]*\x1b\\`)

// startFakeTerm runs tuios with args on a pty of cols by rows.
func startFakeTerm(t *testing.T, base string, cols, rows int, opts fakeTermOpts, args ...string) *fakeTerm {
	t.Helper()
	p, err := xpty.NewPty(cols, rows)
	if err != nil {
		t.Fatalf("new pty: %v", err)
	}
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TERM=xterm-256color")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
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
		n, err := ft.pty.Read(buf)
		if n > 0 {
			ft.mu.Lock()
			ft.out.Write(buf[:n])
			// Answer the requests in what arrived. A request split across
			// reads is found on the next one: the scan restarts a little
			// before where the last one ended.
			start := max(0, ft.scan-64)
			data := ft.out.Bytes()
			var replies []byte
			for _, loc := range fakeTermQuery.FindAllIndex(data[start:], -1) {
				if start+loc[1] <= ft.scan {
					continue // answered on an earlier read
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

// output is everything pane-view wrote so far.
func (ft *fakeTerm) output() string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.out.String()
}

// waitFor waits until the output matches re, and returns the match.
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

// exitCode waits for pane-view to exit and returns its status: 128 plus the
// signal when a signal ended it.
func (ft *fakeTerm) exitCode(timeout time.Duration) (int, bool) {
	select {
	case <-ft.done:
	case <-time.After(timeout):
		return 0, false
	}
	if ft.err == nil {
		return 0, true
	}
	var ee *exec.ExitError
	if errors.As(ft.err, &ee) {
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

// ttyCooked reports whether the pty's line discipline has ICANON and ECHO
// on, as stty reads them from the slave.
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
		for _, x := range words {
			if x == w {
				return true
			}
		}
		return false
	}
	return has("icanon") && has("echo")
}

// paneSize reads the pane's own size, as its shell sees it, by having the
// shell write `stty size` to a file.
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

// waitPaneSize waits until the pane's size is want ("rows cols").
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
