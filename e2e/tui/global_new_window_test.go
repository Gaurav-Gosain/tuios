package tuie2e

import (
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

// Which machine a new pane in a global session runs on.
//
// A global session holds panes from several machines, and every new pane in
// it asks which one. The report was that the answer did not always hold: a
// pick of this machine ran a shell on some other machine, and a pick of
// another machine made nothing the user could see.
//
// These tests set up three real daemons (this machine, alpha and beta), open
// panes through every way into the picker many times, and ask each pane's own
// shell which daemon started it. The daemon's window list is not enough: it
// says what was asked for, and the bug was a pane that went somewhere else.

// writeFakeSSHMulti is writeFakeSSHTo for several machines: the address picks
// which daemon the command reaches. See takeOffline for the down switch.
func writeFakeSSHMulti(t *testing.T, dir string, bases map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh-multi")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("addr=\"$1\"\nshift\n")
	// A file down-ADDR in dir takes the machine off the network: every new
	// connection to it is refused, the way ssh refuses a powered-off box.
	b.WriteString("if [ -e \"" + dir + "/down-$addr\" ]; then\n  echo \"ssh: connect to host $addr port 22: Connection refused\" >&2\n  exit 255\nfi\n")
	b.WriteString("case \"$addr\" in\n")
	for addr, base := range bases {
		b.WriteString("  " + addr + ")\n")
		for _, key := range xdgKeys {
			b.WriteString("    export " + key + "=" + xdgDir(base, key) + "\n")
		}
		b.WriteString("    ;;\n")
	}
	b.WriteString("  *) echo \"ssh: Could not resolve hostname $addr\" >&2; exit 255 ;;\nesac\n")
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

// writeMachinesConfig names every host in hosts at an address the stand-in
// routes by: host h is at someone@hbox.
func writeMachinesConfig(t *testing.T, base string, hosts []string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	var body strings.Builder
	for _, h := range hosts {
		body.WriteString("[hosts." + h + "]\n")
		body.WriteString("addr = \"someone@" + h + "box\"\n")
		body.WriteString("command = \"" + tuiosBin + "\"\n")
		body.WriteString("connect_timeout = 5\n\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// writeHostAddrs writes a [hosts] table of name to address.
func writeHostAddrs(t *testing.T, base string, hosts map[string]string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	var body strings.Builder
	for name, addr := range hosts {
		body.WriteString("[hosts." + name + "]\n")
		body.WriteString("addr = \"" + addr + "\"\n")
		body.WriteString("command = \"" + tuiosBin + "\"\n")
		body.WriteString("connect_timeout = 5\n\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// globalFleet is this machine and two others, each with its own daemon.
type globalFleet struct {
	here, alpha, beta string
	env               []string
	// machine names the daemon each isolation root is, by the root's last
	// path element, which is what a pane's shell reports.
	machine map[string]string
}

// startGlobalFleet starts alpha and beta, then this machine's daemon with
// both configured, and waits for both links. alpha also knows beta, so a
// session on alpha can hold a pane on beta.
func startGlobalFleet(t *testing.T) *globalFleet {
	t.Helper()
	return startGlobalFleetWith(t, map[string]string{"beta": "someone@betabox"})
}

// startGlobalFleetWith is startGlobalFleet with alpha's own [hosts] table
// given, name to address. someone@herebox is this machine, so alpha can name
// this machine whatever a test needs.
func startGlobalFleetWith(t *testing.T, alphaHosts map[string]string) *globalFleet {
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

// hostLineUp reports whether the `tuios hosts` line for name says up.
func hostLineUp(listing, name string) bool {
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, "│", " "))
		if len(fields) > 2 && fields[0] == name && fields[2] == "up" {
			return true
		}
	}
	return false
}

// sessionWindowHosts is the window ids in a session on the daemon at base, each with
// the host the daemon recorded for it.
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

// paneMachine asks the shell in window id which daemon started it, by the
// isolation root in its environment. Every daemon's panes inherit its own
// XDG_CONFIG_HOME, a pane on another machine included, because the far daemon
// is the one that starts it.
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

// openPath is one way into the machine picker.
type openPath struct {
	name string
	open func(t *testing.T, term *tuitest.Terminal)
}

// newWindowPaths is every way into the window picker that a key or the
// palette reaches. The rail's "+" and the dock call the same NewWindowHere.
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

// openPaletteRow opens the palette with the prefix key, which works in both
// modes, filters it to row and runs the first match.
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

// choosePickerRow picks the row labelled label in the open picker, by the
// keyboard (index i) or by a click on the row.
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
	// The cursor goes down to the row by its label, the way a person finds
	// it, rather than by its place: the rows differ between builds and
	// between the machines the client is attached to. The start is the top.
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

// pickerRowAt is the screen cell of the picker row labelled label. The row is
// searched for below the picker's title, so a pane that prints the same word
// is not clicked.
func pickerRowAt(t *testing.T, term *tuitest.Terminal, title, label string) (row, col int) {
	t.Helper()
	if r, c, ok := findPickerRow(term.Screen(), title, label); ok {
		return r, c
	}
	t.Fatalf("no picker row %q on screen:\n%s", label, term.Snapshot())
	return 0, 0
}

// findPickerRow is pickerRowAt that reports a missing row instead of failing.
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
			continue // the search line and its rule, or past the list
		}
		// A row's label starts in the title's column, give or take the
		// cursor mark. The same word elsewhere on the line (a row's detail,
		// a pane's frame) is in another column.
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

// pickerOpen reports whether the picker titled title is on screen. The
// palette's "New window on another machine" row does not count.
func pickerOpen(s tuitest.Screen, title string) bool {
	for _, line := range strings.Split(s.Text(), "\n") {
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

// waitPickerClosed waits for the picker to leave the screen.
func waitPickerClosed(t *testing.T, term *tuitest.Terminal, title string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !pickerOpen(s, title)
	}, uiTimeout); err != nil {
		t.Fatalf("the picker never closed: %v\n%s", err, term.Snapshot())
	}
}

// waitNewWindow waits for a window id in the session that was not in before,
// and returns it with the host the daemon recorded.
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

// pickLoops is how many picks each test makes. TUIOS_E2E_PICK_LOOPS raises it
// for a soak.
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

	rows := []string{"this machine", "alpha", "beta"}
	wants := []string{"here", "alpha", "beta"}
	// 54 is every path, row and way of choosing three times over, and it puts
	// eighteen panes on each machine, which is past what a link used to carry.
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

// takeOffline makes the machine at addr refuse new connections, then kills
// every open link's far side, so each link redials and only addr's fails.
func takeOffline(t *testing.T, dir, addr string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "down-"+addr), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	breakTheLink(t)
}

// cliWithin is tuiosCLIEnv with a deadline, for the reads a wait loop polls.
// A daemon that stops answering then shows as a failed read, and the loop's
// own deadline still holds.
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
