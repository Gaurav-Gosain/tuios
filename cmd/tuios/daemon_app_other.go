//go:build !darwin

package main

func isDaemonAppLauncher() bool { return false }

func runDaemonAppLauncher() int { return 1 }

func startDaemonThroughApp([]string) (bool, error) { return false, nil }
