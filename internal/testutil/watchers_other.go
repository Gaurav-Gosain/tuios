//go:build !linux && !darwin

package testutil

const watcherKind = "watchers"

func watcherInstances() int { return 0 }

func startPoller() {}
