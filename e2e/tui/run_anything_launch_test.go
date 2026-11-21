package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const runAnythingMarker = "RUN_ANYTHING_OK"

const probeName = "zzprobe"

func writeProbe(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "odd dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("probe dir: %v", err)
	}
	script := "#!/bin/sh\necho " + runAnythingMarker + "\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, probeName), []byte(script), 0o755); err != nil {
		t.Fatalf("probe script: %v", err)
	}
	return dir
}

const launcherTitle = "Run a program"

var altSpace = tuitest.Alt(" ")

func openLauncher(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(altSpace); err != nil {
		t.Fatalf("send alt+space: %v", err)
	}
	if err := term.WaitForText(launcherTitle, uiTimeout); err != nil {
		t.Fatalf("the launcher never opened: %v\n%s", err, term.Snapshot())
	}
}

func queryProbe(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	openLauncher(t, term)
	if err := term.SendKeys(probeName); err != nil {
		t.Fatalf("type the probe's name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Count(s.Text(), probeName) >= 2
	}, uiTimeout); err != nil {
		t.Fatalf("the scanned program never reached the launcher: %v\n%s", err, term.Snapshot())
	}
}

func launchProbe(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the probe: %v", err)
	}
	if err := term.WaitForText(runAnythingMarker, shellTimeout); err != nil {
		t.Fatalf("the launched program never printed: %v\n%s", err, term.Snapshot())
	}
}

// TestLauncherIsNotTheCommandPalette is the separation, checked where it is
// visible: ctrl+p lists commands and does not list programs, and alt+space
// lists programs.
//
// The launcher half runs first, deliberately. Its row wait is what proves a
// scan has landed and the probe is in the program tables, and only then does
// "the palette does not list it" mean anything. Asked the other way round the
// palette was quizzed before any scan had run, and "No matching commands"
// passed just as readily against a build that ranks programs among the
// commands but had not found them yet.
func TestLauncherIsNotTheCommandPalette(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin")},
	})
	waitBoot(t, term)

	queryProbe(t, term)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the launcher: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), launcherTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("the launcher never closed: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("send ctrl+p: %v", err)
	}
	waitPaletteOpen(t, term, "for the command palette")
	if err := term.SendKeys(probeName); err != nil {
		t.Fatalf("type the probe's name: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "No matching commands")
	}, uiTimeout); err != nil {
		t.Fatalf("a program on $PATH is still ranked among the commands: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the palette: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), paletteTitle)
	}, uiTimeout); err != nil {
		t.Fatalf("the palette never closed: %v\n%s", err, term.Snapshot())
	}

	queryProbe(t, term)
}

// TestLauncherTypesTheCommandOut is the second verb on a row. Tab opens a pane
// running a shell with the command line waiting at its prompt, so the marker
// the program prints must NOT appear: nothing has been run yet. The program's
// name on the prompt is what proves the line arrived.
func TestLauncherTypesTheCommandOut(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin"), "PS1=" + typeOutPrompt + " "},
	})
	waitBoot(t, term)
	queryProbe(t, term)

	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("type the probe out: %v", err)
	}
	waitForPaneShell(t, term)
	requireTypedThenRuns(t, term)
}

// TestRunAnythingExecsLocalPane pins the local half of the launch semantics: a
// program picked in the palette becomes a pane whose process is that program,
// exec'd from a path containing a space, with no shell in between to re-parse
// or race it.
func TestRunAnythingExecsLocalPane(t *testing.T) {
	dir := writeProbe(t)
	term, _ := start(t, startOpts{
		args: []string{"--standalone"},
		env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin")},
	})
	waitBoot(t, term)
	launchProbe(t, term)
}

// TestRunAnythingExecsDaemonPane is the daemon half: the argv rides the
// NewWindow intent to the daemon, the daemon execs it as the pane's process,
// and the pane comes back in a state push. This is the path that used to type
// the command into a freshly spawned shell and hope it was ready.
//
// The exit runs are launcher.keep_open (#1308). The probe prints its marker
// and exits with status 3. With the option the pane stays, with the output
// and the daemon's exit line under it. Without it the pane closes: that is
// the positive half, which proves the probe did exit, so a pane that stays
// is the option's doing and not a program still running.
func TestRunAnythingExecsDaemonPane(t *testing.T) {
	runs := []struct {
		name     string
		config   string
		exits    bool
		keepOpen bool
	}{
		{name: "runs"},
		{name: "exits", exits: true},
		{name: "keep-open", config: "[launcher]\nkeep_open = true\n", exits: true, keepOpen: true},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			dir := writeProbe(t)
			if r.exits {
				script := "#!/bin/sh\necho " + runAnythingMarker + "\nexit 3\n"
				if err := os.WriteFile(filepath.Join(dir, probeName), []byte(script), 0o755); err != nil {
					t.Fatalf("probe script: %v", err)
				}
			}
			base := t.TempDir()
			killDaemon(t, base)
			if r.config != "" {
				writeConfig(t, base, r.config)
			}

			if out, err := tuiosCLI(t, base, "new", "e2e-run", "--detach"); err != nil {
				t.Fatalf("create detached session: %v: %s", err, out)
			}
			term := startIn(t, base, startOpts{
				args: []string{"attach", "e2e-run"},
				env:  []string{"PATH=" + dir + string(os.PathListSeparator) + agentFreePath("/usr/bin", "/bin")},
			})
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return countWindows(s) == 1
			}, bootTimeout); err != nil {
				t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
			}
			if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
				t.Fatalf("switch to window mode: %v", err)
			}
			if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
				t.Fatalf("client never reached window management mode: %v\n%s", err, term.Snapshot())
			}
			if !r.exits {
				launchProbe(t, term)
				return
			}

			queryProbe(t, term)
			if err := term.SendKeys(tuitest.Enter); err != nil {
				t.Fatalf("run the probe: %v", err)
			}
			if !r.keepOpen {
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, shellTimeout); err != nil {
					t.Fatalf("the probe's pane never opened: %v\n%s", err, term.Snapshot())
				}
				if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, shellTimeout); err != nil {
					t.Fatalf("the pane stayed after the probe exited, without keep_open: %v\n%s", err, term.Snapshot())
				}
				return
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				text := s.Text()
				return strings.Contains(text, runAnythingMarker) && strings.Contains(text, "exited with status 3")
			}, shellTimeout); err != nil {
				t.Fatalf("the kept pane does not show the output and the exit status: %v\n%s", err, term.Snapshot())
			}
			time.Sleep(time.Second)
			if n := countWindows(term.Screen()); n != 2 {
				t.Fatalf("%d panes after the probe exited, want the kept pane and the first\n%s", n, term.Snapshot())
			}
			saveArtifact(t, term, artifactDir(t), "kept-open")
		})
	}
}
