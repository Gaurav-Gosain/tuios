//go:build e2e

package e2e

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These tests drive the tmux shim the way Collie's tmux adapter
// (github.com/AltanS/collie, bridge/mux/tmux) drives tmux: from outside any
// pane, with -S naming the shim's socket, one listing call per snapshot,
// replies through a paste buffer, and a control-mode client per session for
// its events.

const sep = "\x1f"

// shimEnv is a tuios install with the shim's tmux link.
type shimEnv struct {
	*env
	tmux string // the link named tmux
	sock string // the shim's socket, for -S
}

func newShimEnv(t *testing.T) *shimEnv {
	e := newEnv(t)
	e.mustRun("new", "--detach", "coll")
	e.waitForSocket(10 * time.Second)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "tmux")
	if err := os.Symlink(e.bin, link); err != nil {
		t.Fatal(err)
	}
	return &shimEnv{env: e, tmux: link, sock: filepath.Join(e.dirs["XDG_RUNTIME_DIR"], "tuios", "tmux", "socket")}
}

// outside is the environment of a tool outside tuios: the isolated XDG
// directories, and none of a pane's variables.
func (s *shimEnv) outside() []string {
	var out []string
	for _, kv := range s.environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_PANE_TOKEN", "TMUX", "TMUX_PANE":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// tmuxRun runs one tmux call through the shim, with stdin, and returns its
// stdout, stderr and exit status.
func (s *shimEnv) tmuxRun(stdin string, args ...string) (string, string, int) {
	s.t.Helper()
	cmd := exec.Command(s.tmux, append([]string{"-S", s.sock}, args...)...)
	cmd.Env = s.outside()
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		s.t.Fatalf("tmux %q: %v", args, err)
	}
	return out.String(), errb.String(), code
}

func (s *shimEnv) mustTmux(stdin string, args ...string) string {
	s.t.Helper()
	out, errs, code := s.tmuxRun(stdin, args...)
	if code != 0 {
		s.t.Fatalf("tmux %q = %d: %s", args, code, errs)
	}
	return out
}

// waitScreen waits until the pane's screen holds want.
func (s *shimEnv) waitScreen(pane, want string) string {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out := s.mustTmux("", "capture-pane", "-p", "-t", pane)
		if strings.Contains(out, want) {
			return out
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("pane %s never showed %q:\n%s", pane, want, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// listing is Collie's snapshot call.
func listing() []string {
	j := func(f ...string) string { return strings.Join(f, sep) }
	return []string{
		"list-sessions", "-F", j("S", "#{session_id}", "#{session_windows}", "#{session_activity}", "#{session_path}", "#{session_name}"), ";",
		"list-windows", "-a", "-F", j("W", "#{window_id}", "#{session_id}", "#{window_index}", "#{window_active}", "#{window_panes}", "#{automatic-rename}", "#{window_name}"), ";",
		"list-panes", "-a", "-F", j("P", "#{pane_id}", "#{window_id}", "#{session_id}", "#{pane_dead}", "#{pane_active}", "#{window_active}", "#{pane_height}", "#{history_size}", "#{host}", "#{pane_current_path}", "#{pane_current_command}", "#{pane_title}"), ";",
		"list-clients", "-F", j("C", "#{client_session}", "#{client_control_mode}", "#{client_activity}", "#{client_tty}"),
	}
}

func tagged(out, tag string) [][]string {
	var rows [][]string
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Split(line, sep); f[0] == tag {
			rows = append(rows, f[1:])
		}
	}
	return rows
}

// TestTmuxShimTheWayCollieDrivesIt runs Collie's calls against a real daemon.
func TestTmuxShimTheWayCollieDrivesIt(t *testing.T) {
	s := newShimEnv(t)

	if out := s.mustTmux("", "display-message", "-p", "-F", "#{version}"); out != "3.4\n" {
		t.Errorf("version = %q", out)
	}
	if out := s.mustTmux("", "show-options", "-gv", "window-size"); out != "latest\n" {
		t.Errorf("window-size = %q", out)
	}

	out := s.mustTmux("", listing()...)
	sessions, panes := tagged(out, "S"), tagged(out, "P")
	if len(sessions) != 1 || sessions[0][4] != "coll" || sessions[0][2] == "" || sessions[0][2] == "0" {
		t.Fatalf("sessions = %q", sessions)
	}
	if len(panes) != 1 || !strings.HasPrefix(panes[0][0], "%") || panes[0][1] != "@"+strings.TrimPrefix(sessions[0][0], "$")+"001" {
		t.Fatalf("panes = %q (session %s)", panes, sessions[0][0])
	}
	sessID, pane := sessions[0][0], panes[0][0]
	if panes[0][10] == "" {
		t.Errorf("pane_current_command is empty: %q", panes[0])
	}

	// Keys, as Collie's sendKeys sends them.
	s.mustTmux("", "send-keys", "-t", pane, "--", "e", "c", "h", "o", "Space", "k", "e", "y", "s", "-", "o", "k", "Enter")
	s.waitScreen(pane, "\nkeys-ok")

	// A reply, as Collie's typeText sends it: the text on stdin.
	s.mustTmux("echo reply-ok;\n", "load-buffer", "-b", "collie-type", "-", ";", "paste-buffer", "-d", "-b", "collie-type", "-t", pane, "-s", "\n")
	s.waitScreen(pane, "\nreply-ok")
	if _, errs, code := s.tmuxRun("", "paste-buffer", "-b", "collie-type", "-t", pane); code == 0 || !strings.Contains(errs, "no buffer collie-type") {
		t.Errorf("the buffer outlived paste-buffer -d: %d %q", code, errs)
	}

	// Scrollback reads.
	if out := s.mustTmux("", "capture-pane", "-p", "-e", "-t", pane, "-S", "-50"); !strings.Contains(out, "reply-ok") {
		t.Errorf("capture-pane -S -50 lacks the reply:\n%s", out)
	}

	// A tab and a space.
	created := "#{pane_id}" + sep + "#{window_id}" + sep + "#{session_id}" + sep + "#{session_name}" + sep + "#{pane_current_path}"
	tab := strings.Split(strings.TrimSpace(s.mustTmux("", "new-window", "-d", "-t", sessID, "-P", "-F", created, "-c", "/tmp", "-n", "tab2")), sep)
	if len(tab) != 5 || tab[2] != sessID || tab[3] != "coll" {
		t.Fatalf("new-window printed %q", tab)
	}
	s.mustTmux("", "rename-window", "-t", tab[1], "--", "renamed")
	space := strings.Split(strings.TrimSpace(s.mustTmux("", "new-session", "-d", "-P", "-F", created, "-c", "/tmp", "-s", "coll2")), sep)
	if len(space) != 5 || space[3] != "coll2" || space[4] != "/tmp" {
		t.Fatalf("new-session printed %q", space)
	}
	out = s.mustTmux("", listing()...)
	if n := len(tagged(out, "S")); n != 2 {
		t.Errorf("%d sessions after new-session, want 2", n)
	}
	if !strings.Contains(out, sep+"renamed\n") {
		t.Errorf("the renamed tab is not listed:\n%q", out)
	}

	// Rename and close, as Collie does.
	s.mustTmux("", "select-pane", "-t", tab[0], "-T", "label")
	s.mustTmux("", "kill-pane", "-t", tab[0])
	if out := s.mustTmux("", "list-panes", "-a", "-F", "#{pane_id}"); strings.Contains(out, tab[0]) {
		t.Errorf("kill-pane left %s:\n%s", tab[0], out)
	}

	// No real tmux ever started on the shim's socket.
	if _, err := os.Stat(s.sock); err == nil {
		t.Errorf("a file exists at the shim's socket path %s", s.sock)
	}
}

// TestTmuxShimControlModeEvents attaches a control client the way Collie's
// watch does and checks the daemon's changes come out as tmux notifications.
func TestTmuxShimControlModeEvents(t *testing.T) {
	s := newShimEnv(t)
	sessID := strings.TrimSpace(s.mustTmux("", "list-sessions", "-F", "#{session_id}"))
	pane := strings.TrimSpace(s.mustTmux("", "list-panes", "-a", "-F", "#{pane_id}"))

	cmd := exec.Command(s.tmux, "-S", s.sock, "-C", "attach-session", "-t", sessID, "-f", "ignore-size,read-only")
	cmd.Env = s.outside()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	lines := make(chan string, 1024)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	until := func(re string) string {
		t.Helper()
		rx := regexp.MustCompile(re)
		deadline := time.After(10 * time.Second)
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					t.Fatalf("the control client ended before %s", re)
				}
				if rx.MatchString(l) {
					return l
				}
			case <-deadline:
				t.Fatalf("no line matching %s within 10s", re)
			}
		}
	}
	until(`^%begin \d+ 1 0$`)
	until(`^%end \d+ 1 0$`)
	until(`^%session-changed ` + regexp.QuoteMeta(sessID) + ` coll$`)

	// Output in the pane.
	s.mustTmux("", "send-keys", "-t", pane, "echo out", "Enter")
	until(`^%output ` + pane + ` $`)

	// A tab opens and closes.
	win := strings.TrimSpace(s.mustTmux("", "new-window", "-d", "-t", sessID, "-P", "-F", "#{window_id}"))
	until(`^%window-add ` + win + `$`)
	s.mustTmux("", "rename-window", "-t", win, "logs")
	until(`^%window-renamed ` + win + ` logs$`)
	s.mustTmux("", "kill-window", "-t", win)
	until(`^%window-close ` + win + `$`)

	// A command on stdin is framed, and the client is read-only.
	fmt.Fprintln(stdin, "list-sessions -F '#{session_name}'")
	until(`^%begin \d+ \d+ 1$`)
	if l := until(`^coll`); l != "coll" {
		t.Errorf("list-sessions printed %q", l)
	}
	fmt.Fprintln(stdin, "kill-pane -t "+pane)
	until(`^client is read-only$`)
	until(`^%error \d+ \d+ 1$`)

	_ = stdin.Close()
	until(`^%exit$`)
	done := make(chan error, 1)
	go func() { _, _ = io.Copy(io.Discard, stdout); done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the control client exited: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("the control client did not exit after its stdin closed")
	}
}

// TestTmuxShimPasteHeldToGrants runs paste-buffer and send-keys from a pane
// that holds only read, aimed at another pane. The daemon refuses both, and
// nothing is typed there.
func TestTmuxShimPasteHeldToGrants(t *testing.T) {
	s := newShimEnv(t)
	pane := strings.TrimSpace(s.mustTmux("", "list-panes", "-a", "-F", "#{pane_id}"))
	marker := filepath.Join(t.TempDir(), "rc")
	script := strings.Join([]string{
		fmt.Sprintf("printf 'echo pwned-by-paste\\n' | %s -S %s load-buffer -b x -", s.tmux, s.sock),
		fmt.Sprintf("%s -S %s paste-buffer -b x -t %s 2>> %s; echo paste=$? >> %s", s.tmux, s.sock, pane, marker, marker),
		fmt.Sprintf("%s -S %s send-keys -t %s 'echo pwned-by-keys' Enter 2>> %s; echo keys=$? >> %s", s.tmux, s.sock, pane, marker, marker),
		"sleep 30",
	}, "\n")
	c := s.dial()
	c.result(1, "new-window", map[string]any{
		"session": "coll", "focus": false, "grants": []string{"read"},
		"command": []string{"/bin/sh", "-c", script},
	}, 10*time.Second)
	deadline := time.Now().Add(10 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(marker)
		got = string(raw)
		if strings.Contains(got, "keys=") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(got, "paste=1") || !strings.Contains(got, "keys=1") || !strings.Contains(got, "refused for this pane") {
		t.Fatalf("the read-only pane's calls reported %q, want both refused for the pane", got)
	}
	time.Sleep(300 * time.Millisecond)
	if out := s.mustTmux("", "capture-pane", "-p", "-t", pane); strings.Contains(out, "pwned") {
		t.Errorf("text from the read-only pane reached %s:\n%s", pane, out)
	}
}
