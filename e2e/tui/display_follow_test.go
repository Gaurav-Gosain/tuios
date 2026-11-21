package tuie2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func fakeWayland(t *testing.T, dir, name, token string) string {
	t.Helper()
	sock := filepath.Join(dir, name)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte(token))
			_ = c.Close()
		}
	}()
	resolved, err := filepath.EvalSymlinks(sock)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func privateDir(t *testing.T, prefix string, mode os.FileMode) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	return dir
}

func fakeX11(t *testing.T, token string) string {
	t.Helper()
	const dir = "/tmp/.X11-unix"
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.Mkdir(dir, 0o1777); err != nil {
			t.Skipf("cannot make %s: %v", dir, err)
		}
		_ = os.Chmod(dir, 0o1777|os.ModeSticky)
		t.Cleanup(func() { _ = os.Remove(dir) })
	}
	for n := 6100 + os.Getpid()%2000; n < 9000; n++ {
		sock := filepath.Join(dir, fmt.Sprintf("X%d", n))
		if _, err := os.Lstat(sock); err == nil {
			continue
		}
		ln, err := net.Listen("unix", sock)
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = ln.Close(); _ = os.Remove(sock) })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte(token))
				_ = c.Close()
			}
		}()
		return fmt.Sprintf(":%d", n)
	}
	t.Skip("no free X display number")
	return ""
}

func displayProbe(t *testing.T, base string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	path := filepath.Join(base, "display-probe.sh")
	script := `#!/bin/sh
out=$1
{
  echo "WL=$WAYLAND_DISPLAY"
  echo "X=$DISPLAY"
  echo "XA=$XAUTHORITY"
  python3 - <<'PY'
import os, socket
def ask(path):
    try:
        s = socket.socket(socket.AF_UNIX)
        s.settimeout(3)
        s.connect(path)
        return s.recv(64).decode()
    except Exception as e:
        return "ERR:" + type(e).__name__
w = os.environ.get("WAYLAND_DISPLAY", "")
print("WL_GOT=" + (ask(w) if w else "none"))
d = os.environ.get("DISPLAY", "")
print("X_GOT=" + (ask("/tmp/.X11-unix/X" + d[1:].split(".")[0]) if d.startswith(":") else "none"))
PY
} > "$out.tmp"
mv "$out.tmp" "$out"
grep -E '^(WL_GOT|X_GOT)=' "$out" | sed 's/^/PROBE /'
echo "PROBE WL=${WAYLAND_DISPLAY##*/}"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

var probeRuns int

func runProbe(t *testing.T, base, session, win, probe string) map[string]string {
	t.Helper()
	probeRuns++
	out := filepath.Join(base, fmt.Sprintf("probe-%d.txt", probeRuns))
	args := []string{"send-text", "-s", session}
	if win != "" {
		args = append(args, "-w", win)
	}
	args = append(args, "clear; sh "+probe+" "+out+"\n")
	if res, err := tuiosCLI(t, base, args...); err != nil {
		t.Fatalf("send-text: %v\n%s", err, res)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if raw, err := os.ReadFile(out); err == nil {
			got := map[string]string{}
			for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
				if k, v, ok := strings.Cut(line, "="); ok {
					got[k] = v
				}
			}
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe in %s never wrote %s", session, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type displayEnvReport struct {
	Follow bool              `json:"follow"`
	Own    bool              `json:"own"`
	Link   string            `json:"link"`
	Target string            `json:"target"`
	Env    map[string]string `json:"env"`
}

func readDisplayEnv(t *testing.T, base, session string) displayEnvReport {
	t.Helper()
	out, err := tuiosCLI(t, base, "display-env", "-s", session, "--json")
	if err != nil {
		t.Fatalf("display-env: %v\n%s", err, out)
	}
	var r displayEnvReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("decode display-env: %v\n%s", err, out)
	}
	return r
}

func displayClient(t *testing.T, base, session string, env ...string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"attach", session}, env: env})
	deadline := time.Now().Add(bootTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-clients", "--json")
		var rows []struct {
			PID     int    `json:"pid"`
			Session string `json:"session"`
		}
		if json.Unmarshal([]byte(out), &rows) == nil && slices.ContainsFunc(rows, func(r struct {
			PID     int    `json:"pid"`
			Session string `json:"session"`
		}) bool {
			return r.PID == term.Pid() && r.Session == session
		}) {
			return term
		}
		if time.Now().After(deadline) {
			t.Fatalf("client with %v never attached %s\n%s", env, session, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the display follow is tested on Linux")
	}
}

func noDisplay(t *testing.T) {
	linuxOnly(t)
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XAUTHORITY", "")
}

// TestDisplayFollowsTheLocalClient starts a daemon with no display, as a host
// link over ssh starts it, with the shipped config (display is not set, so
// it is the default, follow). A desktop client with a Wayland socket, an X11
// display and an X authority file attaches. A new pane gets all three and
// reaches both sockets. A second client with an absolute WAYLAND_DISPLAY
// outside every runtime dir attaches, and the first pane reaches the second
// socket through the same link. Refused values move nothing. Detaches fall
// back and then remove the link.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The display follow".
func TestDisplayFollowsTheLocalClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	noDisplay(t)
	t.Setenv("SSH_AUTH_SOCK", "")
	probe := displayProbe(t, base)
	const sess = "e2e-display"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	r := readDisplayEnv(t, base, sess)
	if !r.Follow || r.Own || len(r.Env) != 1 || r.Env["WAYLAND_DISPLAY"] != r.Link || r.Target != "" {
		t.Fatalf("before any client: display-env = %+v, want follow, no display of its own, WAYLAND_DISPLAY naming the link alone", r)
	}
	link := r.Link

	runtime := xdgDir(base, "XDG_RUNTIME_DIR")
	sockA := fakeWayland(t, runtime, "wayland-e2e", "TOKEN-A")
	x11 := fakeX11(t, "TOKEN-X")
	xauthDir := privateDir(t, "xa", 0o700)
	xauth := filepath.Join(xauthDir, "Xauthority")
	if err := os.WriteFile(xauth, []byte("cookie"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := displayClient(t, base, sess, "WAYLAND_DISPLAY=wayland-e2e", "DISPLAY="+x11, "XAUTHORITY="+xauth)
	waitLinkTo(t, link, sockA, "after the desktop client attached")

	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "first"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	got := runProbe(t, base, sess, "first", probe)
	if got["WL"] != link || got["WL_GOT"] != "TOKEN-A" {
		t.Fatalf("the new pane did not reach the desktop's Wayland socket through %s: %v", link, got)
	}
	if got["X"] != x11 || got["XA"] != xauth || got["X_GOT"] != "TOKEN-X" {
		t.Fatalf("the new pane did not get the desktop's X11 display %s and %s: %v", x11, xauth, got)
	}
	if err := a.WaitForText("PROBE WL_GOT=TOKEN-A", uiTimeout); err != nil {
		t.Fatalf("the probe's summary never showed: %v\n%s", err, a.Snapshot())
	}
	saveArtifact(t, a, artifactDir(t), "display-new-pane")

	out, err := tuiosCLI(t, base, "display-env", "-s", sess)
	if err != nil {
		t.Fatalf("display-env: %v\n%s", err, out)
	}
	want := "export DISPLAY='" + x11 + "'\nexport WAYLAND_DISPLAY='" + link + "'\nexport XAUTHORITY='" + xauth + "'\n"
	if out != want {
		t.Fatalf("display-env printed\n%s\nwant\n%s", out, want)
	}

	sockB := fakeWayland(t, privateDir(t, "wl", 0o700), "wayland-b", "TOKEN-B")
	b := displayClient(t, base, sess, "WAYLAND_DISPLAY="+sockB)
	waitLinkTo(t, link, sockB, "after a client with an absolute WAYLAND_DISPLAY attached")
	if got := runProbe(t, base, sess, "first", probe); got["WL_GOT"] != "TOKEN-B" {
		t.Fatalf("the pane started under A did not reach B's socket through the link: %v", got)
	}

	notSock := filepath.Join(privateDir(t, "nf", 0o700), "wayland-0")
	if err := os.WriteFile(notSock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	openDir := privateDir(t, "wo", 0o700)
	openSock := fakeWayland(t, openDir, "wayland-0", "TOKEN-OPEN")
	if err := os.Chmod(openDir, 0o777); err != nil {
		t.Fatal(err)
	}
	before := readDisplayEnv(t, base, sess)
	for _, bad := range []struct{ name, env string }{
		{"a WAYLAND_DISPLAY that is a file, not a socket", "WAYLAND_DISPLAY=" + notSock},
		{"a socket in a folder anyone can write to", "WAYLAND_DISPLAY=" + openSock},
		{"a relative WAYLAND_DISPLAY with a slash", "WAYLAND_DISPLAY=../wayland-e2e"},
		{"a forwarded X display", "DISPLAY=localhost:10.0"},
		{"an X display with no server", "DISPLAY=:9999"},
	} {
		c := displayClient(t, base, sess, bad.env)
		time.Sleep(300 * time.Millisecond)
		if target, _ := os.Readlink(link); target != sockB {
			t.Fatalf("a client with %s moved the link to %q", bad.name, target)
		}
		if now := readDisplayEnv(t, base, sess); !maps.Equal(now.Env, before.Env) {
			t.Fatalf("a client with %s changed the display to %v", bad.name, now.Env)
		}
		if err := c.Close(); err != nil {
			t.Logf("close the client with %s: %v", bad.name, err)
		}
	}

	const other = "e2e-display-other"
	if out, err := tuiosCLI(t, base, "new", other, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	otherLink := readDisplayEnv(t, base, other).Link
	nested := "WAYLAND_DISPLAY=" + sockA + " " + tuiosBin + " attach " + other + "\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "first", nested); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for !slices.Contains(listClientSessions(t, base), other) {
		if time.Now().After(deadline) {
			t.Fatalf("the client in the pane never attached %s\n%s", other, b.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if target, err := os.Readlink(otherLink); err == nil {
		t.Fatalf("a client inside a pane made the Wayland link of %s point at %s", other, target)
	}
	if env := readDisplayEnv(t, base, other).Env; len(env) != 1 || env["WAYLAND_DISPLAY"] != otherLink {
		t.Fatalf("a client inside a pane gave %s the display %v", other, env)
	}
	outside := displayClient(t, base, other, "WAYLAND_DISPLAY="+sockA)
	waitLinkTo(t, otherLink, sockA, "after a client outside every pane attached "+other)
	if err := outside.Close(); err != nil {
		t.Logf("close the client outside: %v", err)
	}
	waitLinkTo(t, otherLink, "", "after the only client with a display left "+other)

	if err := b.Close(); err != nil {
		t.Logf("close B: %v", err)
	}
	waitLinkTo(t, link, sockA, "after B left")
	if env := readDisplayEnv(t, base, sess).Env; env["DISPLAY"] != x11 {
		t.Fatalf("after B left the display is %v, want A's %s", env, x11)
	}

	if err := a.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach A: %v", err)
	}
	waitExit(t, a, "A's detach")
	waitLinkTo(t, link, "", "after the last client left")
	if env := readDisplayEnv(t, base, sess).Env; len(env) != 1 || env["WAYLAND_DISPLAY"] != link {
		t.Fatalf("with no client left the display is %v, want the link alone", env)
	}
}

// TestDisplayFollowIgnoresAHostLinkClient joins a hub and a remote daemon,
// neither with a display. A client on the hub attaches the far session
// through the link with a valid Wayland socket. Its values name a display on
// the hub's machine, so neither daemon takes them. A client attached to the
// far daemon directly with the same socket does: that is the positive half.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The display follow".
func TestDisplayFollowIgnoresAHostLinkClient(t *testing.T) {
	noDisplay(t)
	base, remote, ssh := hubWithHost(t, "")
	sock := fakeWayland(t, privateDir(t, "wl", 0o700), "wayland-h", "TOKEN-H")

	term := startIn(t, base, startOpts{
		args: []string{"attach", "--host", "build", "far-agent"},
		env:  []string{"WAYLAND_DISPLAY=" + sock, "TUIOS_SSH=" + ssh},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰──") }, bootTimeout); err != nil {
		t.Fatalf("the client never drew the far session: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("echo TYPED-$((6*7))\r"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := term.WaitForText("TYPED-42", uiTimeout); err != nil {
		t.Fatalf("the keys never reached the far pane: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(1500 * time.Millisecond)
	far := readDisplayEnv(t, remote, "far-agent")
	if len(far.Env) != 1 || far.Env["WAYLAND_DISPLAY"] != far.Link || far.Target != "" {
		t.Fatalf("a client over a host link gave the far session the display %+v", far)
	}
	if target, err := os.Readlink(far.Link); err == nil {
		t.Fatalf("a client over a host link made the far link point at %s", target)
	}
	if hub := readDisplayEnv(t, base, "home"); len(hub.Env) != 1 || hub.Env["WAYLAND_DISPLAY"] != hub.Link {
		t.Fatalf("a client over a host link gave the hub's session the display %+v", hub)
	}
	saveArtifact(t, term, artifactDir(t), "display-host-link")

	direct := displayClient(t, remote, "far-agent", "WAYLAND_DISPLAY="+sock)
	waitLinkTo(t, far.Link, sock, "after a local client attached the far session directly")
	if err := direct.Close(); err != nil {
		t.Logf("close the direct client: %v", err)
	}
}

// TestDisplayFollowLeavesADaemonWithADisplayAlone starts the daemon with a
// WAYLAND_DISPLAY of its own, as one started from the desktop has. A client
// with another socket attaches. Nothing follows it, and a new pane sees the
// daemon's own value. With display = "off" a daemon without a display
// follows nothing either.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The display follow".
func TestDisplayFollowLeavesADaemonWithADisplayAlone(t *testing.T) {
	linuxOnly(t)
	sock := fakeWayland(t, privateDir(t, "wl", 0o700), "wayland-c", "TOKEN-C")
	for _, c := range []struct {
		name, own, config string
	}{
		{"own display", "wayland-own", ""},
		{"off", "", "[daemon]\ndisplay = \"off\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			noDisplay(t)
			t.Setenv("WAYLAND_DISPLAY", c.own)
			if c.config != "" {
				writeConfig(t, base, c.config)
			}
			probe := displayProbe(t, base)
			const sess = "e2e-display-own"
			if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
				t.Fatalf("create session: %v: %s", err, out)
			}
			r := readDisplayEnv(t, base, sess)
			if r.Follow || r.Own != (c.own != "") {
				t.Fatalf("display-env = %+v, want no follow and own=%v", r, c.own != "")
			}
			cl := displayClient(t, base, sess, "WAYLAND_DISPLAY="+sock)
			time.Sleep(300 * time.Millisecond)
			if target, err := os.Readlink(r.Link); err == nil {
				t.Fatalf("the daemon followed the client: its link points at %s", target)
			}
			if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "p"); err != nil {
				t.Fatalf("new-window: %v\n%s", err, out)
			}
			if got := runProbe(t, base, sess, "p", probe); got["WL"] != c.own {
				t.Fatalf("a new pane has WAYLAND_DISPLAY %q, want the daemon's own %q", got["WL"], c.own)
			}
			saveArtifact(t, cl, artifactDir(t), "display-"+strings.ReplaceAll(c.name, " ", "-"))
		})
	}
}

// TestDisplayFollowReachesTheDesktopFromANewPane is the person's case with
// nothing but the pane: a daemon with no display, a desktop client with a
// Wayland socket, and a new pane that connects to it through
// WAYLAND_DISPLAY, the way wl-paste does. It calls no tuios command the
// feature added, so a build without the feature fails on the connect itself.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The display follow".
func TestDisplayFollowReachesTheDesktopFromANewPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	noDisplay(t)
	probe := displayProbe(t, base)
	const sess = "e2e-display-pane"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	sock := fakeWayland(t, privateDir(t, "wl", 0o700), "wayland-p", "TOKEN-P")
	term := displayClient(t, base, sess, "WAYLAND_DISPLAY="+sock)
	deadline := time.Now().Add(uiTimeout)
	var got map[string]string
	for i := 0; ; i++ {
		name := fmt.Sprintf("p%d", i)
		if out, err := tuiosCLI(t, base, "new-window", "-s", sess, name); err != nil {
			t.Fatalf("new-window: %v\n%s", err, out)
		}
		got = runProbe(t, base, sess, name, probe)
		if got["WL_GOT"] == "TOKEN-P" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a new pane never reached the desktop client's Wayland socket: %v\n%s", got, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !filepath.IsAbs(got["WL"]) || got["WL"] == sock {
		t.Fatalf("WAYLAND_DISPLAY is %q, want the session's link, not the client's socket %s", got["WL"], sock)
	}
	if err := term.WaitForText("PROBE WL_GOT=TOKEN-P", uiTimeout); err != nil {
		t.Fatalf("the probe's summary never showed: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "display-pane-connect")
}
