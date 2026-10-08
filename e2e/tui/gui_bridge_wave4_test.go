package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Wave 4 of the GUI bridge: muxed by default and machines.
//
// A renderer reopens where the person left, and a bridge that dies is
// started again behind emulators the renderer kept. The bridge attaches a
// session by id, then the one the person used last. It sends a "seq" event
// with each snapshot's stream position, and a bridge started with the
// positions the renderer holds (--resume) sends no snapshot for a pane the
// renderer is current on, and only the missing bytes for one that is a
// little behind. The bridge also reaches a session on another machine
// (--host), and sends every machine with its link, round trip and sessions
// (the "hosts" event).

// wireAttached is the attached event's own object.
type wireAttached struct {
	SessionID string `json:"session_id"`
	DaemonPID int    `json:"daemon_pid"`
	Restored  bool   `json:"restored"`
	Host      string `json:"host"`
}

// wireSeq is a "seq" event.
type wireSeq struct {
	PTY  string `json:"pty"`
	Seq  int64  `json:"seq"`
	Kept bool   `json:"kept"`
}

// wireHosts is the "hosts" event's object.
type wireHosts struct {
	Live  bool `json:"live"`
	Hosts []struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Health   string `json:"health"`
		RTTMs    int    `json:"rtt_ms"`
		Sessions []struct {
			Name    string `json:"name"`
			ID      string `json:"id"`
			Windows int    `json:"windows"`
		} `json:"sessions"`
	} `json:"hosts"`
}

// eventsOf decodes every event of type kind into a new T, in order.
func eventsOf[T any](b *guiBridge, kind, field string) []T {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []T
	for _, raw := range b.events {
		var head struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &head) != nil || head.Type != kind {
			continue
		}
		var v T
		if field == "" {
			if json.Unmarshal(raw, &v) == nil {
				out = append(out, v)
			}
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil && json.Unmarshal(m[field], &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// waitFor waits until ok holds, under the bridge's lock.
func (b *guiBridge) waitFor(ok func() bool, d time.Duration, what string) {
	b.t.Helper()
	deadline := time.Now().Add(d)
	b.mu.Lock()
	defer b.mu.Unlock()
	for !ok() {
		if time.Now().After(deadline) {
			b.t.Fatalf("ASSERTION: never saw %s", what)
		}
		b.waitLocked(100 * time.Millisecond)
	}
}

// attachedOf waits for the attached event and returns it.
func attachedOf(b *guiBridge) (string, wireAttached) {
	b.t.Helper()
	type ev struct {
		Message  string       `json:"message"`
		Attached wireAttached `json:"attached"`
	}
	var got []ev
	deadline := time.Now().Add(uiTimeout)
	for len(got) == 0 && time.Now().Before(deadline) {
		got = eventsOf[ev](b, "attached", "")
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) == 0 {
		b.t.Fatalf("ASSERTION: no attached event")
	}
	return got[len(got)-1].Message, got[len(got)-1].Attached
}

// position is the stream position the renderer holds for pty: the last seq
// event plus every OUTPUT byte since it, which is how tuios-gpui counts.
func (b *guiBridge) position(pty string) int64 {
	seqs := eventsOf[wireSeq](b, "seq", "")
	var at int64 = -1
	for _, s := range seqs {
		if s.PTY == pty {
			at = s.Seq
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if at < 0 {
		b.t.Fatalf("ASSERTION: no seq event for %s", pty)
	}
	return at + int64(len(b.outputs[pty]))
}

// The bridge attaches the session the renderer names by id, which a rename
// does not change, and with no id or name the session the person used
// last, never the first one listed.
func TestGUIBridgeAttachesByIDThenLastUsed(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"alpha", "zeta"} {
		if out, err := tuiosCLI(t, base, "new", "-d", name); err != nil {
			t.Fatalf("new %s: %v\n%s", name, err, out)
		}
	}
	// zeta is the newer session and neither is used: the daemon's pick is
	// zeta, and the first one listed is alpha.
	b := startBridgeWith(t, base, bridgeOpts{cols: 100, rows: 30, keepDaemon: true, name: "none"})
	name, info := attachedOf(b)
	if name != "zeta" {
		t.Fatalf("ASSERTION: with no session named, the bridge attached %q, want zeta (the newest; alpha is listed first)", name)
	}
	if info.SessionID == "" || info.DaemonPID == 0 {
		t.Fatalf("ASSERTION: the attached event carries no session id or daemon pid: %+v", info)
	}
	_ = b.in.Close()

	// The person uses alpha in a terminal client: it is the last used now.
	term := startIn(t, base, startOpts{args: []string{"attach", "alpha"}})
	time.Sleep(2 * time.Second)
	if err := term.SendKeys("echo USED\r"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "USED") }, uiTimeout); err != nil {
		t.Fatalf("the shell never echoed: %v", err)
	}
	b = startBridgeWith(t, base, bridgeOpts{cols: 100, rows: 30, keepDaemon: true, name: "used"})
	if name, _ := attachedOf(b); name != "alpha" {
		t.Fatalf("ASSERTION: with no session named, the bridge attached %q, want alpha (used last)", name)
	}
	_ = b.in.Close()

	// By id: zeta's id still finds it after a rename.
	zetaID := info.SessionID
	if out, err := tuiosCLI(t, base, "rename-session", "zeta", "omega"); err != nil {
		t.Fatalf("rename: %v\n%s", err, out)
	}
	b = startBridgeWith(t, base, bridgeOpts{args: []string{"--session-id", zetaID}, cols: 100, rows: 30, keepDaemon: true, name: "byid"})
	name, got := attachedOf(b)
	if name != "omega" || got.SessionID != zetaID {
		t.Fatalf("ASSERTION: --session-id %s attached %q (id %s), want omega", zetaID, name, got.SessionID)
	}
	_ = b.in.Close()

	// An id the daemon does not hold falls back to the last used.
	b = startBridgeWith(t, base, bridgeOpts{args: []string{"--session-id", "no-such-id"}, cols: 100, rows: 30, keepDaemon: true, name: "gone"})
	if name, _ := attachedOf(b); name != "alpha" {
		t.Fatalf("ASSERTION: an unknown id attached %q, want alpha (used last)", name)
	}
}

// A bridge started with the stream positions a renderer kept: no snapshot
// for a pane it is current on, only the bytes it missed for a pane a little
// behind, and a snapshot when the daemon is another one or the ring has
// moved on.
func TestGUIBridgeResumesKeptEmulators(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "kept", 100, 30)
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	st := b.waitState(func(s *wireState) bool { return len(s.Windows) == 1 }, "one pane")
	pty := st.Windows[0].PTY
	_, info := attachedOf(b)
	b.input(pty, "echo FIRST-$((6*7))\r")
	b.waitFor(func() bool { return bytes.Contains(b.outputs[pty], []byte("FIRST-42")) }, shellTimeout, "the first echo")
	time.Sleep(500 * time.Millisecond)
	held := b.position(pty)
	_ = b.in.Close()
	b.waitFor(func() bool { return b.closed }, uiTimeout, "the first bridge to stop")

	resume := func(name string, at int64, pid int) *guiBridge {
		return startBridgeWith(t, base, bridgeOpts{
			args: []string{"--session-id", info.SessionID, "--resume", fmt.Sprintf("%s=%d", pty, at), "--resume-pid", fmt.Sprint(pid)},
			cols: 100, rows: 30, keepDaemon: true, name: name,
		})
	}
	settle := func(b *guiBridge) {
		// The pane is restored and subscribed with the first state; give
		// any snapshot time to arrive.
		time.Sleep(1500 * time.Millisecond)
	}

	// 1. Current: kept, and no snapshot.
	b = resume("current", held, info.DaemonPID)
	settle(b)
	seqs := eventsOf[wireSeq](b, "seq", "")
	if len(seqs) != 1 || !seqs[0].Kept || seqs[0].Seq != held {
		t.Fatalf("ASSERTION: a current emulator was not kept: seq events %+v, held %d", seqs, held)
	}
	if n := b.snaps[pty]; n != 0 {
		t.Fatalf("ASSERTION: a kept emulator got %d snapshots", n)
	}
	_ = b.in.Close()
	b.waitFor(func() bool { return b.closed }, uiTimeout, "the bridge to stop")

	// 2. Behind: the pane prints while no bridge runs. The new bridge sends
	// those bytes as output, and still no snapshot.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "kept", "-l", "echo SECOND-$((6*8))\r"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	time.Sleep(time.Second)
	b = resume("behind", held, info.DaemonPID)
	b.waitFor(func() bool { return bytes.Contains(b.outputs[pty], []byte("SECOND-48")) }, shellTimeout, "the missed output as output")
	settle(b)
	seqs = eventsOf[wireSeq](b, "seq", "")
	if len(seqs) != 1 || !seqs[0].Kept || seqs[0].Seq != held {
		t.Fatalf("ASSERTION: an emulator a little behind was not caught up: seq events %+v, held %d", seqs, held)
	}
	if n := b.snaps[pty]; n != 0 {
		t.Fatalf("ASSERTION: an emulator a little behind got %d snapshots", n)
	}
	// The catch-up starts where the renderer stopped: the first echo is not
	// sent again.
	if bytes.Contains(b.outputs[pty], []byte("FIRST-42")) {
		t.Fatalf("ASSERTION: the catch-up repeats output the renderer had:\n%q", b.outputs[pty])
	}
	now := b.position(pty)
	_ = b.in.Close()
	b.waitFor(func() bool { return b.closed }, uiTimeout, "the bridge to stop")

	// 3. Another daemon pid: the positions are not trusted.
	b = resume("otherpid", now, info.DaemonPID+100000)
	b.waitFor(func() bool { return b.snaps[pty] > 0 }, uiTimeout, "a snapshot for positions from another daemon")
	_ = b.in.Close()
	b.waitFor(func() bool { return b.closed }, uiTimeout, "the bridge to stop")

	// 4. The ring moved on: far more than 64 KiB printed while away.
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "kept", "-l", "seq 1 30000\r"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	time.Sleep(2 * time.Second)
	b = resume("rolled", now, info.DaemonPID)
	b.waitFor(func() bool { return b.snaps[pty] > 0 }, uiTimeout, "a snapshot once the ring has moved past the renderer")
	seqs = eventsOf[wireSeq](b, "seq", "")
	if len(seqs) == 0 || seqs[len(seqs)-1].Kept {
		t.Fatalf("ASSERTION: a rolled ring kept the emulator: %+v", seqs)
	}
}

// A bridge killed as a crash kills it leaves the session as it was: a new
// bridge attaches the same session by id.
func TestGUIBridgeKilledLeavesTheSession(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "crash", 100, 30)
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	b.waitState(func(s *wireState) bool { return len(s.Windows) == 1 }, "one pane")
	_, info := attachedOf(b)
	if err := syscall.Kill(b.pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the bridge: %v", err)
	}
	b.waitFor(func() bool { return b.closed }, uiTimeout, "the killed bridge's output to close")
	out, _ := tuiosCLI(t, base, "ls")
	if !strings.Contains(out, "crash") {
		t.Fatalf("ASSERTION: the session ended with the bridge:\n%s", out)
	}
	// A newer session, so only the id leads back to crash.
	if out, err := tuiosCLI(t, base, "new", "-d", "other"); err != nil {
		t.Fatalf("new other: %v\n%s", err, out)
	}
	b = startBridgeWith(t, base, bridgeOpts{args: []string{"--session-id", info.SessionID}, cols: 100, rows: 30, keepDaemon: true, name: "again"})
	if name, got := attachedOf(b); name != "crash" || got.SessionID != info.SessionID {
		t.Fatalf("ASSERTION: the new bridge attached %q (%s), want crash (%s)", name, got.SessionID, info.SessionID)
	}
}

// A session on another machine through the local daemon's link (--host),
// and the hosts event with that machine, its round trip and its sessions.
func TestGUIBridgeOnAHostAndItsLink(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	if out, err := tuiosCLI(t, remote, "new", "-d", "far"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "│ up ") }, "the link to build comes up")

	// A bridge on this machine sees build in its hosts event.
	b := startBridgeWith(t, base, bridgeOpts{args: []string{"--session", "here"}, env: env, cols: 100, rows: 30, keepDaemon: true, name: "here"})
	row := func() (bool, string) {
		ev := eventsOf[wireHosts](b, "hosts", "hosts")
		if len(ev) == 0 {
			return false, "no hosts event"
		}
		last := ev[len(ev)-1]
		for _, h := range last.Hosts {
			if h.Name == "build" {
				far := false
				for _, s := range h.Sessions {
					far = far || s.Name == "far"
				}
				desc, _ := json.Marshal(h)
				return last.Live && h.Status == "up" && h.Health == "good" && h.RTTMs > 0 && far, string(desc)
			}
		}
		return false, "build not listed"
	}
	deadline := time.Now().Add(30 * time.Second)
	ok, desc := row()
	for !ok && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		ok, desc = row()
	}
	if !ok {
		t.Fatalf("ASSERTION: the hosts event never showed build up, good, with a round trip and its session far: %s", desc)
	}
	t.Logf("hosts event row: %s", desc)
	_ = b.in.Close()

	// A bridge on build's session far.
	b = startBridgeWith(t, base, bridgeOpts{args: []string{"--host", "build", "--session", "far"}, env: env, cols: 100, rows: 30, keepDaemon: true, name: "far"})
	name, info := attachedOf(b)
	if name != "far" || info.Host != "build" || info.SessionID == "" {
		t.Fatalf("ASSERTION: --host build attached %q on %q (id %q), want far on build", name, info.Host, info.SessionID)
	}
	st := b.waitState(func(s *wireState) bool { return len(s.Windows) == 1 }, "far's pane")
	pty := st.Windows[0].PTY
	b.input(pty, "echo FAR-$((6*7))\r")
	b.waitFor(func() bool { return bytes.Contains(b.outputs[pty], []byte("FAR-42")) }, shellTimeout, "the far shell's echo through the bridge")
	// The session is the far daemon's, not this machine's.
	if out, _ := tuiosCLI(t, base, "ls"); strings.Contains(out, "far") {
		t.Fatalf("ASSERTION: this machine's daemon holds far:\n%s", out)
	}
}

// A config reload keeps the bridge's own chrome off. The reload applies the
// file's appearance whole, and before the fix it brought the dock back: the
// panes moved down two rows and the renderer drew them short. Writing an
// option or adding a host reloads the file.
func TestGUIBridgeKeepsItsChromeAcrossAReload(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "chrome", 100, 30)
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	full := func(s *wireState) bool {
		return len(s.Windows) == 1 && s.Windows[0].Y == 0 && s.Windows[0].H == 30
	}
	st := b.waitState(full, "one pane over the whole grid")
	before, _ := json.Marshal(st.Windows[0])
	// The person's file puts the dock at the top, as a config with no dock
	// setting does; the watcher reloads it.
	cfg := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	if err := os.WriteFile(cfg, []byte("[appearance]\ndockbar_position = \"top\"\n"), 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}
	time.Sleep(3 * time.Second)
	st = b.waitState(func(*wireState) bool { return true }, "a state")
	after, _ := json.Marshal(st.Windows[0])
	if !full(st) {
		t.Fatalf("ASSERTION: after a config reload the pane is not over the whole grid:\nbefore %s\nafter  %s", before, after)
	}
}

// farProxy finds the far side of the link to the remote machine at
// remoteBase: the stdio-proxy whose runtime folder is that machine's.
func farProxy(t *testing.T, remoteBase string) int {
	t.Helper()
	want := "XDG_RUNTIME_DIR=" + xdgDir(remoteBase, "XDG_RUNTIME_DIR")
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid := 0
		if _, err := fmt.Sscan(e.Name(), &pid); err != nil {
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if !bytes.Contains(cmd, []byte("stdio-proxy")) {
			continue
		}
		env, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		for _, kv := range bytes.Split(env, []byte{0}) {
			if string(kv) == want {
				return pid
			}
		}
	}
	t.Fatalf("no stdio-proxy runs for the far machine")
	return 0
}

// A link that stops answering: a probe asked for after typing shows it as
// stalled within seconds, and the moment the far side answers again the
// row is good, with a round trip that does not count the stall.
func TestGUIBridgeHostStallAndRecovery(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	if out, err := tuiosCLI(t, remote, "start-server"); err != nil {
		t.Fatalf("start the far daemon: %v\n%s", err, out)
	}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "│ up ") }, "the link to build comes up")
	b := startBridgeWith(t, base, bridgeOpts{args: []string{"--session", "here"}, env: env, cols: 100, rows: 30, keepDaemon: true, name: "stall"})
	type row struct {
		Health      string `json:"health"`
		RTTMs       int    `json:"rtt_ms"`
		SilentSince int64  `json:"silent_since"`
	}
	build := func() (row, bool) {
		ev := eventsOf[struct {
			Hosts []struct {
				Name string `json:"name"`
				row
			} `json:"hosts"`
		}](b, "hosts", "hosts")
		if len(ev) == 0 {
			return row{}, false
		}
		for _, h := range ev[len(ev)-1].Hosts {
			if h.Name == "build" {
				return h.row, true
			}
		}
		return row{}, false
	}
	waitRow := func(ok func(row) bool, d time.Duration, what string) row {
		deadline := time.Now().Add(d)
		for {
			r, seen := build()
			if seen && ok(r) {
				return r
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s within %v; last row %+v", what, d, r)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitRow(func(r row) bool { return r.Health == "good" && r.RTTMs > 0 }, 30*time.Second, "build good with a round trip")
	// Right after a probe: the next one is about 5 s away.
	time.Sleep(500 * time.Millisecond)

	pid := farProxy(t, remote)
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stop the far proxy: %v", err)
	}
	resumed := false
	defer func() {
		if !resumed {
			_ = syscall.Kill(pid, syscall.SIGCONT)
		}
	}()
	stopped := time.Now()
	b.send(map[string]any{"cmd": "probe-host", "name": "build"})
	r := waitRow(func(r row) bool { return r.Health == "stalled" }, 7500*time.Millisecond, "build stalled after a probe asked for at the stop")
	t.Logf("stalled after %v, silent since %d", time.Since(stopped).Round(100*time.Millisecond), r.SilentSince)
	if r.SilentSince == 0 {
		t.Fatalf("ASSERTION: a stalled row carries no silent_since")
	}

	_ = syscall.Kill(pid, syscall.SIGCONT)
	resumed = true
	back := time.Now()
	r = waitRow(func(r row) bool { return r.Health == "good" }, 2*time.Second, "build good again within 2 s of the far side answering")
	t.Logf("good again after %v, round trip %d ms", time.Since(back).Round(10*time.Millisecond), r.RTTMs)
	if r.RTTMs >= 1000 {
		t.Fatalf("ASSERTION: the round trip counts the stall: %d ms", r.RTTMs)
	}
}

// --scrollback sets how much history each snapshot carries: a renderer that
// keeps 3000 rows gets 3000 on a reopen, not the daemon's default 1000.
func TestGUIBridgeSnapshotCarriesTheRenderersHistory(t *testing.T) {
	base := t.TempDir()
	b := startBridge(t, base, "deep", 100, 30)
	b.mustCall(map[string]any{"cmd": "action", "name": "new_window"})
	st := b.waitState(func(s *wireState) bool { return len(s.Windows) == 1 }, "one pane")
	pty := st.Windows[0].PTY
	b.input(pty, "seq -f 'deep row %g' 1 2500\r")
	b.waitFor(func() bool { return bytes.Contains(b.outputs[pty], []byte("deep row 2500")) }, shellTimeout, "the rows printed")
	_ = b.in.Close()
	b.waitFor(func() bool { return b.closed }, uiTimeout, "the bridge to stop")

	for _, c := range []struct {
		args []string
		deep bool
	}{
		{nil, false},
		{[]string{"--scrollback", "3000"}, true},
	} {
		b := startBridgeWith(t, base, bridgeOpts{args: append([]string{"--session", "deep"}, c.args...), cols: 100, rows: 30, keepDaemon: true, name: fmt.Sprint("deep", len(c.args))})
		b.waitFor(func() bool { return len(b.snapBytes[pty]) > 0 }, uiTimeout, "the pane's snapshot")
		b.mu.Lock()
		snap := b.snapBytes[pty]
		b.mu.Unlock()
		// Row 100 and not row 1000 or 1001: a digit may not follow.
		has := regexp.MustCompile(`deep row 100[^0-9]`).Match(snap)
		first := ""
		if i := bytes.Index(snap, []byte("deep row ")); i >= 0 {
			first = string(snap[i:min(len(snap), i+16)])
		}
		t.Logf("%v: snapshot %d bytes, holds row 100: %v, first %q", c.args, len(snap), has, first)
		if has != c.deep {
			t.Fatalf("ASSERTION: with %v the snapshot holds row 100 of 2500: %v, want %v", c.args, has, c.deep)
		}
		_ = b.in.Close()
		b.waitFor(func() bool { return b.closed }, uiTimeout, "the bridge to stop")
	}
}
