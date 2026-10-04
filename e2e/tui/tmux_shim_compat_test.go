package tuie2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The tmux shim answering what common tools send: command prefixes, the
// pane formats and their modifiers, wait-for, run-shell and if-shell,
// buffers and environments, the commands that move panes, display-popup, and
// control mode. Each test drives `tuios tmux` the way a tool outside tuios
// does (no TUIOS_SESSION, so every session is a tmux session), against a real
// daemon, and writes what it ran and read to transcript.txt in its artifact
// directory.

// shimRun is one `tuios tmux` call: its output and exit status.
type shimRun struct {
	out  string
	code int
}

// shimTranscript records the calls of one test and saves them at its end.
type shimTranscript struct {
	t    *testing.T
	base string
	log  strings.Builder
}

func newShimTranscript(t *testing.T, base string) *shimTranscript {
	tr := &shimTranscript{t: t, base: base}
	t.Cleanup(func() {
		path := filepath.Join(artifactDir(t), "transcript.txt")
		if err := os.WriteFile(path, []byte(tr.log.String()), 0o644); err != nil {
			t.Logf("save the transcript: %v", err)
		}
	})
	return tr
}

// tmux runs `tuios tmux args...` with stdin, outside every pane.
func (tr *shimTranscript) tmux(stdin string, args ...string) shimRun {
	tr.t.Helper()
	pinPreV080Looks(tr.t, tr.base)
	cmd := exec.Command(tuiosBin, append([]string{"tmux"}, args...)...)
	cmd.Dir = workDirIn(tr.t, tr.base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TUIOS_SESSION=", "TUIOS_PANE_ID=", "TMUX=", "TMUX_PANE=")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(tr.base, key))
	}
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		tr.t.Fatalf("run tuios tmux %q: %v", args, err)
	}
	fmt.Fprintf(&tr.log, "$ tmux %s\n%s[rc=%d]\n", strings.Join(args, " "), out.String(), code)
	return shimRun{out.String(), code}
}

// ok runs a call that must succeed and returns its output, trimmed.
func (tr *shimTranscript) ok(args ...string) string {
	tr.t.Helper()
	r := tr.tmux("", args...)
	if r.code != 0 {
		tr.t.Fatalf("tmux %q failed with %d: %s", args, r.code, r.out)
	}
	return strings.TrimSpace(r.out)
}

// shimDaemon starts a daemon with detached sessions named names.
func shimDaemon(t *testing.T, names ...string) (string, *shimTranscript) {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	for _, n := range names {
		if out, err := tuiosCLI(t, base, "new", n, "--detach"); err != nil {
			t.Fatalf("new %s: %v\n%s", n, err, out)
		}
	}
	return base, newShimTranscript(t, base)
}

// TestTmuxShimCommandPrefixes: tmux takes any prefix that names one command
// (show-option, list-pa) and refuses an ambiguous one with the commands it
// could be.
//
// NEGATIVE CONTROL: with runOne resolving only full names and aliases (the
// lookup before this change), show-option and list-pa fail with unknown
// command.
func TestTmuxShimCommandPrefixes(t *testing.T) {
	_, tr := shimDaemon(t, "pfx")
	if got := tr.ok("show-option", "-gv", "base-index"); got != "1" {
		t.Errorf("show-option -gv base-index = %q, want 1", got)
	}
	if got := tr.ok("list-pa", "-t", "pfx", "-F", "#{session_name}"); got != "pfx" {
		t.Errorf("list-pa = %q, want pfx", got)
	}
	r := tr.tmux("", "kill-se")
	if r.code == 0 || !strings.Contains(r.out, "ambiguous command: kill-se, could be: kill-server, kill-session") {
		t.Errorf("kill-se = %d %q, want tmux's ambiguous command error", r.code, r.out)
	}
	if r := tr.tmux("", "frobnicate"); r.code == 0 || !strings.Contains(r.out, "unknown command: frobnicate") {
		t.Errorf("frobnicate = %d %q, want unknown command", r.code, r.out)
	}
}

// TestTmuxShimPaneFormats: pane_pid and pane_tty come from the daemon's PTY,
// #{pid} is the daemon's, the edge flags and the zoom flag are filled, the
// modifiers tools use expand as tmux expands them, and history-limit has a
// value when nothing set it.
//
// NEGATIVE CONTROL: with addOnePaneMeta not adding pid and tty, pane_pid and
// pane_tty print empty. With historyLimit reading the default as a number
// (the code before this change), history-limit prints "".
func TestTmuxShimPaneFormats(t *testing.T) {
	base, tr := shimDaemon(t, "fmt")
	line := tr.ok("list-panes", "-t", "fmt", "-F", "#{pane_pid}|#{pane_tty}|#{pid}|#{pane_at_left}#{pane_at_top}#{pane_at_right}#{pane_at_bottom}|#{window_zoomed_flag}|#{=2:session_name}|#{?#{==:#{session_name},fmt},same,other}|#{s/m/M/:session_name}|#{l:#{x}}")
	f := strings.Split(line, "|")
	if len(f) != 9 {
		t.Fatalf("list-panes printed %q, want 9 fields", line)
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		t.Fatalf("pane_pid = %q, want the pane's process", f[0])
	}
	if !strings.HasPrefix(f[1], "/dev/") {
		t.Errorf("pane_tty = %q, want a terminal device", f[1])
	}
	if runtime.GOOS == "linux" {
		// The pane's process has the pane's terminal as its standard input.
		if fd0, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/0", pid)); err != nil || fd0 != f[1] {
			t.Errorf("process %d reads %q (%v), want pane_tty %q", pid, fd0, err, f[1])
		}
	}
	pidFiles, _ := filepath.Glob(filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "*.pid"))
	if len(pidFiles) == 1 {
		raw, _ := os.ReadFile(pidFiles[0])
		if want := strings.TrimSpace(string(raw)); f[2] != want {
			t.Errorf("#{pid} = %q, want the daemon's pid %s", f[2], want)
		}
	} else if f[2] == "" || f[2] == "0" {
		t.Errorf("#{pid} = %q, want the daemon's pid", f[2])
	}
	want := []string{"1111", "0", "fm", "same", "fMt", "#{x}"}
	for i, w := range want {
		if f[3+i] != w {
			t.Errorf("field %d = %q, want %q (line %q)", 3+i, f[3+i], w, line)
		}
	}
	if got := tr.ok("show-options", "-gv", "history-limit"); got != "10000" {
		t.Errorf("history-limit = %q, want 10000", got)
	}
	if got := tr.ok("list-windows", "-t", "fmt", "-F", "#{window_layout}"); !strings.Contains(got, ",") {
		t.Errorf("window_layout = %q, want a layout", got)
	}
}

// TestTmuxShimWaitFor: a signal sent before the wait is kept for it, a wait
// blocks until the signal, and a lock is taken, handed over and released, as
// tmux's channels behave.
//
// NEGATIVE CONTROL: without wait-for in the shim's commands, the first call
// fails with unknown command.
func TestTmuxShimWaitFor(t *testing.T) {
	_, tr := shimDaemon(t, "wf")
	tr.ok("wait-for", "-S", "early")
	done := make(chan shimRun, 1)
	go func() { done <- tr.tmux("", "wait-for", "early") }()
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("wait-for after -S failed: %s", r.out)
		}
	case <-time.After(shellTimeout):
		t.Fatal("wait-for did not take the signal sent before it")
	}

	go func() { done <- tr.tmux("", "wait-for", "late") }()
	select {
	case r := <-done:
		t.Fatalf("wait-for returned before any signal: %d %s", r.code, r.out)
	case <-time.After(time.Second):
	}
	tr.ok("wait-for", "-S", "late")
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("the woken wait-for failed: %s", r.out)
		}
	case <-time.After(shellTimeout):
		t.Fatal("wait-for -S did not wake the waiter")
	}

	tr.ok("wait-for", "-L", "lk")
	go func() { done <- tr.tmux("", "wait-for", "-L", "lk") }()
	select {
	case r := <-done:
		t.Fatalf("a second -L took a held lock: %d %s", r.code, r.out)
	case <-time.After(time.Second):
	}
	tr.ok("wait-for", "-U", "lk")
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("the queued -L failed: %s", r.out)
		}
	case <-time.After(shellTimeout):
		t.Fatal("-U did not hand the lock to the queued -L")
	}
	tr.ok("wait-for", "-U", "lk")
	if r := tr.tmux("", "wait-for", "-U", "lk"); r.code == 0 || !strings.Contains(r.out, "channel lk not locked") {
		t.Errorf("-U of a free lock = %d %q, want channel lk not locked", r.code, r.out)
	}
}

// TestTmuxShimShellCommands: run-shell expands formats, prints the command's
// output and exits with its status; run-shell -C runs tmux commands; if-shell
// picks a branch by a shell command or, with -F, by a format.
//
// NEGATIVE CONTROL: without run-shell and if-shell in the shim's commands,
// every call here fails with unknown command.
func TestTmuxShimShellCommands(t *testing.T) {
	_, tr := shimDaemon(t, "sh")
	if got := tr.ok("run-shell", "-t", "sh", "echo [#{session_name}]"); got != "[sh]" {
		t.Errorf("run-shell = %q, want [sh]", got)
	}
	if r := tr.tmux("", "run-shell", "exit 3"); r.code != 3 || !strings.Contains(r.out, "'exit 3' returned 3") {
		t.Errorf("run-shell 'exit 3' = %d %q, want status 3 and tmux's message", r.code, r.out)
	}
	if got := tr.ok("run-shell", "-C", "display-message -p -t sh #{session_name}"); got != "sh" {
		t.Errorf("run-shell -C = %q, want sh", got)
	}
	if got := tr.ok("if-shell", "-t", "sh", "test #{session_name} = sh", "display -p yes", "display -p no"); got != "yes" {
		t.Errorf("if-shell (true) = %q, want yes", got)
	}
	if got := tr.ok("if-shell", "false", "display -p yes", "display -p no"); got != "no" {
		t.Errorf("if-shell (false) = %q, want no", got)
	}
	if got := tr.ok("if-shell", "-F", "#{==:a,b}", "display -p yes", "display -p no"); got != "no" {
		t.Errorf("if-shell -F = %q, want no", got)
	}
}

// TestTmuxShimBuffersAndEnvironment: buffers are shown, saved and listed as
// tmux shows them, and set-environment reaches a pane the shim opens.
//
// NEGATIVE CONTROL: with split-window passing only its -e values (no
// paneEnvFor), the new pane writes an empty SHIM_ENV_MARK.
func TestTmuxShimBuffersAndEnvironment(t *testing.T) {
	base, tr := shimDaemon(t, "buf")
	tr.ok("set-buffer", "-b", "notes", "hello\tworld")
	if r := tr.tmux("", "show-buffer", "-b", "notes"); r.code != 0 || r.out != "hello\tworld" {
		t.Errorf("show-buffer = %d %q, want the buffer with no line feed added", r.code, r.out)
	}
	if got := tr.ok("list-buffers"); got != `notes: 11 bytes: "hello\tworld"` {
		t.Errorf("list-buffers = %q", got)
	}
	saved := filepath.Join(base, "saved.txt")
	tr.ok("save-buffer", "-b", "notes", saved)
	if raw, err := os.ReadFile(saved); err != nil || string(raw) != "hello\tworld" {
		t.Errorf("save-buffer wrote %q (%v)", raw, err)
	}
	if r := tr.tmux("", "show-buffer", "-b", "nope"); r.code == 0 || !strings.Contains(r.out, "no buffer nope") {
		t.Errorf("show-buffer of a missing buffer = %d %q", r.code, r.out)
	}

	tr.ok("set-environment", "-g", "SHIM_ENV_MARK", "global-value")
	if got := tr.ok("show-environment", "-g", "SHIM_ENV_MARK"); got != "SHIM_ENV_MARK=global-value" {
		t.Errorf("show-environment -g = %q", got)
	}
	tr.ok("set-environment", "-t", "buf", "SHIM_SESSION_MARK", "session-value")
	if got := tr.ok("show-environment", "-t", "buf", "-s", "SHIM_SESSION_MARK"); got != `SHIM_SESSION_MARK="session-value"; export SHIM_SESSION_MARK;` {
		t.Errorf("show-environment -s = %q", got)
	}
	out := filepath.Join(base, "pane-env.txt")
	tr.ok("split-window", "-d", "-t", "buf", "echo \"$SHIM_ENV_MARK/$SHIM_SESSION_MARK\" > "+out)
	deadline := time.Now().Add(shellTimeout)
	var raw []byte
	for time.Now().Before(deadline) {
		if raw, _ = os.ReadFile(out); len(raw) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := strings.TrimSpace(string(raw)); got != "global-value/session-value" {
		t.Errorf("the new pane saw %q, want global-value/session-value", got)
	}
}

// TestTmuxShimMovesPanes: break-pane, join-pane, next-window,
// previous-window, last-pane and rename-session move what tmux moves, and
// select-pane with a direction answers on a session no client shows.
//
// NEGATIVE CONTROL: with select-pane -L sent to the daemon as a direction
// (the code before this change), it fails on the detached session because
// the daemon answers a direction only with a client attached.
func TestTmuxShimMovesPanes(t *testing.T) {
	_, tr := shimDaemon(t, "mv")
	tr.ok("split-window", "-d", "-t", "mv")
	if r := tr.tmux("", "select-pane", "-t", "mv", "-L"); r.code != 0 {
		t.Errorf("select-pane -t mv -L on a detached session failed: %s", r.out)
	}
	panes := strings.Fields(tr.ok("list-panes", "-t", "mv", "-F", "#{pane_id}"))
	if len(panes) != 2 {
		t.Fatalf("panes = %v, want 2", panes)
	}
	tr.ok("select-pane", "-t", panes[0])
	tr.ok("select-pane", "-t", panes[1])
	tr.ok("last-pane", "-t", "mv")
	if got := tr.ok("display-message", "-p", "-t", "mv", "#{pane_id}"); got != panes[0] {
		t.Errorf("after last-pane the active pane is %s, want %s", got, panes[0])
	}
	got := tr.ok("break-pane", "-d", "-s", panes[1], "-P", "-F", "#{window_index} #{pane_id}")
	if got != "2 "+panes[1] {
		t.Errorf("break-pane -P = %q, want %q", got, "2 "+panes[1])
	}
	if got := tr.ok("list-windows", "-t", "mv", "-F", "#{window_index}:#{window_panes}"); got != "1:1\n2:1" {
		t.Errorf("windows after break-pane = %q", got)
	}
	tr.ok("next-window", "-t", "mv")
	if got := tr.ok("list-windows", "-t", "mv", "-F", "#{window_index}#{window_flags}"); got != "1\n2*" {
		t.Errorf("after next-window the window is %s, want 2", got)
	}
	tr.ok("previous-window", "-t", "mv")
	if got := tr.ok("list-windows", "-t", "mv", "-F", "#{window_index}#{window_flags}"); got != "1*\n2" {
		t.Errorf("after previous-window the window is %s, want 1", got)
	}
	tr.ok("join-pane", "-d", "-s", panes[1], "-t", panes[0])
	if got := tr.ok("list-windows", "-t", "mv", "-F", "#{window_index}:#{window_panes}"); got != "1:2" {
		t.Errorf("windows after join-pane = %q", got)
	}
	if r := tr.tmux("", "swap-pane", "-s", panes[0], "-t", panes[1]); r.code == 0 {
		t.Errorf("swap-pane succeeded, but tuios cannot swap panes: %s", r.out)
	}
	tr.ok("rename-session", "-t", "mv", "moved")
	if got := tr.ok("list-sessions", "-F", "#{session_name}"); got != "moved" {
		t.Errorf("sessions after rename-session = %q", got)
	}
}

// TestTmuxShimSelectPaneDirection: select-pane -t PANE -L (and the others)
// moves to the target's neighbour, worked out from the tiled layout, from
// every pane in every direction that has one.
//
// NEGATIVE CONTROL: with select-pane -L sent to the daemon as a direction,
// the move starts from the focused pane rather than the target, and lands
// outside the target's neighbours.
func TestTmuxShimSelectPaneDirection(t *testing.T) {
	term, base := tiledPanes(t, "shimdir", "", 4)
	tr := newShimTranscript(t, base)
	rects := waitForSettledGeometryIn(t, base, "shimdir", 4)
	ids := map[string]string{}
	for _, l := range strings.Split(tr.ok("list-panes", "-t", "shimdir", "-F", "#{tuios_window_id} #{pane_id}"), "\n") {
		if w, p, ok := strings.Cut(l, " "); ok {
			ids[w] = p
		}
	}
	layout, _ := focusedLayout(t, base, "shimdir")
	flags := map[string]string{"left": "-L", "right": "-R", "up": "-U", "down": "-D"}
	tried := 0
	for _, from := range rects {
		for dir, flag := range flags {
			want := touchingNeighbours(from, layout, dir)
			if len(want) == 0 {
				continue
			}
			// Focus a pane that is neither from nor one of its neighbours,
			// so the move has to start at the target and not at the focus.
			other := ""
			for _, r := range rects {
				if r.ID != from.ID && !want[r.ID] {
					other = r.ID
				}
			}
			if other == "" {
				continue
			}
			tr.ok("select-pane", "-t", ids[other])
			waitFocused(t, base, "shimdir", map[string]bool{other: true})
			tr.ok("select-pane", "-t", ids[from.ID], flag)
			if focused := waitFocused(t, base, "shimdir", want); !want[focused] {
				t.Errorf("select-pane -t %s %s focused %s, want one of %v", ids[from.ID], flag, ids[focused], keysOf(want))
			}
			tried++
		}
	}
	if tried == 0 {
		t.Fatal("no pane had a neighbour: the layout was not tiled")
	}
	saveArtifact(t, term, artifactDir(t), "shim-direction")
}

// waitFocused polls until the session's focused window is one of want, and
// returns the last one it read.
func waitFocused(t *testing.T, base, session string, want map[string]bool) string {
	t.Helper()
	focused := ""
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, focused = focusedLayout(t, base, session); want[focused] {
			return focused
		}
		time.Sleep(100 * time.Millisecond)
	}
	return focused
}

// TestTmuxShimControlModeTargetsItsSession: a control client attached to one
// session runs its commands there, though a person looks at another session.
//
// NEGATIVE CONTROL: with callerPane not consulting the attached session,
// display-message and list-windows answer for e2e-ctrlp, the session the
// person's client shows.
func TestTmuxShimControlModeTargetsItsSession(t *testing.T) {
	term, base := attachClientBase(t)
	if out, err := tuiosCLI(t, base, "new", "ctlb", "--detach"); err != nil {
		t.Fatalf("new ctlb: %v\n%s", err, out)
	}
	tr := newShimTranscript(t, base)
	r := tr.tmux("display-message -p 'S=#{session_name}'\nlist-windows -F 'W=#{session_name}'\n", "-C", "attach-session", "-t", "ctlb")
	if r.code != 0 {
		t.Fatalf("control mode failed: %s", r.out)
	}
	if !strings.Contains(r.out, "S=ctlb") || !strings.Contains(r.out, "W=ctlb") || strings.Contains(r.out, "=e2e-ctrlp") {
		t.Errorf("control client attached to ctlb answered for another session:\n%s", r.out)
	}
	alive(t, term, "after a control client used another session")
}

// TestTmuxShimDisplayPopupWaits: display-popup opens a tuios popup on the
// client and returns only when the popup's command exits, which is what fzf
// --tmux waits for before it reads the choice.
//
// NEGATIVE CONTROL: without display-popup in the shim's commands, the call
// returns at once with unknown command and no popup draws.
func TestTmuxShimDisplayPopupWaits(t *testing.T) {
	term, base := attachClientBase(t)
	tr := newShimTranscript(t, base)
	done := filepath.Join(base, "popup-done")
	result := make(chan shimRun, 1)
	go func() {
		result <- tr.tmux("", "display-popup", "-E", "-t", "e2e-ctrlp", "-w", "60%", "-h", "40%",
			"-d", base, "echo POPUP_''UP; sleep 2; echo ok > "+done)
	}()
	if err := term.WaitForText("POPUP_UP", shellTimeout); err != nil {
		select {
		case r := <-result:
			t.Fatalf("no popup drew; the call returned %d %q", r.code, r.out)
		default:
		}
		t.Fatalf("the popup never drew: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "popup-open")
	select {
	case r := <-result:
		if r.code != 0 {
			t.Fatalf("display-popup failed: %d %q", r.code, r.out)
		}
		if _, err := os.Stat(done); err != nil {
			t.Errorf("display-popup returned before its command finished: %v", err)
		}
	case <-time.After(shellTimeout):
		t.Fatal("display-popup did not return after its command exited")
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "POPUP_UP") }, uiTimeout); err != nil {
		t.Errorf("the popup did not close: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after display-popup")
}
