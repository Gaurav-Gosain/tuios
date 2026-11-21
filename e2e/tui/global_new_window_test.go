package tuie2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func writeFakeSSHMulti(t *testing.T, dir string, bases map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh-multi")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("addr=\"$1\"\nshift\n")
	b.WriteString("if [ -e \"")
	b.WriteString(dir)
	b.WriteString("/down-$addr\" ]; then\n  echo \"ssh: connect to host $addr port 22: Connection refused\" >&2\n  exit 255\nfi\n")
	b.WriteString("case \"$addr\" in\n")
	for addr, base := range bases {
		b.WriteString("  ")
		b.WriteString(addr)
		b.WriteString(")\n")
		for _, key := range xdgKeys {
			b.WriteString("    export ")
			b.WriteString(key)
			b.WriteString("=")
			b.WriteString(xdgDir(base, key))
			b.WriteString("\n")
		}
		b.WriteString("    ")
		b.WriteString(recordFakeSSHPid(base))
		b.WriteString("    ;;\n")
	}
	b.WriteString("  *) echo \"ssh: Could not resolve hostname $addr\" >&2; exit 255 ;;\nesac\n")
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

func writeMachinesConfig(t *testing.T, base string, hosts []string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	var body strings.Builder
	for _, h := range hosts {
		body.WriteString("[hosts.")
		body.WriteString(h)
		body.WriteString("]\n")
		body.WriteString("addr = \"someone@")
		body.WriteString(h)
		body.WriteString("box\"\n")
		body.WriteString("command = \"")
		body.WriteString(tuiosBin)
		body.WriteString("\"\n")
		body.WriteString("connect_timeout = 5\n\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeHostAddrs(t *testing.T, base string, hosts map[string]string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	var body strings.Builder
	for name, addr := range hosts {
		body.WriteString("[hosts.")
		body.WriteString(name)
		body.WriteString("]\n")
		body.WriteString("addr = \"")
		body.WriteString(addr)
		body.WriteString("\"\n")
		body.WriteString("command = \"")
		body.WriteString(tuiosBin)
		body.WriteString("\"\n")
		body.WriteString("connect_timeout = 5\n\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

type globalFleet struct {
	here, alpha, beta string
	env               []string
	machine           map[string]string
}

func startGlobalFleet(t *testing.T) *globalFleet {
	t.Helper()
	return startGlobalFleetWith(t, map[string]string{"beta": "someone@betabox"})
}

func startGlobalFleetWith(t *testing.T, alphaHosts map[string]string, alphaConfig ...string) *globalFleet {
	t.Helper()
	f := &globalFleet{here: t.TempDir(), alpha: remoteMachine(t), beta: remoteMachine(t)}
	ssh := writeFakeSSHMulti(t, f.here, map[string]string{
		"someone@alphabox": f.alpha,
		"someone@betabox":  f.beta,
		"someone@herebox":  f.here,
	})
	f.env = []string{"TUIOS_SSH=" + ssh}
	f.machine = map[string]string{
		filepath.Base(f.here):  "here",
		filepath.Base(f.alpha): "alpha",
		filepath.Base(f.beta):  "beta",
	}
	writeHostAddrs(t, f.alpha, alphaHosts)
	if len(alphaConfig) > 0 {
		path := filepath.Join(f.alpha, "XDG_CONFIG_HOME", "tuios", "config.toml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read alpha's config: %v", err)
		}
		if err := os.WriteFile(path, append(data, strings.Join(alphaConfig, "\n")...), 0o600); err != nil {
			t.Fatalf("write alpha's config: %v", err)
		}
	}
	for _, r := range []string{f.beta, f.alpha} {
		if out, err := tuiosCLIEnv(t, r, f.env, "new", "far", "--detach"); err != nil {
			t.Fatalf("start a far daemon: %v\n%s", err, out)
		}
	}
	writeMachinesConfig(t, f.here, []string{"alpha", "beta"})
	killDaemon(t, f.here)
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start this machine's daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, f.here, func(s string) bool {
		return hostLineUp(s, "alpha") && hostLineUp(s, "beta")
	}, "this machine never reported alpha and beta up")
	return f
}

func hostLineUp(listing, name string) bool {
	for line := range strings.SplitSeq(listing, "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, "│", " "))
		if len(fields) > 2 && fields[0] == name && fields[2] == "up" {
			return true
		}
	}
	return false
}

func sessionWindowHosts(t *testing.T, base string, env []string, sess string) map[string]string {
	t.Helper()
	out, err := cliWithin(t, base, env, "list-windows", "-s", sess, "--json")
	if err != nil {
		return nil
	}
	var v struct {
		Windows []struct {
			ID   string `json:"window_id"`
			Host string `json:"host"`
		} `json:"windows"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return nil
	}
	ids := map[string]string{}
	for _, w := range v.Windows {
		ids[w.ID] = w.Host
	}
	return ids
}

var paneMachineMark = regexp.MustCompile(`M=([^\s=]+)=M`)

func (f *globalFleet) paneMachine(t *testing.T, base, sess, id string) string {
	t.Helper()
	cmd := "echo M=$(basename $(dirname $XDG_CONFIG_HOME))=M\n"
	deadline := time.Now().Add(shellTimeout)
	var out string
	sent := false
	for time.Now().Before(deadline) {
		if !sent {
			if _, err := cliWithin(t, base, f.env, "send-text", "-s", sess, "-w", id, cmd); err == nil {
				sent = true
			}
		}
		out, _ = cliWithin(t, base, f.env, "capture-pane", "-s", sess, "-w", id)
		if m := paneMachineMark.FindStringSubmatch(out); m != nil {
			if name, ok := f.machine[m[1]]; ok {
				return name
			}
			return "unknown root " + m[1]
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "no answer from the pane: " + strings.TrimSpace(out)
}

type openPath struct {
	name string
	open func(t *testing.T, term *tuitest.Terminal)
}

var newWindowPaths = []openPath{
	{"prefix c", func(t *testing.T, term *tuitest.Terminal) {
		if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
			t.Fatal(err)
		}
	}},
	{"palette New window", func(t *testing.T, term *tuitest.Terminal) {
		openPaletteRow(t, term, "new window", "New window")
	}},
	{"palette New window on another machine", func(t *testing.T, term *tuitest.Terminal) {
		openPaletteRow(t, term, "another machine", "New window on another machine")
	}},
}

func openPaletteRow(t *testing.T, term *tuitest.Terminal, query, row string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "P"); err != nil {
		t.Fatal(err)
	}
	waitPaletteOpen(t, term, "for "+row)
	if err := term.SendKeys(query); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText(row, uiTimeout); err != nil {
		t.Fatalf("palette never filtered to %q: %v\n%s", row, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
}

const (
	pickerWindowTitle  = "New window on"
	pickerSessionTitle = "New session on"
)

func choosePickerRow(t *testing.T, term *tuitest.Terminal, title, label string, i int, click bool) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return pickerOpen(s, title)
	}, uiTimeout); err != nil {
		t.Fatalf("the machine picker never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, found := findPickerRow(s, title, label)
		return found && !strings.Contains(s.Text(), " loading ")
	}, uiTimeout); err != nil {
		t.Fatalf("the picker never listed %q: %v\n%s", label, err, term.Snapshot())
	}
	if click {
		r, c := pickerRowAt(t, term, title, label)
		clickAt(t, term, c, r, 1)
		return
	}
	_ = i
	if err := term.SendKeys(tuitest.Home); err != nil {
		t.Fatal(err)
	}
	onLabel := func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "› "+label+" ") }
	for range 12 {
		if term.WaitFor(onLabel, 300*time.Millisecond) == nil {
			break
		}
		if err := term.SendKeys(tuitest.Down); err != nil {
			t.Fatal(err)
		}
	}
	if err := term.WaitFor(onLabel, uiTimeout); err != nil {
		t.Fatalf("the cursor never reached %q: %v\n%s", label, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
}

func pickerRowAt(t *testing.T, term *tuitest.Terminal, title, label string) (row, col int) {
	t.Helper()
	if r, c, ok := findPickerRow(term.Screen(), title, label); ok {
		return r, c
	}
	t.Fatalf("no picker row %q on screen:\n%s", label, term.Snapshot())
	return 0, 0
}

func findPickerRow(s tuitest.Screen, title, label string) (row, col int, ok bool) {
	_, rows := s.Size()
	titleRow, titleCol := -1, 0
	for r := range rows {
		line := s.Line(r)
		if titleRow < 0 {
			if isPickerTitle(line, title) {
				titleRow = r
				titleCol = len([]rune(line[:strings.Index(line, title)]))
			}
			continue
		}
		if r <= titleRow+2 || r > titleRow+14 {
			continue
		}
		for from := 0; ; {
			b := strings.Index(line[from:], label)
			if b < 0 {
				break
			}
			col := len([]rune(line[:from+b]))
			if col >= titleCol-2 && col <= titleCol+4 {
				return r, col + 1, true
			}
			from += b + len(label)
		}
	}
	return 0, 0, false
}

func pickerOpen(s tuitest.Screen, title string) bool {
	for line := range strings.SplitSeq(s.Text(), "\n") {
		if isPickerTitle(line, title) {
			return true
		}
	}
	return false
}

func isPickerTitle(line, title string) bool {
	i := strings.Index(line, title)
	return i >= 0 && !strings.HasPrefix(line[i+len(title):], " another")
}

func waitPickerClosed(t *testing.T, term *tuitest.Terminal, title string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !pickerOpen(s, title)
	}, uiTimeout); err != nil {
		t.Fatalf("the picker never closed: %v\n%s", err, term.Snapshot())
	}
}

func waitNewWindow(t *testing.T, base string, env []string, sess string, before map[string]string, within time.Duration) (string, string, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for id, host := range sessionWindowHosts(t, base, env, sess) {
			if _, old := before[id]; !old {
				return id, host, true
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return "", "", false
}

func pickLoops(def int) int {
	if n, err := strconv.Atoi(os.Getenv("TUIOS_E2E_PICK_LOOPS")); err == nil && n > 0 {
		return n
	}
	return def
}

func pickHow(i int, path, label string, click bool) string {
	by := "enter"
	if click {
		by = "click"
	}
	return fmt.Sprintf("#%d %s, %s by %s", i, path, label, by)
}

// TestAPaneInAGlobalSessionRunsOnThePickedMachine is the case with the global
// session on this machine and the client attached here. Every way into the
// picker, by keyboard and by click, for every machine, many times over.
func TestAPaneInAGlobalSessionRunsOnThePickedMachine(t *testing.T) {
	f := startGlobalFleet(t)
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new", "global", "--global"); err != nil {
		t.Fatalf("create the global session: %v\n%s", err, out)
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "global"}, env: f.env})
	if err := term.WaitForText(welcomeText, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}

	rows := []string{thisMachineRow(), "alpha", "beta"}
	wants := []string{"here", "alpha", "beta"}
	loops := pickLoops(54)
	var wrong []string
	for i := range loops {
		path := newWindowPaths[i%len(newWindowPaths)]
		row := (i / len(newWindowPaths)) % len(rows)
		click := (i/(len(newWindowPaths)*len(rows)))%2 == 1
		how := pickHow(i, path.name, rows[row], click)
		t.Logf("%s", how)

		before := sessionWindowHosts(t, f.here, f.env, "global")
		path.open(t, term)
		choosePickerRow(t, term, pickerWindowTitle, rows[row], row, click)
		waitPickerClosed(t, term, pickerWindowTitle)
		id, host, ok := waitNewWindow(t, f.here, f.env, "global", before, 20*time.Second)
		if !ok {
			wrong = append(wrong, how+": no pane appeared in the global session")
			continue
		}
		if got := f.paneMachine(t, f.here, "global", id); got != wants[row] {
			wrong = append(wrong, fmt.Sprintf("%s: the pane runs on %s (recorded host %q)", how, got, host))
		}
	}
	if len(wrong) > 0 {
		t.Fatalf("ASSERTION: %d of %d picks put the pane on the wrong machine:\n%s\n%s",
			len(wrong), loops, strings.Join(wrong, "\n"), term.Snapshot())
	}
	t.Logf("%d of %d picks landed on the picked machine", loops, loops)
}

func sessionIsGlobal(t *testing.T, base string, env []string, name string) (global, restored bool) {
	t.Helper()
	var found bool
	waitUntil(t, fmt.Sprintf("session %q never appeared in ls --json", name), bootTimeout, func() (bool, string) {
		out, err := tuiosCLIEnv(t, base, env, "ls", "--json")
		if err != nil {
			return false, out
		}
		var rows []struct {
			Name     string `json:"name"`
			Global   bool   `json:"global"`
			Restored bool   `json:"restored"`
		}
		if json.Unmarshal([]byte(out), &rows) != nil {
			return false, out
		}
		for _, r := range rows {
			if r.Name == name {
				found, global, restored = true, r.Global, r.Restored
			}
		}
		return found, out
	})
	return global, restored
}

func newWindowAsksForAMachine(t *testing.T, base string, env []string, sess string, windows int, name string) bool {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"attach", sess}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == windows }, bootTimeout); err != nil {
		t.Fatalf("the client never showed the %d windows of %s: %v\n%s", windows, sess, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatal(err)
	}
	asked := term.WaitFor(func(s tuitest.Screen) bool {
		return pickerOpen(s, pickerWindowTitle) || countWindows(s) > windows
	}, uiTimeout) == nil && pickerOpen(term.Screen(), pickerWindowTitle)
	saveFrame(t, term, name)
	if asked {
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatal(err)
		}
		waitPickerClosed(t, term, pickerWindowTitle)
	}
	if out, err := tuiosCLIEnv(t, base, env, "detach-client", "-s", sess); err != nil {
		t.Logf("detach-client: %v: %s", err, out)
	}
	return asked
}

// TestAGlobalSessionStaysGlobalAcrossARestart (#2203): a session made with
// --global, holding a pane here and a pane on alpha, is still global after
// the daemon restarts and restores it. ls --json says so, and prefix c in it
// still asks which machine the new window runs on.
//
// How it could pass wrongly, and what stops it:
//   - the session read from a daemon that never restarted: it must be listed
//     as restored.
//   - the picker never shown in this fixture at all: it must open before the
//     restart, with the same session, keys and hosts.
//   - the mark kept for one restart only (restored, then saved without it):
//     the daemon restarts twice.
//   - the mark never written to the state file, or written and not read
//     back: either way the first restart drops it.
//   - the hosts not back up after the restart, so the picker has one machine
//     and stays shut: the test waits for alpha and beta up each time.
//
// Then it takes the mark out of the saved file, as an older daemon lost it,
// and restarts. The session must come back plain, and set-session-space
// --global must mark it again: ls --json says so, and prefix c in a client
// attached before the call asks for a machine. --global=false with a space
// takes the mark off and files it, and --global takes it out of the space.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "A global session stays
// global across a restart").
func TestAGlobalSessionStaysGlobalAcrossARestart(t *testing.T) {
	f := startGlobalFleet(t)
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new", "work", "--global"); err != nil {
		t.Fatalf("create the global session: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new-window", "local", "-s", "work"); err != nil {
		t.Fatalf("open a pane here: %v\n%s", err, out)
	}
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new-window", "far", "-s", "work", "--host", "alpha"); err != nil {
		t.Fatalf("open a pane on alpha: %v\n%s", err, out)
	}
	if global, _ := sessionIsGlobal(t, f.here, f.env, "work"); !global {
		t.Fatalf("ASSERTION: the session made with --global is not listed as global before any restart")
	}
	if !newWindowAsksForAMachine(t, f.here, f.env, "work", 2, "global-restore-before") {
		t.Fatalf("ASSERTION: prefix c in the global session did not ask for a machine before any restart")
	}

	restart := func(round int, edit func()) {
		t.Helper()
		if out, err := tuiosCLIEnv(t, f.here, f.env, "kill-server"); err != nil {
			t.Fatalf("restart %d: kill-server: %v: %s", round, err, out)
		}
		if edit != nil {
			edit()
		}
		if out, err := tuiosCLIEnv(t, f.here, f.env, "new", fmt.Sprintf("trigger-%d", round), "--detach"); err != nil {
			t.Fatalf("restart %d: start a fresh daemon: %v: %s", round, err, out)
		}
		waitForHostListing(t, f.here, func(s string) bool {
			return hostLineUp(s, "alpha") && hostLineUp(s, "beta")
		}, "alpha and beta never came back up after the restart")
	}
	for round := 1; round <= 2; round++ {
		restart(round, nil)
		global, restored := sessionIsGlobal(t, f.here, f.env, "work")
		if !restored {
			t.Fatalf("restart %d: work is listed but not as restored, so the restart did not happen", round)
		}
		if !global {
			t.Fatalf("ASSERTION: restart %d: the global session came back as a plain local one (ls --json has no global mark)", round)
		}
		if !newWindowAsksForAMachine(t, f.here, f.env, "work", 2, fmt.Sprintf("global-restore-after-%d", round)) {
			t.Fatalf("ASSERTION: restart %d: prefix c in the restored global session opened a pane without asking for a machine", round)
		}
	}

	saved := filepath.Join(xdgDir(f.here, "XDG_STATE_HOME"), "tuios", "sessions", "work.json")
	restart(3, func() {
		data, err := os.ReadFile(saved)
		if err != nil {
			t.Fatalf("read the saved state: %v", err)
		}
		var st map[string]any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&st); err != nil {
			t.Fatalf("parse the saved state: %v", err)
		}
		if st["global"] != true {
			t.Fatalf("ASSERTION: the saved state of the global session has no global mark: %s", data)
		}
		delete(st, "global")
		if data, err = json.Marshal(st); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(saved, data, 0o600); err != nil {
			t.Fatalf("write the saved state: %v", err)
		}
	})
	if global, restored := sessionIsGlobal(t, f.here, f.env, "work"); global || !restored {
		t.Fatalf("the session restored from a file with no mark is listed global=%v restored=%v, want a restored plain session", global, restored)
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "work"}, env: f.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("the client never showed the 2 windows of work: %v\n%s", err, term.Snapshot())
	}
	out, err := tuiosCLIEnv(t, f.here, f.env, "set-session-space", "-s", "work", "--global")
	if err != nil || !strings.Contains(out, "Session work is global.") {
		t.Fatalf("ASSERTION: set-session-space --global did not mark work global: %v: %s", err, out)
	}
	if global, _ := sessionIsGlobal(t, f.here, f.env, "work"); !global {
		t.Fatalf("ASSERTION: set-session-space --global said so, but ls --json does not list work as global")
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return pickerOpen(s, pickerWindowTitle) || countWindows(s) > 2
	}, uiTimeout); err != nil || !pickerOpen(term.Screen(), pickerWindowTitle) {
		saveFrame(t, term, "global-set-again")
		t.Fatalf("ASSERTION: after set-session-space --global, prefix c in the attached client did not ask for a machine: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "global-set-again")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	waitPickerClosed(t, term, pickerWindowTitle)

	if out, err := tuiosCLIEnv(t, f.here, f.env, "set-session-space", "-s", "work", "--global=false", "payments"); err != nil || !strings.Contains(out, "is in space payments") {
		t.Fatalf("ASSERTION: set-session-space --global=false payments: %v: %s", err, out)
	}
	if global, _ := sessionIsGlobal(t, f.here, f.env, "work"); global {
		t.Fatalf("ASSERTION: --global=false left work global")
	}
	if sp := sessionSpaces(t, f.here)["work"]; sp != "payments" {
		t.Fatalf("ASSERTION: after --global=false payments, work is in space %q, want payments", sp)
	}
	if out, err := tuiosCLIEnv(t, f.here, f.env, "set-session-space", "-s", "work", "--global", "payments"); err == nil || !strings.Contains(out, globalSpaceRefusal) {
		t.Fatalf("ASSERTION: --global with a space was not refused with %q: %v: %s", globalSpaceRefusal, err, out)
	}
	if out, err := tuiosCLIEnv(t, f.here, f.env, "set-session-space", "-s", "work", "--global"); err != nil {
		t.Fatalf("set-session-space --global from a space: %v: %s", err, out)
	}
	if sp := sessionSpaces(t, f.here)["work"]; sp != "" {
		t.Fatalf("ASSERTION: marking work global left it in space %q", sp)
	}
}

// TestAHostHoldsManyPanes opens more panes on one machine than a link used to
// carry. Every pane on another machine holds two streams on the link (its
// terminal and its report channel), and the link refused its 33rd stream, so
// the sixteenth pane on a host was refused while the fifteen before it worked.
// The panes are then closed and opened again, so a stream that a closed pane
// keeps shows here as the same refusal.
func TestAHostHoldsManyPanes(t *testing.T) {
	f := startGlobalFleet(t)
	const panes = 24
	for round := range 2 {
		var ids []string
		for i := range panes {
			before := sessionWindowHosts(t, f.here, f.env, "home")
			out, err := tuiosCLIEnv(t, f.here, f.env, "new-window", "-s", "home", "--host", "alpha")
			if err != nil {
				t.Fatalf("ASSERTION: round %d, pane %d on alpha was refused: %v\n%s", round+1, i+1, err, out)
			}
			id, _, ok := waitNewWindow(t, f.here, f.env, "home", before, 10*time.Second)
			if !ok {
				t.Fatalf("round %d, pane %d never appeared", round+1, i+1)
			}
			ids = append(ids, id)
		}
		if got := f.paneMachine(t, f.here, "home", ids[len(ids)-1]); got != "alpha" {
			t.Fatalf("ASSERTION: the last pane runs on %s, not alpha", got)
		}
		for _, id := range ids {
			if out, err := tuiosCLIEnv(t, f.here, f.env, "run-command", "-s", "home", "CloseWindow", id); err != nil {
				t.Fatalf("close %s: %v\n%s", id, err, out)
			}
		}
	}
}

func takeOffline(t *testing.T, dir, addr string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "down-"+addr), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	breakTheLink(t)
}

func cliWithin(t *testing.T, base string, env []string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func thisMachineRow() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "this machine"
	}
	h, _, _ = strings.Cut(h, ".")
	return h + " (this machine)"
}
