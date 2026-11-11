package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestUserActionsRunFromAKeyAndTheCLI: [[actions]] entries in config.toml run
// from their key in the client and from tuios actions run, on the focused
// pane, and text from the pane reaches a command whole.
//
// It checks: a command action bound to prefix+alt+u writes the focused pane's
// title, which holds $(...) and quotes, as one argument, and nothing in the
// title runs; tuios actions run of a built-in action opens a window; a
// [[keybindings.command]] entry runs as user:NAME; an action with a
// placeholder in a shell's -c script is left out with a warning that says
// why; --dry-run prints the filled arguments; tuios actions list names each
// action and its source; a pane with only the read grant may not run one.
//
// How it could pass wrongly: a shell that read the title would still write a
// file, so the file must hold the title byte for byte and the file the title
// would create must not exist. A run-command that reached nothing would still
// answer, so each run is checked by its effect.
//
// Artifact: the CLI transcript and the file the action wrote.
//
// Negative control: with ExpandRun handing the title to sh -c (the
// placeholder spliced into a script), the marker file appears and the check
// fails.
func TestUserActionsRunFromAKeyAndTheCLI(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	art := artifactDir(t)
	out := filepath.Join(base, "title.out")
	marker := filepath.Join(base, "pwned")
	note := filepath.Join(base, "command.out")
	cfg := `[[actions]]
name = "save-title"
label = "Save the pane title"
key = "prefix+alt+u"
run = ["/bin/sh", "-c", "printf '%s' \"$1\" > ` + out + `", "save-title", "{pane.title}"]
in = "silent"

[[actions]]
name = "more-panes"
label = "Open a window"
action = "new_window"

[[actions]]
name = "unsafe"
run = ["/bin/sh", "-c", "echo {pane.title}"]

[[keybindings.command]]
key = "prefix+alt+n"
name = "note-it"
type = "shell"
command = "echo noted > ` + note + `"

[agents.permissions]
mode = "strict"
grants = ["read"]
`
	writeConfig(t, base, cfg)
	var log strings.Builder
	run := func(args ...string) (string, error) {
		o, err := tuiosCLI(t, base, args...)
		fmt.Fprintf(&log, "$ tuios %s\n%s\n", strings.Join(args, " "), o)
		return o, err
	}
	defer func() { _ = os.WriteFile(filepath.Join(art, "transcript.txt"), []byte(log.String()), 0o644) }()

	mustCLI(t, base, "new", "S", "--detach")
	waitForSessionInfo(t, base, "S")
	term := attachIn(t, base, "S", startOpts{cols: 120, rows: 36})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never started: %v\n%s", err, term.Snapshot())
	}
	title := `it's "$(touch ` + marker + `)" and $HOME`
	mustCLI(t, base, "set-window", "-s", "S", "-w", "0", "--name", title)

	// The list, with the bad action left out, and the command entry as user:.
	listed, _ := run("actions", "list")
	for _, want := range []string{"save-title", "more-panes", "note-it", "command key", "prefix+alt+u"} {
		if !strings.Contains(listed, want) {
			t.Errorf("ASSERTION: tuios actions list lacks %q:\n%s", want, listed)
		}
	}
	if strings.Contains(listed, "unsafe") {
		t.Errorf("ASSERTION: the action with a placeholder in a shell script is listed:\n%s", listed)
	}
	if o, err := run("actions", "list", "unsafe"); err == nil || !strings.Contains(o, "shell script") {
		t.Errorf("ASSERTION: list unsafe does not say why the unsafe action is left out: %v\n%s", err, o)
	}

	// The dry run fills the title into one argument.
	dry, err := run("actions", "run", "save-title", "-s", "S", "--dry-run")
	if err != nil || !strings.Contains(dry, `"it's \"$(touch `) {
		t.Errorf("ASSERTION: the dry run does not show the title as one argument: %v\n%s", err, dry)
	}

	// The key runs the command action.
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), tuitest.Alt('u')); err != nil {
		t.Fatalf("press the key: %v", err)
	}
	got := waitForFileText(t, out, func(s string) bool { return s != "" }, "the key never ran the action")
	_ = os.WriteFile(filepath.Join(art, "title.out"), []byte(got), 0o644)
	if got != title {
		t.Errorf("ASSERTION: the action wrote %q, want the title %q whole", got, title)
	}
	stayTrue(t, "nothing in the title runs", 500*time.Millisecond, func() (bool, string) {
		_, err := os.Stat(marker)
		return os.IsNotExist(err), "the title's $(touch) ran: " + marker + " exists"
	})

	// tuios actions run of a built-in action, and of a command entry.
	if o, err := run("actions", "run", "more-panes", "-s", "S"); err != nil {
		t.Fatalf("ASSERTION: tuios actions run more-panes failed: %v\n%s", err, o)
	}
	waitUntil(t, "the built-in action never opened a window", uiTimeout, func() (bool, string) {
		n := windowCount(t, base, "S")
		return n >= 2, "windows: " + strings.Repeat("|", n)
	})
	if o, err := run("actions", "run", "note-it", "-s", "S"); err != nil {
		t.Fatalf("ASSERTION: tuios actions run of a command entry failed: %v\n%s", err, o)
	}
	waitForFileText(t, note, func(s string) bool { return strings.Contains(s, "noted") }, "the command entry never ran as user:note-it")

	// A pane with only read may not run an action.
	pane := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", "S", "worker", "--no-focus", "--print-id"))
	paneOut := filepath.Join(base, "pane.out")
	mustCLI(t, base, "send-text", "-s", "S", "-w", pane, tuiosBin+" actions run note-it > "+paneOut+".tmp 2>&1; echo EXIT=$? >> "+paneOut+".tmp; mv "+paneOut+".tmp "+paneOut+"\n")
	refused := waitForFileText(t, paneOut, func(s string) bool { return strings.Contains(s, "EXIT=") }, "the pane never ran its call")
	fmt.Fprintf(&log, "pane:\n%s\n", refused)
	if strings.Contains(refused, "EXIT=0") || !strings.Contains(refused, "refused") {
		t.Errorf("ASSERTION: a pane with only read ran an action:\n%s", refused)
	}
}
