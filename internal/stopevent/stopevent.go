// Package stopevent is how tuios kill-server asks a daemon on Windows to
// stop. Windows has no SIGTERM to send another process, and the daemon has
// no console to send a Ctrl+Break to, so the daemon waits on a named event
// and kill-server sets it. The daemon then shuts down as it does on a
// signal: it saves its sessions and removes its sockets.
package stopevent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
)

// ErrNotWaiting is what Request returns when no daemon waits on the event
// of a socket.
var ErrNotWaiting = errors.New("no daemon waits for a stop request")

// Name is the event's name for the daemon on socketPath. Two daemons with
// two sockets have two events. Windows paths ignore case, so the name does
// too. Local\ keeps the event in the Windows session the daemon runs in.
func Name(socketPath string) string {
	return `Local\` + baseName(socketPath)
}

// GlobalName is the same event as a process in another Windows session
// names it. A daemon started over OpenSSH runs in session 0, where Local\
// and Global\ are one namespace, so kill-server on the desktop reaches it
// as Global\.
func GlobalName(socketPath string) string {
	return `Global\` + baseName(socketPath)
}

func baseName(socketPath string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(socketPath))))
	return "tuios-daemon-stop-" + hex.EncodeToString(sum[:12])
}
