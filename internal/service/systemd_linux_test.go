package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSystemctl puts a systemctl first on PATH that logs each call and
// answers show-environment with env, as the user manager would. It returns
// the log's path. No test of this package reaches the real manager.
func stubSystemctl(t *testing.T, env ...string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + logPath + "'\n" +
		"if [ \"$2\" = show-environment ]; then printf '%s\\n' " + shellWords(env) + "; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil { //nolint:gosec // a test stub must run
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func shellWords(words []string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func calls(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func testSpec(t *testing.T) Spec {
	t.Helper()
	return Spec{Name: "tuios-daemon-unittest", Binary: "/bin/true", Socket: filepath.Join(t.TempDir(), "s.sock")}
}

// TestInstallRefusesAHomeOfItsOwn is the bug where an e2e test wrote the
// person's real ~/.config/systemd/user/tuios-daemon.service: a process whose
// HOME is not the account's writes no unit and changes no service, even
// when the manager answers.
func TestInstallRefusesAHomeOfItsOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	logPath := stubSystemctl(t, "HOME="+passwdHome())
	real := filepath.Join(passwdHome(), ".config", "systemd", "user", "tuios-daemon-unittest.service")

	if err := Install(testSpec(t), true); err == nil {
		t.Fatal("Install with a HOME of its own succeeded")
	} else if !strings.Contains(err.Error(), "HOME="+home) {
		t.Errorf("the error does not name the HOME: %v", err)
	}
	if _, err := os.Stat(real); err == nil {
		t.Fatalf("Install wrote %s", real)
	}
	if got := calls(t, logPath); got != "" {
		t.Errorf("Install called systemctl: %q", got)
	}
	for _, dir := range []string{filepath.Join(home, "config"), filepath.Join(home, ".config")} {
		if entries, _ := os.ReadDir(filepath.Join(dir, "systemd", "user")); len(entries) > 0 {
			t.Errorf("Install wrote under %s", dir)
		}
	}
	if err := Start("tuios-daemon-unittest"); err == nil {
		t.Error("Start with a HOME of its own succeeded")
	}
	if got := calls(t, logPath); got != "" {
		t.Errorf("Start called systemctl: %q", got)
	}
}

// TestUninstallLeavesTheManagerAlone: a process with a home of its own
// removes the unit in its own folder and tells the manager nothing, so a
// unit of the same name in the person's folder stays enabled.
func TestUninstallLeavesTheManagerAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	logPath := stubSystemctl(t, "HOME="+passwdHome())
	own := filepath.Join(home, ".config", "systemd", "user", "tuios-daemon-unittest.service")
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("[Unit]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall("tuios-daemon-unittest"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(own); err == nil {
		t.Error("Uninstall left the unit in its own folder")
	}
	if got := calls(t, logPath); strings.Contains(got, "disable") || strings.Contains(got, "daemon-reload") {
		t.Errorf("Uninstall changed the manager: %q", got)
	}
}

// TestRenderRefusesAConfigHomeTheManagerDoesNotRead: with the account's HOME
// but an XDG_CONFIG_HOME the manager does not have, the unit would land
// where the manager never looks. Render says so and names both folders.
func TestRenderRefusesAConfigHomeTheManagerDoesNotRead(t *testing.T) {
	t.Setenv("HOME", passwdHome())
	other := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", other)
	stubSystemctl(t, "HOME="+passwdHome())
	_, _, err := Render(testSpec(t))
	if err == nil {
		t.Fatal("Render accepted a config home the manager does not read")
	}
	if !strings.Contains(err.Error(), other) {
		t.Errorf("the error does not name %s: %v", other, err)
	}
}
