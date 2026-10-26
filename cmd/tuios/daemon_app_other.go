//go:build !darwin

package main

// The TUIOS Daemon app exists only on macOS. See daemon_app.go.

func isDaemonAppLauncher() bool { return false }

func runDaemonAppLauncher() int { return 1 }

func startDaemonThroughApp([]string) (bool, error) { return false, nil }
