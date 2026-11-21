package tuie2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var hostTitlePattern = regexp.MustCompile(`\x1b\]2;([^\x07\x1b]*)(?:\x07|\x1b\\)`)

func hostTitles(wire string) []string {
	var titles []string
	for _, m := range hostTitlePattern.FindAllStringSubmatch(wire, -1) {
		titles = append(titles, m[1])
	}
	return titles
}

func lastHostTitle(wire *syncBuffer) (string, []string) {
	titles := hostTitles(wire.String())
	if len(titles) == 0 {
		return "", nil
	}
	return titles[len(titles)-1], titles
}

func saveHostTitles(t *testing.T, wire *syncBuffer) {
	t.Helper()
	saveText(t, "osc2", strings.Join(hostTitles(wire.String()), "\n")+"\n")
}

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

func waitForHostTitle(t *testing.T, wire *syncBuffer, want, when string) {
	t.Helper()
	waitHostTitle(t, wire, when, "became", want, func(last string) bool { return last == want })
}

func waitForHostTitleEnding(t *testing.T, wire *syncBuffer, want, when string) {
	t.Helper()
	waitHostTitle(t, wire, when, "ended with", want, func(last string) bool { return strings.HasSuffix(last, want) })
}

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
	first := firstWindow(t, base, "home").ID
	cli("new-window", "second", "-s", "home", "--no-focus")

	var wire syncBuffer
	term := attachIn(t, base, "home", startOpts{out: &wire})
	t.Cleanup(func() {
		saveArtifact(t, term, artifactDir(t), "host-title")
		saveHostTitles(t, &wire)
	})

	cli("set-config", "appearance.host_title", "{title} · {session}")
	waitForHostTitle(t, &wire, "home", "after attach")

	cli("focus-window", "second", "-s", "home")
	waitForHostTitle(t, &wire, "second · home", "focus on the named pane")

	cli("send-text", "-s", "home", "-w", first, "printf '\\033]2;build\\007'\n")
	cli("focus-window", first, "-s", "home")
	waitForHostTitle(t, &wire, "build · home", "focus on the pane that titled itself")

	cli("send-text", "-s", "home", "-w", first, "printf '\\033]2;deploy\\007'\n")
	waitForHostTitle(t, &wire, "deploy · home", "the focused pane retitled")

	cli("send-text", "-s", "home", "-w", first, "\"$HERDR_BIN_PATH\" terminal title set 'from a tool'\n")
	waitForHostTitle(t, &wire, "from a tool", "a tool set the title")
	cli("send-text", "-s", "home", "-w", first, "printf '\\033]2;ignored\\007'\n")
	holdHostTitle(t, &wire, "from a tool", time.Second, "the pane retitled under a tool's title")
	cli("send-text", "-s", "home", "-w", first, "\"$HERDR_BIN_PATH\" terminal title clear\n")
	waitForHostTitle(t, &wire, "ignored · home", "the tool's title cleared")

	cli("set-window", "-s", "home", "-w", first, "--name", "a\x1b]2;PWN\x07b")
	waitForHostTitle(t, &wire, "a]2;PWNb · home", "a pane name holding an OSC 2")

	cli("set-config", "appearance.host_title", "{cwd}")
	waitForHostTitleEnding(t, &wire, "/cwd", "the working directory before the cd")
	if err := os.Mkdir(filepath.Join(workDirIn(t, base), "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli("send-text", "-s", "home", "-w", first, "cd plain\n")
	waitForHostTitleEnding(t, &wire, "/cwd/plain", "a plain cd")
	cli("send-text", "-s", "home", "-w", first, "d=\"$(printf 'd\\033]2;PWN2\\007x')\"; mkdir -p \"$d\" && cd \"$d\"\n")
	waitForHostTitleEnding(t, &wire, "/d]2;PWN2x", "a working directory holding an OSC 2")

	cli("set-config", "appearance.host_title", "{index}")
	waitForHostTitle(t, &wire, "1", "the template changed live")

	cli("set-session-name", "demo", "-s", "home")
	cli("set-config", "appearance.host_title", "{session}:{workspace}")
	waitForHostTitle(t, &wire, "demo:1", "a session with a display name")

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
