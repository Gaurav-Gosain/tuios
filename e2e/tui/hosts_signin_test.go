package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A host behind Tailscale SSH in check mode, on the rail. The link waits for
// the person to sign in in a browser. The rail says "sign in" beside the host,
// a hover says why, and a click opens the sign-in page with the link opener.
// When the person signs in, the link goes on and the host comes up with no
// other step.
//
// The ssh is the stand-in of the rail tests (writeFakeSSH) with the Tailscale
// wrapper of hosts_tailscale_test.go in front of it. The link opener is a
// script that appends each address it gets to a file, as in
// link_open_test.go. Nothing reaches a real machine or a real browser.

// signInSetup starts tuios with the rail on, the recording opener, and the
// [hosts] given as TOML. It returns the terminal, the base folder, the
// opener's record, the wrapper's state and the wrapper's env.
func signInSetup(t *testing.T, name, hosts string) (*tuitest.Terminal, string, string, tailscaleGate, []string) {
	t.Helper()
	base := t.TempDir()
	inner := writeFakeSSH(t, base)
	wrapper, g := writeTailscaleSSH(t, base, inner)
	record := filepath.Join(base, "opened.txt")
	opener := filepath.Join(base, "opener.sh")
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> '"+record+"'\n"), 0o755); err != nil { //nolint:gosec // the test's own opener
		t.Fatal(err)
	}
	writeConfig(t, base, fmt.Sprintf("[appearance]\nlink_opener = %q\n\n%s", opener, hosts))
	// No ssh in the environment, so the opener runs for the right reason
	// and not as a fallback.
	env := []string{"TUIOS_SSH=" + wrapper, "SSH_CONNECTION=", "SSH_CLIENT=", "SSH_TTY="}
	term := startIn(t, base, startOpts{args: []string{"new", name}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	return term, base, record, g, env
}

// gatedHostTOML is one [hosts] entry for an address the wrapper gates.
func gatedHostTOML(name, addr string) string {
	return "[hosts." + name + "]\n" +
		"addr = \"" + addr + "\"\n" +
		"command = \"" + tuiosBin + "\"\n" +
		"connect_timeout = 5\n\n"
}

// waitRailHeader waits until the rail row of a host header holds want.
func waitRailHeader(t *testing.T, term *tuitest.Terminal, host, want, why string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, hostOpen+" "+host)
		return r >= 0 && strings.Contains(railLine(s, r), want)
	}, 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: %s: %v\n%s", why, err, term.Snapshot())
	}
}

// lastURL is the last sign-in page the wrapper gave out.
func lastURL(t *testing.T, g tailscaleGate) string {
	t.Helper()
	urls := g.urls(t)
	if len(urls) == 0 {
		t.Fatalf("the wrapper gave out no sign-in page")
	}
	return urls[len(urls)-1]
}

// TestRailOpensTheTailscaleSignIn: the rail says "sign in" and not
// "approve", a hover says why, a click opens the page the link waits on, the
// CLI says and does the same, and signing in brings the host up with no new
// dial.
func TestRailOpensTheTailscaleSignIn(t *testing.T) {
	term, base, record, g, env := signInSetup(t, "signin-e2e",
		gatedHostTOML("gated", "someone@gatedbox")+gatedHostTOML("build", "someone@buildbox"))

	waitRailHeader(t, term, "gated", "sign in", "the rail never said sign in beside the gated host")
	s := term.Screen()
	if line := railLine(s, railRowOf(s, hostOpen+" gated")); strings.Contains(line, "approve") {
		t.Errorf("ASSERTION: the header still says approve: %q", line)
	}
	saveFrame(t, term, "rail-host-sign-in")

	// The hover says why the host waits and what a click does.
	col, row := 2, railRowOf(term.Screen(), hostOpen+" gated")
	mouseHover(t, term, col+3, row)
	if err := term.WaitForText("Tailscale SSH needs you to sign in", uiTimeout); err != nil {
		t.Errorf("ASSERTION: the hover does not say why the host waits: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-host-sign-in-hover")

	// A click opens the page the link waits on.
	url := lastURL(t, g)
	mouseClick(t, term, col+3, row, tuitest.MouseLeft, 0)
	waitOpened(t, record, []string{url}, "a click on the header of a host that waits for a sign-in")
	if err := term.WaitForText("Opening the Tailscale sign-in page", uiTimeout); err != nil {
		t.Errorf("ASSERTION: the click did not say what it did: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-host-sign-in-opened")
	// The click opened the page. It did not fold the group.
	if railRowOf(term.Screen(), hostShut+" gated") >= 0 {
		t.Errorf("ASSERTION: the click folded the group instead of only opening the page:\n%s", term.Snapshot())
	}

	// The CLI: the table says sign in and how to open it, the JSON keeps
	// tailscale_check, and hosts signin opens the same page.
	out, _ := tuiosCLIEnv(t, base, env, "hosts")
	saveSyncArtifact(t, "signin-hosts.txt", out)
	if !strings.Contains(out, "sign in") || strings.Contains(out, "│ tailscale_check") ||
		!strings.Contains(out, "Run 'tuios hosts signin gated' to open the sign-in page.") {
		t.Errorf("ASSERTION: tuios hosts does not say sign in and how:\n%s", out)
	}
	out, _ = tuiosCLIEnv(t, base, env, "hosts", "--json")
	var rep struct {
		Hosts []struct {
			Host        string `json:"host"`
			Status      string `json:"status"`
			ApprovalURL string `json:"approval_url"`
		} `json:"hosts"`
	}
	if start := strings.Index(out, "{"); start < 0 || json.Unmarshal([]byte(out[start:]), &rep) != nil {
		t.Fatalf("tuios hosts --json printed no JSON:\n%s", out)
	}
	for _, h := range rep.Hosts {
		if h.Host == "gated" && (h.Status != "tailscale_check" || h.ApprovalURL != url) {
			t.Errorf("ASSERTION: the JSON of gated is %q with %q, want tailscale_check with %s", h.Status, h.ApprovalURL, url)
		}
	}
	out, err := tuiosCLIEnv(t, base, env, "hosts", "signin", "--print")
	saveSyncArtifact(t, "signin-print.txt", out)
	if err != nil || !strings.Contains(out, "gated: open "+url+" to sign in.") || strings.Contains(out, "build") {
		t.Errorf("ASSERTION: hosts signin --print does not print the page of gated alone: %v\n%s", err, out)
	}
	out, err = tuiosCLIEnv(t, base, env, "hosts", "signin", "gated")
	saveSyncArtifact(t, "signin-open.txt", out)
	if err != nil || !strings.Contains(out, "gated: opened the sign-in page "+url) {
		t.Errorf("ASSERTION: hosts signin gated did not open the page: %v\n%s", err, out)
	}
	waitOpened(t, record, []string{url, url}, "tuios hosts signin gated")

	// The person signs in. The held dial goes on, and the host comes up.
	before := len(g.urls(t))
	g.approve(t, url)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, hostOpen+" gated")
		return r >= 0 && !strings.Contains(railLine(s, r), "sign in")
	}, 30*time.Second); err != nil {
		t.Fatalf("ASSERTION: the host did not come up after the sign-in: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-host-signed-in")
	if n := len(g.urls(t)); n != before {
		t.Errorf("ASSERTION: the link dialed again for the sign-in: %d pages, want %d", n, before)
	}
	alive(t, term, "after the sign-in")
}

// TestRailSignInAsksForANewPage: Tailscale ended the wait, so the link has no
// page. A click or Enter on the header asks the daemon to dial again at once,
// opens the new page as soon as the link reports it, and the host comes up
// once the person signs in, with nothing more to do.
func TestRailSignInAsksForANewPage(t *testing.T) {
	t.Run("click", func(t *testing.T) { signInNewPage(t, false) })
	t.Run("enter", func(t *testing.T) { signInNewPage(t, true) })
}

func signInNewPage(t *testing.T, keyboard bool) {
	way := "click"
	if keyboard {
		way = "enter"
	}
	term, base, record, g, env := signInSetup(t, "signin-new-"+way, gatedHostTOML("quick", "someone@quickbox"))

	waitRailHeader(t, term, "quick", "sign in", "the rail never said sign in beside the quick host")

	// Wait for the third dial to end. The link's backoff is then 8 seconds,
	// so a page that shows up much sooner came from the gesture. A dial that
	// has just started has given out its page and has not reported it yet,
	// so "no page" is read twice, a second apart, with no new page between.
	deadline := time.Now().Add(90 * time.Second)
	for {
		n := len(g.urls(t))
		if n >= 3 && signInURLOf(t, base, env, "quick") == "" {
			time.Sleep(time.Second)
			if len(g.urls(t)) == n && signInURLOf(t, base, env, "quick") == "" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the link never ended its third dial: %d pages", len(g.urls(t)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	before := len(g.urls(t))
	var asked time.Time
	if keyboard {
		// s gives the rail the keyboard with the cursor on the attached
		// session. j moves it to the header of quick, the next row.
		if err := term.SendKeys("s"); err != nil {
			t.Fatalf("enter rail: %v", err)
		}
		if err := term.WaitForText(railPill, uiTimeout); err != nil {
			t.Fatalf("s did not give the keyboard to the rail: %v\n%s", err, term.Snapshot())
		}
		if err := term.SendKeys("j"); err != nil {
			t.Fatalf("move cursor: %v", err)
		}
		time.Sleep(insertGuard)
		asked = time.Now()
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatalf("activate the header: %v", err)
		}
	} else {
		row := railRowOf(term.Screen(), hostOpen+" quick")
		asked = time.Now()
		mouseClick(t, term, 5, row, tuitest.MouseLeft, 0)
	}
	if err := term.WaitForText("Opening the Tailscale sign-in page", uiTimeout); err != nil {
		t.Errorf("ASSERTION: the %s did not say what it does: %v\n%s", way, err, term.Snapshot())
	}
	saveFrame(t, term, "rail-host-sign-in-asking-"+way)

	for len(g.urls(t)) == before {
		if time.Since(asked) > 20*time.Second {
			t.Fatalf("ASSERTION: the link never dialed again")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if took := time.Since(asked); took > 5*time.Second {
		t.Errorf("ASSERTION: the new page took %v; the %s must make the link dial at once", took, way)
	}
	url := lastURL(t, g)
	waitOpened(t, record, []string{url}, "the page the "+way+" asked for")

	g.signIn(t)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, hostOpen+" quick")
		return r >= 0 && !strings.Contains(railLine(s, r), "sign in")
	}, 30*time.Second); err != nil {
		t.Fatalf("ASSERTION: the host did not come up after the sign-in: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "rail-host-signed-in-new-page-"+way)
	alive(t, term, "after the sign-in on a new page")
}

// signInURLOf is the sign-in page the daemon reports for host, or "".
func signInURLOf(t *testing.T, base string, env []string, host string) string {
	t.Helper()
	out, _ := tuiosCLIEnv(t, base, env, "hosts", "--json")
	var rep struct {
		Hosts []struct {
			Host        string `json:"host"`
			ApprovalURL string `json:"approval_url"`
		} `json:"hosts"`
	}
	start := strings.Index(out, "{")
	if start < 0 || json.Unmarshal([]byte(out[start:]), &rep) != nil {
		return ""
	}
	for _, h := range rep.Hosts {
		if h.Host == host {
			return h.ApprovalURL
		}
	}
	return ""
}
