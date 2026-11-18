package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallRefusesAHomeOfItsOwn is the macOS side of the bug where an e2e
// test wrote the person's real service: a process whose HOME is not the
// account's writes no agent into the real LaunchAgents and loads nothing.
func TestInstallRefusesAHomeOfItsOwn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := Spec{Name: "tuios-daemon-unittest", Binary: "/usr/bin/true", Socket: filepath.Join(t.TempDir(), "s.sock")}
	real := filepath.Join(passwdHome(), "Library", "LaunchAgents", label(s.Name)+".plist")

	err := Install(s, true)
	if err == nil {
		t.Fatal("Install with a HOME of its own succeeded")
	}
	if !strings.Contains(err.Error(), "HOME="+home) {
		t.Errorf("the error does not name the HOME: %v", err)
	}
	if _, err := os.Stat(real); err == nil {
		t.Fatalf("Install wrote %s", real)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "Library", "LaunchAgents")); len(entries) > 0 {
		t.Error("Install wrote an agent under the test's HOME")
	}
	if err := Start(s.Name); err == nil {
		t.Error("Start with a HOME of its own succeeded")
	}
}
