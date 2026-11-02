package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// hostTitlePattern matches an OSC 2 the client writes to the host terminal,
// ended by BEL or by ST.
var hostTitlePattern = regexp.MustCompile(`\x1b\]2;([^\x07\x1b]*)(?:\x07|\x1b\\)`)

// hostTitles returns every OSC 2 payload in what the client wrote, in order.
func hostTitles(wire string) []string {
	var titles []string
	for _, m := range hostTitlePattern.FindAllStringSubmatch(wire, -1) {
		titles = append(titles, m[1])
	}
	return titles
}

// lastHostTitle is the title the client wrote last, and every title it wrote.
func lastHostTitle(wire *syncBuffer) (string, []string) {
	titles := hostTitles(wire.String())
	if len(titles) == 0 {
		return "", nil
	}
	return titles[len(titles)-1], titles
}

// saveHostTitles keeps every title the client wrote, one per line, as
// osc2.txt in the test's artifact directory.
func saveHostTitles(t *testing.T, wire *syncBuffer) {
	t.Helper()
	saveText(t, "osc2", strings.Join(hostTitles(wire.String()), "\n")+"\n")
}

// waitHostTitle waits until ok holds for the last title the client wrote. On a
// timeout it fails with "the host title never <verb> <want>". It fails at once
// if the OSC 2 a test planted in a pane name or a directory reached the host
// as one. An ESC let through would also end the title's own OSC 2 early, so
// the title read back would come out cut rather than whole.
func waitHostTitle(t *testing.T, wire *syncBuffer, when, verb, want string, ok func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		if strings.Contains(wire.String(), "\x1b]2;PWN") {
			t.Fatalf("%s: the OSC 2 inside the title reached the host", when)
		}
		last, titles := lastHostTitle(wire)
		if len(titles) > 0 && ok(last) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the host title never %s %q; titles written: %q", when, verb, want, titles)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForHostTitle waits until the last title the client wrote is want.
func waitForHostTitle(t *testing.T, wire *syncBuffer, want, when string) {
	t.Helper()
	waitHostTitle(t, wire, when, "became", want, func(last string) bool { return last == want })
}

// waitForHostTitleEnding waits until the last title the client wrote ends
// with want, for a title that starts with a path the test cannot predict.
func waitForHostTitleEnding(t *testing.T, wire *syncBuffer, want, when string) {
	t.Helper()
	waitHostTitle(t, wire, when, "ended with", want, func(last string) bool { return strings.HasSuffix(last, want) })
}

// holdHostTitle fails if, at any point over d, the last title the client
// wrote is not want.
func holdHostTitle(t *testing.T, wire *syncBuffer, want string, d time.Duration, when string) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if last, titles := lastHostTitle(wire); len(titles) == 0 || last != want {
			t.Fatalf("%s: the host title is not %q; titles written: %q", when, want, titles)
		}
	}
}

// TestHostTitleFollowsFocusedPane holds appearance.host_title (issue #1064):
// the client keeps the title of the terminal it runs in on the focused pane,
// as the focus moves, as the focused pane retitles itself, when the template
// changes live, and with the session's name when no pane is focused. A title
// a tool sets through herdr's terminal title set wins until it is cleared. A
// pane name or a working directory holding an OSC 2 reaches the host without
// it.
//
// The artifact is osc2.txt in the test's artifact directory: every title the
// client wrote, one per line, in order. The negative controls are in
// NEGATIVE_CONTROLS.md.
func TestHostTitleFollowsFocusedPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	cli := func(args ...string) {
		t.Helper()
		mustCLI(t, base, args...)
	}
	cli("new", "home", "--detach")
	// The pane tuios new opened, before the second one is there.
	first := firstWindow(t, base, "home").ID
	cli("new-window", "second", "-s", "home", "--no-focus")

	var wire syncBuffer
	term := attachIn(t, base, "home", startOpts{out: &wire})
	t.Cleanup(func() {
		saveArtifact(t, term, artifactDir(t), "host-title")
		saveHostTitles(t, &wire)
	})

	// Set while attached: with no client, the daemon only records the value.
	// The first pane is a shell with no title of its own, so the title falls
	// back to the session's name.
	cli("set-config", "appearance.host_title", "{title} · {session}")
	waitForHostTitle(t, &wire, "home", "after attach")

	cli("focus-window", "second", "-s", "home")
	waitForHostTitle(t, &wire, "second · home", "focus on the named pane")

	cli("send-text", "-s", "home", "-w", first, "printf '\\033]2;build\\007'\n")
	cli("focus-window", first, "-s", "home")
	waitForHostTitle(t, &wire, "build · home", "focus on the pane that titled itself")

	// The focused pane retitles itself while the focus stays put.
	cli("send-text", "-s", "home", "-w", first, "printf '\\033]2;deploy\\007'\n")
	waitForHostTitle(t, &wire, "deploy · home", "the focused pane retitled")

	// A title a tool sets through herdr's terminal title set wins while it is
	// set, and clearing it gives the title back to the focused pane.
	cli("send-text", "-s", "home", "-w", first, "\"$HERDR_BIN_PATH\" terminal title set 'from a tool'\n")
	waitForHostTitle(t, &wire, "from a tool", "a tool set the title")
	cli("send-text", "-s", "home", "-w", first, "printf '\\033]2;ignored\\007'\n")
	holdHostTitle(t, &wire, "from a tool", time.Second, "the pane retitled under a tool's title")
	cli("send-text", "-s", "home", "-w", first, "\"$HERDR_BIN_PATH\" terminal title clear\n")
	waitForHostTitle(t, &wire, "ignored · home", "the tool's title cleared")

	// A pane name is anyone's text. The escape sequence in it must not reach
	// the host, where it would be read as a command.
	cli("set-window", "-s", "home", "-w", first, "--name", "a\x1b]2;PWN\x07b")
	waitForHostTitle(t, &wire, "a]2;PWNb · home", "a pane name holding an OSC 2")

	// So is a directory name, and {cwd} comes from the shell, not from
	// anything tuios cleans first: a cloned repository can hold this. The
	// template is set before the cd, as a person sets it, so the title has to
	// follow the cd itself rather than a render the set-config caused.
	cli("set-config", "appearance.host_title", "{cwd}")
	waitForHostTitleEnding(t, &wire, "/cwd", "the working directory before the cd")
	// A plain cd into a folder that is already there. cd is a builtin, so the
	// prompt the shell draws right after it is the only redraw.
	if err := os.Mkdir(filepath.Join(workDirIn(t, base), "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli("send-text", "-s", "home", "-w", first, "cd plain\n")
	waitForHostTitleEnding(t, &wire, "/cwd/plain", "a plain cd")
	cli("send-text", "-s", "home", "-w", first, "d=\"$(printf 'd\\033]2;PWN2\\007x')\"; mkdir -p \"$d\" && cd \"$d\"\n")
	waitForHostTitleEnding(t, &wire, "/d]2;PWN2x", "a working directory holding an OSC 2")

	cli("set-config", "appearance.host_title", "{index}")
	waitForHostTitle(t, &wire, "1", "the template changed live")

	// {session} is the name the rail shows, which a display name replaces.
	cli("set-session-name", "demo", "-s", "home")
	cli("set-config", "appearance.host_title", "{session}:{workspace}")
	waitForHostTitle(t, &wire, "demo:1", "a session with a display name")

	// A scratch group is drawn over the workspace in view, and the title
	// names that workspace, as the dock does, not the group's own number.
	toggleScratch(t, term)
	if err := term.WaitForText("scratch", uiTimeout); err != nil {
		t.Fatalf("the scratch group never showed: %v\n%s", err, term.Snapshot())
	}
	holdHostTitle(t, &wire, "demo:1", time.Second, "the scratch group shown")
	toggleScratch(t, term)

	cli("select-workspace", "2", "-s", "home")
	waitForHostTitle(t, &wire, "demo", "an empty workspace")
}

// TestHostTitleWithoutADaemon holds appearance.host_title in the standalone
// TUI, which has no session: the title must not be left with a separator
// hanging off the end, nor cleared, for want of a session's name.
func TestHostTitleWithoutADaemon(t *testing.T) {
	base := t.TempDir()
	writeConfigPart(t, base, "config.d/host-title.toml", "[appearance]\nhost_title = \"{title} · {session}\"\n")

	var wire syncBuffer
	term := startIn(t, base, startOpts{out: &wire})
	t.Cleanup(func() { saveHostTitles(t, &wire) })
	waitBoot(t, term)
	newWindow(t, term)
	waitForHostTitle(t, &wire, "tuios", "a fresh shell with no session")

	enterTerminalMode(t, term)
	runInShell(t, term, "printf '\\033]2;build\\007'; echo TI''TLED", "TITLED", shellTimeout)
	waitForHostTitle(t, &wire, "build · tuios", "the pane titled itself")
}
