package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRemoteDesktopSetsUpAHostAndReadsAFrame runs tuios remote-desktop
// --check against a linked machine over the real link: the ssh stand-in
// runs the real stdio-proxy against a second real daemon. The engine is
// fakerelay, which takes the engine's flags, checks the token and sends one
// tiles keyframe.
//
// How this could fail, and what the test checks:
//   - install: the engine never reaches the host, or lands outside its data
//     folder. The first run must say it installed and started the engine,
//     and the binary must be in the host's data folder.
//   - token: the token is made here, or leaks into a world-readable file.
//     It must be a 0600 file on the host, and the engine must accept the
//     viewer, which reads it over the link.
//   - frame: the path carries no frames. The check must read the 320x200
//     keyframe through tuios forward.
//   - again: a second run installs or starts the engine again. It must do
//     neither, and still read a frame.
//   - missing: tuios-rd is not installed. tuios remote-desktop must say how
//     to get it.
//
// Negative control: on origin/main tuios has no remote-desktop command.
func TestRemoteDesktopSetsUpAHostAndReadsAFrame(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	recv := filepath.Join(remote, "recv")
	if err := os.MkdirAll(recv, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nfiles_roots = [\""+recv+"\"]\n")
	killDaemon(t, remote)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	killDaemon(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start this machine's daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")

	bin := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "fakerelay"), "./testdata/fakerelay").CombinedOutput(); err != nil {
		t.Fatalf("build fakerelay: %v\n%s", err, out)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "tuios-rd"), "./cmd/tuios-rd")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tuios-rd: %v\n%s", err, out)
	}
	state := filepath.Join(xdgDir(remote, "XDG_STATE_HOME"), "tuios-rd")
	data := filepath.Join(xdgDir(remote, "XDG_DATA_HOME"), "tuios-rd", "engine")
	t.Cleanup(func() {
		// The engine runs on its own on build: stop it by its pid.
		if b, err := os.ReadFile(filepath.Join(state, "engine.pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	})
	rdEnv := append(env, "TUIOS_RD_BIN="+filepath.Join(bin, "tuios-rd"))
	check := func() string {
		out, errs, code := runSplit(t, base, rdEnv, "remote-desktop", "build", "--check",
			"--engine-bin", filepath.Join(bin, "fakerelay"), "--service", "process")
		t.Logf("tuios remote-desktop build --check:\n%s", out)
		if code != 0 || !strings.Contains(out, "ok build backend=fake frame=320x200") {
			dumpLinkLogs(t, base, remote)
			log, _ := os.ReadFile(filepath.Join(state, "engine.log"))
			t.Fatalf("ASSERTION: the check exited %d; stdout %q; stderr %q; engine log %q", code, out, errs, log)
		}
		return out
	}

	t.Run("install", func(t *testing.T) {
		out := check()
		if !strings.Contains(out, "Installing the engine ... done") || !strings.Contains(out, "Starting the engine ... done") {
			t.Fatalf("ASSERTION: the first run did not install and start the engine:\n%s", out)
		}
		if fi, err := os.Stat(filepath.Join(data, "relay-engine")); err != nil || fi.Mode()&0o111 == 0 {
			t.Fatalf("ASSERTION: no runnable engine in build's data folder: %v", err)
		}
	})

	t.Run("token", func(t *testing.T) {
		fi, err := os.Stat(filepath.Join(state, "token"))
		if err != nil {
			t.Fatalf("ASSERTION: no token file on build: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("ASSERTION: the token file is %v, want 0600", fi.Mode().Perm())
		}
		// The engine can be stopped by its pid: the pid file names it.
		b, _ := os.ReadFile(filepath.Join(state, "engine.pid"))
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 1 || syscall.Kill(pid, 0) != nil {
			t.Fatalf("ASSERTION: engine.pid holds %q, not the pid of the running engine", b)
		}
	})

	t.Run("again", func(t *testing.T) {
		out := check()
		if strings.Contains(out, "Installing") || strings.Contains(out, "Starting") {
			t.Fatalf("ASSERTION: a second run installed or started the engine again:\n%s", out)
		}
	})

	// With TUIOS_E2E_FRAMES, the first run is shown in a real window and
	// saved as a picture there, for a person to check.
	t.Run("frame", func(t *testing.T) {
		dir := os.Getenv("TUIOS_E2E_FRAMES")
		if dir == "" {
			t.Skip("set TUIOS_E2E_FRAMES to a folder for the picture")
		}
		// Start from nothing again: stop the engine by its pid, remove it.
		if b, err := os.ReadFile(filepath.Join(state, "engine.pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
		_ = os.Remove(filepath.Join(data, "relay-engine"))
		line := "TUIOS_RD_BIN=" + posixQuote(filepath.Join(bin, "tuios-rd")) + " " + posixQuote(tuiosBin) +
			" remote-desktop build --check --engine-bin " + posixQuote(filepath.Join(bin, "fakerelay")) + " --service process"
		if out, err := tuiosCLIEnv(t, base, env, "new-window", "rd", "-s", "home", "--keep-open", "--", "sh", "-c", line); err != nil {
			t.Fatalf("open the window: %v\n%s", err, out)
		}
		waitUntil(t, "the check finishes in the window", 60*time.Second, func() (bool, string) {
			out, _ := tuiosCLIEnv(t, base, env, "capture-pane", "-s", "home", "-w", "rd")
			return strings.Contains(out, "ok build"), out
		})
		// The pictures of the PRs use JetBrainsMono Nerd Font Mono.
		if font := os.Getenv("TUIOS_E2E_FRAME_FONT"); font != "" {
			cfg := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
			f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString("\n[screenshot]\nfont_file = " + strconv.Quote(font) + "\n")
			_ = f.Close()
			if out, err := tuiosCLIEnv(t, base, env, "config", "apply"); err != nil {
				t.Fatalf("config apply: %v\n%s", err, out)
			}
		}
		png := filepath.Join(dir, "remote-desktop-check.png")
		if out, err := tuiosCLIEnv(t, base, env, "screenshot", "-s", "home", "-w", "rd", "--format", "png", "--no-copy", "-o", png); err != nil {
			t.Fatalf("screenshot: %v\n%s", err, out)
		}
		t.Logf("frame: %s", png)
	})

	t.Run("missing", func(t *testing.T) {
		_, errs, code := runSplit(t, base, append(env, "PATH=/usr/bin:/bin"), "remote-desktop", "build")
		if code == 0 || !strings.Contains(errs, "needs the tuios-rd program") {
			t.Fatalf("ASSERTION: without tuios-rd, remote-desktop exited %d with %q; want a message that says how to get it", code, errs)
		}
	})
}
