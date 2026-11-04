//go:build !(linux && (amd64 || arm64))

package main

// installGLibPoll is for GTK's main loop on Linux (glibpoll_linux.go).
func installGLibPoll() {}
