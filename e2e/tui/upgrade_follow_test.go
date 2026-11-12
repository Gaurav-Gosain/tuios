package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// newerVersion is the version newerTuios is stamped with, newer than any
// release and than a source build.
const newerVersion = "v99.1.0"

// TestAttachedClientRunsTheUpgradedBuild attaches a real client, with an
// interface flag, to a session with a shell, and upgrades the daemon to a
// build stamped newerVersion. The client process, by its pid, then runs that
// build: tuios attach on the same session, on the same terminal, with the
// flag it was started with. The shell keeps its pid, and a line typed at the
// new client reaches it. With daemon.client_follows_upgrade off, the client
// keeps its build and reattaches as before.
//
// The ways it can go wrong, each an assertion here:
//   - the client keeps running the old build after the upgrade (the
//     person's report: they had to quit and start tuios again);
//   - it runs the new build in a new process or a new terminal, so the pid
//     changes or the terminal ends;
//   - it attaches to another session, or loses the panes;
//   - it drops the flags it was started with;
//   - it follows with the option off.
//
// The command lines before and after go under artifactDir as
// upgrade-follow.json.
func TestAttachedClientRunsTheUpgradedBuild(t *testing.T) {
	newer := newerTuios(t)
	for _, follow := range []bool{true, false} {
		t.Run("follow="+strconv.FormatBool(follow), func(t *testing.T) {
			base := t.TempDir()
			if !follow {
				writeConfig(t, base, "[daemon]\nclient_follows_upgrade = false\n")
			}
			startHandoffDaemon(t, base)
			sess := "e2e-runs-new-" + strconv.FormatBool(follow)
			win := newCommandWindow(t, base, sess, "shell", false, "sh")
			term, logPath := startInLogged(t, base, startOpts{args: []string{"attach", "--no-animations", sess}})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
				t.Fatalf("never attached: %v\n%s", err, term.Snapshot())
			}
			windowManagementMode(t, term)
			enterTerminalMode(t, term)
			if err := term.SendKeys("echo before-$((20+1))", tuitest.Enter); err != nil {
				t.Fatal(err)
			}
			if err := term.WaitForText("before-21", uiTimeout); err != nil {
				t.Fatalf("the shell never answered before the upgrade: %v\n%s", err, term.Snapshot())
			}
			pid := term.Pid()
			cmdBefore := processCommand(t, pid)
			pids := panePIDs(t, base, sess)

			text, err := handoffCLI(t, base, 90*time.Second, "daemon", "upgrade", "--binary", newer)
			if err != nil {
				t.Fatalf("tuios daemon upgrade failed: %v\n%s", err, text)
			}
			want := "The daemon was upgraded to " + newerVersion + ". This window runs it too."
			if !follow {
				want = "tuios reattached."
			}
			said := term.WaitForText(want, 2*bootTimeout)
			code, exited := term.ExitCode()
			cmdAfter := processCommand(t, pid)
			exeAfter := processExe(pid)
			var typed string
			var typedErr error
			if said == nil && !exited {
				windowManagementMode(t, term)
				enterTerminalMode(t, term)
				if typedErr = term.SendKeys("echo after-$((40+2))", tuitest.Enter); typedErr == nil {
					typedErr = term.WaitForText("after-42", uiTimeout)
				}
				typed = paneScreen(t, base, sess, win)
			}
			afterPIDs := panePIDs(t, base, sess)
			logBytes, _ := os.ReadFile(logPath)
			writeHandoffArtifact(t, map[string]any{"upgrade": text, "client_pid": pid, "command_before": cmdBefore,
				"command_after": cmdAfter, "exe_after": exeAfter, "client_exited": exited, "client_exit": code,
				"panes_before": pids, "panes_after": afterPIDs, "pane_after_typing": typed, "typing_error": errString(typedErr)})

			if exited {
				t.Fatalf("ASSERTION: the client exited (code %d) after the upgrade\nlog tail:\n%s\n%s", code, tailMessage(string(logBytes)), term.Snapshot())
			}
			if said != nil {
				t.Fatalf("ASSERTION: the client never said %q after the upgrade: %v\n%s", want, said, term.Snapshot())
			}
			runsNewer := strings.HasPrefix(cmdAfter, newer+" ") || (exeAfter != "" && exeAfter == newer)
			if follow {
				if !runsNewer {
					t.Errorf("ASSERTION: client pid %d runs %q (exe %q) after the upgrade, want %s", pid, cmdAfter, exeAfter, newer)
				}
				for _, part := range []string{"--no-animations=true", "attach", "--after-upgrade=" + newerVersion, sess} {
					if !strings.Contains(cmdAfter, part) {
						t.Errorf("ASSERTION: the new client's command line %q has no %q", cmdAfter, part)
					}
				}
			} else if runsNewer || cmdAfter != cmdBefore {
				t.Errorf("ASSERTION: with client_follows_upgrade off the client changed from %q to %q", cmdBefore, cmdAfter)
			}
			if typedErr != nil || !strings.Contains(typed, "after-42") {
				t.Errorf("ASSERTION: a line typed at the client after the upgrade did not reach the shell: %v\npane:\n%s\nclient:\n%s", typedErr, typed, term.Snapshot())
			}
			if afterPIDs[win] == 0 || afterPIDs[win] != pids[win] {
				t.Errorf("ASSERTION: the shell had pid %d and %d after the upgrade", pids[win], afterPIDs[win])
			}
		})
	}
}

// processCommand is the command line pid runs, its arguments joined by
// spaces.
func processCommand(t *testing.T, pid int) string {
	t.Helper()
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		if err != nil {
			t.Fatalf("read the command line of %d: %v", pid, err)
		}
		return strings.ReplaceAll(strings.TrimRight(string(raw), "\x00"), "\x00", " ")
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps %d: %v", pid, err)
	}
	return strings.TrimSpace(string(out))
}

// processExe is the file pid's image was loaded from, where the system says
// it (Linux), and empty elsewhere.
func processExe(pid int) string {
	exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return exe
}

var (
	newerOnce sync.Once
	newerBin  string
	newerErr  error
	// newerDir holds that build, and runE2E removes it when the run ends,
	// as it does otherProtocolDir.
	newerDir string
)

// newerTuios builds this tree once per run stamped newerVersion, as a
// release after this one would be.
func newerTuios(t *testing.T) string {
	t.Helper()
	newerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tuios-e2e-newer")
		if err != nil {
			newerErr = err
			return
		}
		newerDir = dir
		bin := filepath.Join(dir, "tuios")
		build := goBuild("-ldflags", "-X main.version="+newerVersion, "-o", bin, "./cmd/tuios")
		build.Dir = "../.."
		if out, err := build.CombinedOutput(); err != nil {
			newerErr = &buildError{err: err, out: string(out)}
			return
		}
		if resolved, err := filepath.EvalSymlinks(bin); err == nil {
			bin = resolved
		}
		newerBin = bin
	})
	if newerErr != nil {
		t.Fatalf("build tuios stamped %s: %v", newerVersion, newerErr)
	}
	return newerBin
}

// buildError is a go build that failed, with what it printed.
type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }
