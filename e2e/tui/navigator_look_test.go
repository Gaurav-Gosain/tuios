package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The navigator's look: framed boxes, a tree drawn with guides, a selected
// row with a bar, and a preview that shows a pane's screen in its own
// colours, without letting anything but those colours through.
//
// How these could pass wrongly, written down first:
//   - A coloured preview could be the coloured row of the list, or the
//     search line, and not the preview. Each colour check reads only the
//     cells right of the list box's right border.
//   - The red could come from the theme or the chrome and not from the pane.
//     No theme is on, so the pane's red reaches the host as SGR 31, index 1,
//     and the chrome is drawn in RGB or in other slots.
//   - The current session's preview could be read from the load and the
//     other session's from the client, so one path is never run. The test
//     reads one pane of each.
//   - A link or a clipboard write could reach the host on a frame the test
//     did not look at. The host copy holds every byte tuios wrote, from the
//     start, and it is read after the preview is on screen.
//   - The guides could be drawn on rows of the preview and not the list. The
//     guide check reads the list box's columns only.
//
// Negative controls, all confirmed red (see NEGATIVE_CONTROLS.md).

// navLookScene builds the sessions TestNavigatorLooks draws: home, which the
// client attaches to, with a coloured prompt and a coloured listing; work,
// with a test run, an agent and a coloured listing on two workspaces; and
// far-shell on the machine build, with a meter. The [hosts] table and the
// theme, if one is given, are written before the daemon starts, since a
// change to [hosts] after it waits for a person to apply it. It returns the
// isolation root and the environment a client needs to reach build.
func navLookScene(t *testing.T, themeName string) (base string, env []string) {
	t.Helper()
	base = t.TempDir()
	killDaemon(t, base)
	remote := remoteMachine(t)
	env = []string{"TUIOS_SSH=" + writeFakeSSHTo(t, base, remote)}
	writeOneHostConfig(t, base, tuiosBin)
	if themeName != "" {
		path := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append([]byte("[appearance]\ntheme = \""+themeName+"\"\n\n"), data...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"new", "home", "--detach"},
		{"new", "work", "--detach"},
		{"new-window", "logs", "-s", "work", "--no-focus"},
	} {
		if o, err := tuiosCLIEnv(t, base, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	printMarker(t, base, "work", "logs", navPrintMarker, navMarker)
	files := filepath.Join(base, "files")
	for _, d := range []string{"cmd", "docs", "internal"} {
		if err := os.MkdirAll(filepath.Join(files, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"go.mod", "README.md", "main.go"} {
		if err := os.WriteFile(filepath.Join(files, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(files, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"new-window", "build", "-s", "work", "--no-focus"},
		{"new-window", "claude", "-s", "work", "--workspace", "2", "--no-focus"},
		{"new-window", "editor", "-s", "work", "--workspace", "2", "--no-focus"},
		{"set-workspace-name", "2", "agents", "-s", "work"},
	} {
		if o, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	for _, args := range [][]string{
		{"new", "far-shell", "--detach"},
		{"new-window", "farlog", "-s", "far-shell", "--no-focus"},
	} {
		if o, err := tuiosCLI(t, remote, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, o)
		}
	}
	home := focusedIn(t, base, "home")
	runShown(t, base, "home", home,
		`PS1="$(printf '\033[1;35mhome\033[0m \033[36m~/dev\033[0m \033[33m$\033[0m ')"; clear; ls --color=always -F `+files+`; printf '\033[1;32m%s\033[0m\n' GRN-5523`,
		"GRN-5523")
	runShown(t, base, "work", "build",
		`clear; printf '\033[1;32mok  \033[0m tuios/internal/app      \033[2m1.42s\033[0m\n\033[1;32mok  \033[0m tuios/internal/vt       \033[2m0.88s\033[0m\n\033[1;31mFAIL\033[0m tuios/internal/session  \033[2m3.10s\033[0m\n\033[33m--- FAIL: TestAttach (0.31s)\033[0m\n\033[31mRED-4417\033[0m\n'`,
		"RED-4417")
	runShown(t, base, "work", "claude",
		`clear; printf '\033[38;5;208m*\033[0m \033[1mClaude\033[0m is reading \033[4mrender_navigator.go\033[0m\n  \033[38;2;120;200;255m- Read 348 lines\033[0m\n  \033[38;2;160;230;140m- Edited 12 lines\033[0m\n\033[2m  esc to interrupt\033[0m\n'`,
		"esc to interrupt")
	runShown(t, base, "work", "editor",
		`clear; ls --color=always -F `+files+`; printf '\033[31m-\told := strip(text)\033[0m\n\033[32m+\tnew := cells(text)\033[0m\n'`,
		"cells(text)")
	runShown(t, remote, "far-shell", "farlog",
		`clear; printf 'cpu \033[32m||||||||||\033[33m||||\033[31m||\033[0m 63%%\nmem \033[32m||||||\033[0m 31%%\nfarmark-5521\n'`,
		"farmark-5521")
	if o, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "work", "-w", "claude"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, o)
	}
	return base, env
}

// runShown types command into a pane and waits for its screen to hold want.
func runShown(t *testing.T, base, sess, window, command, want string) {
	t.Helper()
	if o, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", window, command); err != nil {
		t.Fatalf("send-text: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "send-keys", "-s", sess, "-w", window, "Enter"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, o)
	}
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", window)
		for _, l := range strings.Split(out, "\n") {
			if strings.TrimSpace(l) == want || strings.HasSuffix(strings.TrimSpace(l), want) && !strings.Contains(l, "printf") {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s in %s never printed %s", window, sess, want)
}

// navLookClient attaches a client to home.
func navLookClient(t *testing.T, base string, env []string, o startOpts) *tuitest.Terminal {
	t.Helper()
	o.env = append(o.env, env...)
	if o.cols == 0 {
		o.cols, o.rows = 160, 44
	}
	term := attachIn(t, base, "home", o)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")
	return term
}

// navLookOpen opens the navigator once it lists the session on build. The
// client learns of the other machine's sessions a moment after the link is
// up, and the navigator lists what the client holds when it opens.
func navLookOpen(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		openNavigator(t, term, "home", "work")
		if term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "far-shell") }, 2*time.Second) == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the navigator never listed far-shell\n%s", term.Snapshot())
		}
		sendKeys(t, term, tuitest.Esc)
		time.Sleep(500 * time.Millisecond)
	}
}

// navLookWait waits for every marker, then for the screen to settle.
func navLookWait(t *testing.T, term *tuitest.Terminal, what string, markers ...string) {
	t.Helper()
	waitScreen(t, term, what, markers...)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
}

// TestNavigatorLooks saves the navigator as text, styled text and a PNG on
// the dark and the light look at every colour depth, for a person to look
// at: the tree on another session's pane, an agent's pane, a search, the
// flat layout and the cards. TUIOS_E2E_QA adds catppuccin_mocha.
func TestNavigatorLooks(t *testing.T) {
	looks := []chromeLook{lookDark, lookLatte}
	if os.Getenv("TUIOS_E2E_QA") != "" {
		looks = append(looks, chromeLook{name: "mocha", theme: "catppuccin_mocha"})
	}
	for _, look := range looks {
		for _, depth := range chromeDepths {
			t.Run(look.name+"-"+depth.name, func(t *testing.T) {
				base, env := navLookScene(t, look.theme)
				term := navLookClient(t, base, env, startOpts{shippedLooks: true, env: depth.env})
				dir := artifactDir(t)
				_ = os.WriteFile(filepath.Join(dir, "README"), []byte("choose_tree on three sessions, one on another machine: the tree on a test run, an agent, a search, the flat list and the cards\n"), 0o644)
				pal := hostPalette(t, look.theme)
				shoot := func(name string) {
					saveArtifact(t, term, dir, name)
					savePNG(t, term.Screen(), pal, dir, name)
				}

				navLookOpen(t, term)
				// Open far-shell, the last row, so its panes are listed, then
				// open work above it and go down to build.
				sendKeys(t, term, "G", "l")
				navLookWait(t, term, "far-shell did not open", "farlog")
				sendKeys(t, term, "k", "l")
				navLookWait(t, term, "work did not open", "build", "editor")
				sendKeys(t, term, "j", "j", "j", "j")
				navLookWait(t, term, "the preview is not on build", "TestAttach", "RED-4417")
				shoot("tree")

				sendKeys(t, term, "j", "j")
				navLookWait(t, term, "the preview is not on claude", "esc to interrupt")
				shoot("agent")

				sendKeys(t, term, "/")
				if err := term.SendKeys("testattach"); err != nil {
					t.Fatalf("type the query: %v", err)
				}
				navLookWait(t, term, "the search found nothing", "TestAttach (0.31s)", "1/")
				shoot("search")

				sendKeys(t, term, tuitest.Esc, tuitest.Esc, "v")
				navLookWait(t, term, "the flat layout never showed", "flat", "GRN-5523")
				shoot("flat")

				sendKeys(t, term, "v")
				navLookWait(t, term, "the cards never showed", "cards")
				shoot("cards")
			})
		}
	}
}
