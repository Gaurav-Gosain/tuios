package tuie2e

import (
	"os"
	"strings"
	"testing"
)

// TestDisplayFollowReachesAPaneStartedBeforeAnyClient is the case of a daemon
// that a service manager started: no display, and panes that start (or are
// restored) before any client attaches. Such a pane must still reach the
// desktop once a client with a Wayland socket attaches, without a new shell.
//
// It also covers the link flap: a client with an X11 display and no Wayland
// socket attaches after the Wayland client. The link must stay on the Wayland
// socket, and the pane must get both. With
// the X11 client alone the panes get DISPLAY and no WAYLAND_DISPLAY.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The display follow".
func TestDisplayFollowReachesAPaneStartedBeforeAnyClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	noDisplay(t)
	probe := displayProbe(t, base)
	const sess = "e2e-display-early"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	link := readDisplayEnv(t, base, sess).Link
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "early"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	got := runProbe(t, base, sess, "early", probe)
	if got["WL"] != link {
		t.Fatalf("a pane started before any client has WAYLAND_DISPLAY %q, want the link %s", got["WL"], link)
	}
	if strings.HasPrefix(got["WL_GOT"], "TOKEN") {
		t.Fatalf("the link answered before any client attached: %v", got)
	}

	sock := fakeWayland(t, privateDir(t, "wl", 0o700), "wayland-e", "TOKEN-E")
	a := displayClient(t, base, sess, "WAYLAND_DISPLAY="+sock)
	waitLinkTo(t, link, sock, "after the desktop client attached")
	if got := runProbe(t, base, sess, "early", probe); got["WL_GOT"] != "TOKEN-E" {
		t.Fatalf("the pane started before the client did not reach its socket: %v", got)
	}
	if err := a.WaitForText("PROBE WL_GOT=TOKEN-E", uiTimeout); err != nil {
		t.Fatalf("the probe's summary never showed: %v\n%s", err, a.Snapshot())
	}
	saveArtifact(t, a, artifactDir(t), "display-early-pane")

	// An X11 client after the Wayland one: the link stays.
	x11 := fakeX11(t, "TOKEN-X")
	x := displayClient(t, base, sess, "DISPLAY="+x11)
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "early", "true\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	env := readDisplayEnv(t, base, sess).Env
	if env["WAYLAND_DISPLAY"] != link || env["DISPLAY"] != x11 {
		t.Fatalf("with a Wayland and an X11 client the display is %v, want the link and %s", env, x11)
	}
	if target, _ := os.Readlink(link); target != sock {
		t.Fatalf("an X11 client moved the link to %q, want %s", target, sock)
	}

	// The Wayland client leaves: the X11 client alone gives no link.
	if err := a.Close(); err != nil {
		t.Logf("close A: %v", err)
	}
	waitLinkTo(t, link, "", "after the Wayland client left")
	env = readDisplayEnv(t, base, sess).Env
	if _, ok := env["WAYLAND_DISPLAY"]; ok || env["DISPLAY"] != x11 {
		t.Fatalf("with an X11 client alone the display is %v, want DISPLAY %s and no WAYLAND_DISPLAY", env, x11)
	}
	if err := x.Close(); err != nil {
		t.Logf("close X: %v", err)
	}
}
