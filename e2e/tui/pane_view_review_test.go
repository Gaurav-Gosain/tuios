package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The review of tuios pane-view, each finding as a test. Each could pass
// wrongly in the way its comment says, and is held against that.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "pane-view review"): see
// that file for each control and the test it fails.

// pvSession makes a detached session for one test and returns its base dir
// and the window id of its pane.
func pvSession(t *testing.T, name string) (string, string) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	return base, firstWindow(t, base, name).ID
}

// pvReady waits until the viewer shows the pane: a line typed through the
// daemon comes back through pane-view. The prompt is no sign, since it
// differs between shells.
func pvReady(ft *fakeTerm, t *testing.T, base, session, window string) bool {
	t.Helper()
	pvSend(t, base, session, window, "echo RDY-$((6*7))")
	_, ok := ft.waitFor(regexp.MustCompile(`RDY-42`), shellTimeout)
	return ok
}

// pvSend types a line into the pane through the daemon, not through a viewer.
func pvSend(t *testing.T, base, session, window, line string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", window, line+"\n"); err != nil {
		t.Fatalf("send-text: %v: %s", err, out)
	}
}

// TestPaneViewTheme: the pane is told the colours and graphics of the
// terminal pane-view runs in. Both themes are checked, and neither is the
// daemon's default (black, sixel), so an answer the daemon made up passes
// neither.
func TestPaneViewTheme(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bg      string
		want    string
		sixel   bool
		wantDA1 string
	}{
		{"light", "rgb:eeee/f1f1/f5f5", "eeee/f1f1/f5f5", false, "noSixel"},
		{"dark", "rgb:1e1e/1e1e/2e2e", "1e1e/1e1e/2e2e", true, "sixel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, win := pvSession(t, "pvt-"+tc.name)
			ft := startFakeTerm(t, base, 90, 25, fakeTermOpts{fg: "rgb:4c4c/4f4f/6969", bg: tc.bg, sixel: tc.sixel},
				"pane-view", "-s", "pvt-"+tc.name, "-w", win)
			if ok := pvReady(ft, t, base, "pvt-"+tc.name, win); !ok {
				t.Fatalf("pane-view never drew the prompt: %q", ft.output())
			}
			pvSend(t, base, "pvt-"+tc.name, win,
				`bash -c 'stty -echo; printf "\033]11;?\007"; IFS= read -rs -t 2 -d $'"'"'\a'"'"' x; printf "\033[c"; IFS= read -rs -t 2 -d c y; stty echo; echo "B""G=${x#*rgb:}=GB D""A=${y}=AD"'`)
			m, ok := ft.waitFor(regexp.MustCompile(`BG=[^=]*=GB DA=[^=]*=AD`), shellTimeout)
			if !ok {
				t.Fatalf("the pane never printed its answers: %q", ft.output())
			}
			if !strings.Contains(m, "BG="+tc.want) {
				t.Fatalf("ASSERTION: the pane was told the background %q, want %s, the terminal pane-view runs in", m, tc.want)
			}
			da := strings.TrimSuffix(strings.SplitN(m, "DA=", 2)[1], "=AD")
			hasSixel := false
			for p := range strings.SplitSeq(strings.TrimPrefix(da, "\x1b[?"), ";") {
				hasSixel = hasSixel || p == "4"
			}
			if hasSixel != tc.sixel {
				t.Fatalf("ASSERTION: the pane's DA1 is %q: sixel %v, want %v, as the terminal pane-view runs in said", da, hasSixel, tc.sixel)
			}
		})
	}
}

// TestPaneViewSizes: the pane's size follows its viewers and its clients.
//   - Two viewers: the larger decides, and the smaller takes over when the
//     larger leaves. A later viewer winning fails the first check.
//   - The last viewer leaving gives the pane back its own size. The pane's
//     own size is read before any viewer, so a pane left at a viewer's size
//     fails.
//   - A client that attaches decides; when it detaches, the viewer sizes the
//     pane again. A viewer that only resized on its own changes would leave
//     the pane at the client's size.
//   - A rename does not stop the viewer: it resizes the pane after one.
func TestPaneViewSizes(t *testing.T) {
	base, win := pvSession(t, "pvs")
	own := pvPaneSize(t, base, "pvs", win)

	// One viewer comes and goes: the pane takes its size, then its own back.
	solo := startFakeTerm(t, base, 120, 36, fakeTermOpts{bg: "rgb:0000/0000/0000"}, "pane-view", "-s", "pvs", "-w", win)
	waitPaneSize(t, base, "pvs", win, "36 120", "one viewer")
	_ = solo.cmd.Process.Signal(syscall.SIGTERM)
	waitPaneSize(t, base, "pvs", win, own, "the viewer left, and the pane kept its size")

	a := startFakeTerm(t, base, 100, 30, fakeTermOpts{bg: "rgb:0000/0000/0000"}, "pane-view", "-s", "pvs", "-w", win)
	waitPaneSize(t, base, "pvs", win, "30 100", "one viewer")
	b := startFakeTerm(t, base, 70, 20, fakeTermOpts{bg: "rgb:0000/0000/0000"}, "pane-view", "-s", "pvs", "-w", win)
	time.Sleep(time.Second)
	waitPaneSize(t, base, "pvs", win, "30 100", "a smaller second viewer must not shrink the pane while the larger one shows it")
	_ = a.cmd.Process.Signal(syscall.SIGTERM)
	waitPaneSize(t, base, "pvs", win, "20 70", "the larger viewer left")

	// A client attaches, then detaches.
	client := startIn(t, base, startOpts{cols: 60, rows: 18, args: []string{"attach", "pvs"}, daemonDefault: true})
	time.Sleep(2 * time.Second)
	if got := pvPaneSize(t, base, "pvs", win); got == "20 70" {
		t.Fatalf("ASSERTION: a client attached and the pane kept the viewer's size %s", got)
	}
	_ = client.Close()
	waitPaneSize(t, base, "pvs", win, "20 70", "the client detached, and the viewer did not size the pane again")

	// A rename, then the viewer's terminal grows.
	if out, err := tuiosCLI(t, base, "rename-session", "-s", "pvs", "pvs2"); err != nil {
		t.Fatalf("rename: %v: %s", err, out)
	}
	if err := b.pty.Resize(110, 32); err != nil {
		t.Fatalf("resize: %v", err)
	}
	waitPaneSize(t, base, "pvs2", win, "32 110", "after a rename the viewer must still size the pane")

	_ = b.cmd.Process.Signal(syscall.SIGTERM)
	// Its own size is now the one the client asked for, not a viewer's.
	time.Sleep(time.Second)
	if got := pvPaneSize(t, base, "pvs2", win); got == "32 110" || got == "20 70" {
		t.Fatalf("ASSERTION: the last viewer left, and the pane kept a viewer's size %s", got)
	}
}

// TestPaneViewExit: pane-view's exit status and what it leaves the terminal
// in.
func TestPaneViewExit(t *testing.T) {
	base, win := pvSession(t, "pve")
	opts := fakeTermOpts{bg: "rgb:0000/0000/0000"}

	// SIGTERM: the terminal comes back cooked, and the status is 128+15.
	ft := startFakeTerm(t, base, 80, 24, opts, "pane-view", "-s", "pve", "-w", win)
	if ok := pvReady(ft, t, base, "pve", win); !ok {
		t.Fatalf("no prompt: %q", ft.output())
	}
	_ = ft.cmd.Process.Signal(syscall.SIGTERM)
	code, ok := ft.exitCode(10 * time.Second)
	if !ok || code != 143 {
		t.Fatalf("ASSERTION: pane-view ended by SIGTERM exited %d (done %v), want 143", code, ok)
	}
	if !ft.ttyCooked(t) {
		t.Fatalf("ASSERTION: SIGTERM left the terminal raw")
	}

	// The pane's program killed by SIGKILL: 128+9.
	ft = startFakeTerm(t, base, 80, 24, opts, "pane-view", "-s", "pve", "-w", win)
	if ok := pvReady(ft, t, base, "pve", win); !ok {
		t.Fatalf("no prompt: %q", ft.output())
	}
	pvSend(t, base, "pve", win, `echo PID=$$=DIP`)
	m, ok := ft.waitFor(regexp.MustCompile(`PID=\d+=DIP`), shellTimeout)
	if !ok {
		t.Fatalf("no pid: %q", ft.output())
	}
	pid, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(m, "PID="), "=DIP"))
	_ = syscall.Kill(pid, syscall.SIGKILL)
	if code, ok := ft.exitCode(15 * time.Second); !ok || code != 137 {
		t.Fatalf("ASSERTION: pane-view of a pane whose program got SIGKILL exited %d (done %v), want 137", code, ok)
	}

	// A window closed while its program runs: status 3, and no error printed.
	// Three times: tuios ends the program with a signal as it closes the
	// window, and which of the two the stream sees first is a race.
	base2 := ""
	for round := range 3 {
		// Each round closes a session's only window, which is when the
		// race showed: the window and its session go together.
		name := fmt.Sprintf("pvc%d", round)
		var win2 string
		base2, win2 = pvSession(t, name)
		ft = startFakeTerm(t, base2, 80, 24, opts, "pane-view", "-s", name, "-w", win2)
		if ok := pvReady(ft, t, base2, name, win2); !ok {
			t.Fatalf("no prompt: %q", ft.output())
		}
		pvSend(t, base2, name, win2, "sleep 300")
		time.Sleep(500 * time.Millisecond)
		if out, err := tuiosCLI(t, base2, "close-window", "-s", name, win2); err != nil {
			t.Fatalf("close-window: %v: %s", err, out)
		}
		code, ok = ft.exitCode(15 * time.Second)
		if !ok || code != 3 {
			t.Fatalf("ASSERTION: round %d: pane-view of a closed window exited %d (done %v), want 3", round, code, ok)
		}
		if out := ft.output(); strings.Contains(strings.ToLower(out), "error") {
			t.Fatalf("ASSERTION: a closed window printed an error: %q", out[max(0, len(out)-400):])
		}
	}

	// A window that does not exist fails at once.
	start := time.Now()
	if _, err := tuiosCLI(t, base2, "new", "pvc", "--detach"); err != nil {
		t.Fatalf("new: %v", err)
	}
	ft = startFakeTerm(t, base2, 80, 24, opts, "pane-view", "-s", "pvc", "-w", "no-such-window")
	code, ok = ft.exitCode(10 * time.Second)
	if !ok || code != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("ASSERTION: pane-view of a missing window exited %d (done %v) after %s, want 1 at once", code, ok, time.Since(start))
	}
}

// TestPaneViewFilter: what reaches the terminal pane-view runs in. Each case
// is printed between markers, so the bytes between them are exactly what the
// filter let through.
func TestPaneViewFilter(t *testing.T) {
	base, win := pvSession(t, "pvf")
	ft := startFakeTerm(t, base, 120, 30, fakeTermOpts{bg: "rgb:0000/0000/0000"}, "pane-view", "-s", "pvf", "-w", win)
	if ok := pvReady(ft, t, base, "pvf", win); !ok {
		t.Fatalf("no prompt: %q", ft.output())
	}
	cases := []struct {
		name string
		seq  string // printf format
		keep bool
		want string // when kept, a part that must reach the terminal
	}{
		{"title ending ;?", `\033]2;build;?\007`, true, "\x1b]2;build;?"},
		{"modifyOtherKeys reset", `\033[>4n`, true, "\x1b[>4n"},
		// A terminal answers an XTSMGRAPHICS set as it answers a read, and
		// the daemon has answered it already.
		{"XTSMGRAPHICS set", `\033[?1;3;256S`, false, ""},
		{"kitty q=1 forced quiet", `\033_Ga=d,q=1\033\\`, true, "\x1b_Gq=2,a=d\x1b\\"},
		{"OSC 21 query", `\033]21;foreground=?\033\\`, false, ""},
		{"DECRQPSR", `\033[1\$w`, false, ""},
		{"DECRQTSR", `\033[1\$u`, false, ""},
		{"DECREQTPARM", `\033[x`, false, ""},
		{"DECRQCRA", `\033[1;1;1;1;5;5*y`, false, ""},
		{"DA1", `\033[c`, false, ""},
		{"unterminated OSC then text", `\033]0;no end\033[31mSEEN\033[0m`, true, "SEEN"},
	}
	for i, c := range cases {
		open, closeM := fmt.Sprintf("<<%02d", i), fmt.Sprintf("%02d>>", i)
		// The daemon answers the requests itself, and its answer reaches the
		// pane as input; the read drains it so the next line types clean.
		pvSend(t, base, "pvf", win, fmt.Sprintf(`bash -c 'printf "<<%%02d%s%%02d>>\n" %d %d; IFS= read -rs -t 1 -d "" junk; true'`, c.seq, i, i))
		re := regexp.MustCompile(regexp.QuoteMeta(open) + `(?s)(.*?)` + regexp.QuoteMeta(closeM))
		var got string
		found := false
		deadline := time.Now().Add(shellTimeout)
		for time.Now().Before(deadline) {
			// The echo of the typed line also holds the markers, as
			// literal %02d; the printed one holds the number.
			if ms := re.FindAllStringSubmatch(ft.output(), -1); len(ms) > 0 {
				got, found = ms[len(ms)-1][1], true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !found {
			out := ft.output()
			t.Fatalf("%s: the markers never came back (%d bytes): %q", c.name, len(out), out[max(0, len(out)-800):])
		}
		if got == "" && c.keep {
			t.Fatalf("ASSERTION: %s: nothing reached the terminal between the markers: %q", c.name, ft.output())
		}
		if c.keep && !strings.Contains(got, c.want) {
			t.Fatalf("ASSERTION: %s: the terminal got %q, want it to hold %q", c.name, got, c.want)
		}
		if !c.keep && got != "" {
			t.Fatalf("ASSERTION: %s: a request reached the terminal: %q", c.name, got)
		}
		// The drain reads for a second; the next line waits for it.
		time.Sleep(1300 * time.Millisecond)
	}
}

// TestPaneViewTuitestSmoke keeps the tuitest path alive beside the fake
// terminal, with the shell's own prompt and a key typed through it.
func TestPaneViewTuitestSmoke(t *testing.T) {
	base, win := pvSession(t, "pvsmoke")
	view := startIn(t, base, startOpts{cols: 90, rows: 25, animations: true,
		args: []string{"pane-view", "-s", "pvsmoke", "-w", win}})
	if err := view.SendKeys("echo SMOKE-$((40+2))", tuitest.Enter); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := view.WaitForText("SMOKE-42", shellTimeout); err != nil {
		t.Fatalf("ASSERTION: %v", err)
	}
}

// TestPaneViewGone: a pane-view whose stream drops reconnects, but stops at
// once when the daemon says the session is gone. With every error retried it
// waits 20 attempts, about 31 seconds, which the 10 second bound fails.
func TestPaneViewGone(t *testing.T) {
	base, win := pvSession(t, "pvg")
	ft := startFakeTerm(t, base, 80, 24, fakeTermOpts{bg: "rgb:0000/0000/0000"}, "pane-view", "-s", "pvg", "-w", win)
	if ok := pvReady(ft, t, base, "pvg", win); !ok {
		t.Fatalf("no prompt: %q", ft.output())
	}
	// The daemon restarts and restores the session: pane-view resumes on
	// the new daemon and shows the pane again.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "start-server"); err != nil {
		t.Fatalf("start-server: %v: %s", err, out)
	}
	time.Sleep(2 * time.Second)
	pvSend(t, base, "pvg", win, "echo RESUMED-$((20+22))")
	if _, ok := ft.waitFor(regexp.MustCompile(`RESUMED-42`), shellTimeout); !ok {
		out := ft.output()
		t.Fatalf("ASSERTION: pane-view did not resume after the daemon restarted: %q", out[max(0, len(out)-400):])
	}

	// The daemon stops, and starts again without the session: its saved
	// state is removed in between, so nothing restores it.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	if err := os.Remove(filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sessions", "pvg.json")); err != nil {
		t.Fatalf("remove the saved session: %v", err)
	}
	start := time.Now()
	if out, err := tuiosCLI(t, base, "new", "other", "--detach"); err != nil {
		t.Fatalf("restart: %v: %s", err, out)
	}
	code, ok := ft.exitCode(25 * time.Second)
	if !ok || code != 1 || time.Since(start) > 10*time.Second {
		t.Fatalf("ASSERTION: pane-view of a session that is gone exited %d (done %v) after %s, want 1 within 10s", code, ok, time.Since(start))
	}
}
