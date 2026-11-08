package tuie2e

import (
	"strings"
	"testing"
)

// TestCapturePaneTakesTheWindowAsAnArgument reads a pane that is not the
// focused one by naming it as a plain argument, `tuios capture-pane -s S ID`,
// the way an agent reads a worker. capture-pane had no argument rule, so it
// dropped the id in silence and captured the focused pane: a lead read one
// worker's screen as another's and nothing said so.
//
// It also runs verbs that read no argument with one, and asks for an error
// that names the argument and, for a verb that targets a window, says to use
// -w. Before, each of them did its default work and exited 0.
//
// How this could pass wrongly, written down first:
//   - The named pane could be the focused one, and then the old silent drop
//     reads the right pane by chance. The panes open with --no-focus, so the
//     session's first pane keeps the focus, and the test checks that.
//   - The two panes could print the same text. Each prints its own marker,
//     and the capture must hold one and not the other.
//   - The id could only work in full. The capture is taken by the full id and
//     by the eight characters the CLI prints elsewhere.
//
// Negative control: on origin/main, capture-pane -s S ID prints the focused
// pane, and screenshot, list-windows and kill-server take an argument and
// exit 0, so every subtest fails.
func TestCapturePaneTakesTheWindowAsAnArgument(t *testing.T) {
	const sess = "e2e-plainargs"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	focused := focusedWindowID(t, base, sess)

	var ids []string
	for _, name := range []string{"worker-a", "worker-b"} {
		out, err := tuiosCLI(t, base, "new-window", "-s", sess, "--no-focus", "--print-id", name, "--",
			"/bin/sh", "-c", "printf 'MARK-"+name+"\\n'; exec cat >/dev/null")
		if err != nil {
			t.Fatalf("new-window %s: %v\n%s", name, err, out)
		}
		ids = append(ids, strings.TrimSpace(out))
		if out, err := tuiosCLI(t, base, "wait-for", "window-output", "-s", sess, "-w", name,
			"--pattern", "MARK-"+name, "--timeout", "10000"); err != nil {
			t.Fatalf("window %s never printed its marker: %v\n%s", name, err, out)
		}
	}
	if got := focusedWindowID(t, base, sess); got != focused || got == ids[0] || got == ids[1] {
		t.Fatalf("a worker took the focus (%s); the test needs the first pane focused", got)
	}

	t.Run("capture-pane", func(t *testing.T) {
		for _, target := range []string{ids[0], ids[0][:8], "worker-a"} {
			out, err := tuiosCLI(t, base, "capture-pane", "-s", sess, target)
			if err != nil {
				t.Fatalf("capture-pane -s %s %s: %v\n%s", sess, target, err, out)
			}
			if !strings.Contains(out, "MARK-worker-a") || strings.Contains(out, "MARK-worker-b") {
				t.Fatalf("ASSERTION: capture-pane -s %s %s did not capture worker-a, it printed:\n%s", sess, target, out)
			}
		}
		// Two different windows is a mistake to refuse, not a pick.
		if out, err := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", "worker-b", "worker-a"); err == nil {
			t.Fatalf("ASSERTION: capture-pane with -w worker-b and worker-a exited 0:\n%s", out)
		}
	})

	for _, verb := range [][]string{
		{"screenshot", "-s", sess, "--no-copy", "--format", "txt", ids[1]},
		{"list-windows", "-s", sess, ids[1]},
		{"kill-server", "now"},
	} {
		t.Run(verb[0], func(t *testing.T) {
			out, err := tuiosCLI(t, base, verb...)
			if err == nil {
				t.Fatalf("ASSERTION: tuios %s exited 0 with an argument it does not read:\n%s", strings.Join(verb, " "), out)
			}
			arg := verb[len(verb)-1]
			if !strings.Contains(out, "does not take arguments") || !strings.Contains(out, `"`+arg+`"`) {
				t.Fatalf("ASSERTION: tuios %s failed without naming the argument it refused:\n%s", verb[0], out)
			}
			if verb[0] == "screenshot" && !strings.Contains(out, "-w "+arg) {
				t.Fatalf("ASSERTION: tuios %s did not say to name the window with -w:\n%s", verb[0], out)
			}
		})
	}
	// kill-server refused its argument, so the daemon is still up.
	if out, err := tuiosCLI(t, base, "capture-pane", "-s", sess, "-w", "worker-b"); err != nil || !strings.Contains(out, "MARK-worker-b") {
		t.Fatalf("the daemon is not answering after the refused kill-server: %v\n%s", err, out)
	}
}
