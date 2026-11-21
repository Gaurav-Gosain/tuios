//go:build unix && !linux

package main

// jobLeaderName cannot read another process's name here.
func jobLeaderName(int) (string, bool) { return "", false }
