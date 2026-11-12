package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDaemonAppReadsTheLoginShellsPath: a daemon the TUIOS Daemon app starts
// with no client behind it, as the login item or a person opening the app
// does, gets the PATH the person's login shell sets, for fish as for bash.
// LaunchServices gives an app a bare environment, and the panes and every
// agent the daemon starts inherit the daemon's PATH, so without this an agent
// in ~/.local/bin, which fish users add in config.fish, is "not installed".
//
// How it could pass wrongly, written down first:
//   - The PATH could come from the test's own environment: the launcher runs
//     with PATH=/usr/bin:/bin, and the folder the test looks for is only in
//     the shell's login file.
//   - The login file could be read and the arguments mangled: an argument
//     with a space and one that looks like an option must reach the target
//     as they were, and the bundle's own path has a space in it.
//   - fish could read config.fish without being a login shell: the line sits
//     behind `status is-login`.
//
// NEGATIVE CONTROL: without the fish case in runDaemonAppLauncher, the fish
// case gets the fixed PATH and fails; the bash case passes either way.
func TestDaemonAppReadsTheLoginShellsPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the TUIOS Daemon app exists only on macOS")
	}
	for _, c := range []struct {
		shell string
		file  string
		line  string
	}{
		{"fish", ".config/fish/config.fish", "if status is-login\n    set -gx PATH %s $PATH\nend\n"},
		{"bash", ".bash_profile", "PATH=%s:$PATH; export PATH\n"},
	} {
		t.Run(c.shell, func(t *testing.T) {
			shell, err := exec.LookPath(c.shell)
			if err != nil {
				t.Skipf("%s is not installed", c.shell)
			}
			base := t.TempDir()
			home := filepath.Join(base, "home")
			marker := filepath.Join(base, "login only", "bin")
			login := filepath.Join(home, c.file)
			if err := os.MkdirAll(filepath.Dir(login), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(marker, 0o755); err != nil {
				t.Fatal(err)
			}
			quoted := "'" + marker + "'"
			if err := os.WriteFile(login, []byte(strings.Replace(c.line, "%s", quoted, 1)), 0o644); err != nil {
				t.Fatal(err)
			}

			// The bundle as daemon-app install lays it out, with a stand-in
			// for the tuios it execs that writes down what it was given.
			app := filepath.Join(base, "TUIOS Daemon.app")
			exe := filepath.Join(app, "Contents", "MacOS", "tuios-daemon-app")
			out := filepath.Join(base, "seen")
			target := filepath.Join(base, "stand in", "tuios")
			for _, d := range []string{filepath.Dir(exe), filepath.Join(app, "Contents", "Resources"), filepath.Dir(target)} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			bin, err := os.ReadFile(tuiosBin)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, bin, 0o755); err != nil { //nolint:gosec // the bundle's executable
				t.Fatal(err)
			}
			script := "#!/bin/sh\n{ printf '%s\\n' \"$PATH\"; for a; do printf '<%s>' \"$a\"; done; echo; } > '" + out + "'\n"
			if err := os.WriteFile(target, []byte(script), 0o755); err != nil { //nolint:gosec // the stand-in runs
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(app, "Contents", "Resources", "tuios-path"), []byte(target+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(exe, "daemon", "--flag-like", "two words")
			cmd.Env = []string{
				"HOME=" + home,
				"SHELL=" + shell,
				"PATH=/usr/bin:/bin",
				"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
				"XDG_DATA_HOME=" + filepath.Join(base, "data"),
				"XDG_CACHE_HOME=" + filepath.Join(base, "cache"),
			}
			if b, err := cmd.CombinedOutput(); err != nil {
				log, _ := os.ReadFile(filepath.Join(home, "Library", "Logs", "tuios-daemon-app.log"))
				t.Fatalf("the launcher failed: %v\n%s\nlog:\n%s", err, b, log)
			}
			seen := waitForFileText(t, out, func(s string) bool { return strings.Contains(s, "<") }, "the stand-in never ran")
			saveText(t, c.shell+"-seen", seen)
			lines := strings.SplitN(strings.TrimSpace(seen), "\n", 2)
			if len(lines) != 2 {
				t.Fatalf("the stand-in wrote %q, want the PATH and the arguments", seen)
			}
			if !strings.Contains(":"+lines[0]+":", ":"+marker+":") {
				t.Errorf("ASSERTION: the daemon's PATH %q lacks %s, which only the %s login file adds", lines[0], marker, c.shell)
			}
			if want := "<daemon><--flag-like><two words>"; lines[1] != want {
				t.Errorf("ASSERTION: the daemon got the arguments %s, want %s", lines[1], want)
			}
		})
	}
}
