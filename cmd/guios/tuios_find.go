package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// findTuios returns the tuios binary guios starts the daemon with. The daemon
// is its own process, never this one: it outlives the window, and a fault in
// the GUI must not end every session.
//
// The order: the --tuios flag, a tuios beside this executable (the app
// bundle puts one there), PATH, the login shell's PATH, then the places
// package managers use. A Finder or Dock launch has a thin PATH, which is why
// the login shell is asked.
func findTuios(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	name := "tuios"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, p := range []string{filepath.Join(dir, name), filepath.Join(dir, "..", "Resources", name)} {
			if isExec(p) {
				return p, nil
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range loginPath() {
		if p := filepath.Join(dir, name); isExec(p) {
			return p, nil
		}
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, "go", "bin"), filepath.Join(home, ".nix-profile", "bin")} {
		if p := filepath.Join(dir, name); isExec(p) {
			return p, nil
		}
	}
	return "", errors.New("tuios was not found. Install tuios, or start guios with --tuios PATH")
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// loginPath is the PATH of the person's login shell, read with a 3 s cap.
func loginPath() []string {
	sh := os.Getenv("SHELL")
	if sh == "" || runtime.GOOS == "windows" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, sh, "-lc", `printf %s "$PATH"`).Output()
	if err != nil {
		return nil
	}
	return filepath.SplitList(strings.TrimSpace(string(out)))
}

// ensureDaemon starts a daemon with `tuios start-server` when none answers.
// start-server is the path every tuios client takes, so the daemon starts the
// same way here (through the TUIOS Daemon app on macOS when it is installed).
func ensureDaemon(tuios string) error {
	if err := session.CheckSocketEnv(); err != nil {
		return err
	}
	if session.IsDaemonRunning() {
		return nil
	}
	out, err := exec.Command(tuios, "start-server").CombinedOutput()
	if err != nil && !session.IsDaemonRunning() {
		return fmt.Errorf("the tuios daemon did not start: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
