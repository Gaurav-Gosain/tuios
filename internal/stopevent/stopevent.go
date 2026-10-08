// Package stopevent is how tuios kill-server asks a daemon on Windows to
// stop. Windows has no SIGTERM to send another process, and the daemon has
// no console to send a Ctrl+Break to, so the daemon waits on a named event
// and kill-server sets it. The daemon then shuts down as it does on a
// signal: it saves its sessions and removes its sockets.
package stopevent

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// Name is the event's name for the daemon on socketPath. Two daemons with
// two sockets have two events. Windows paths ignore case, so the name does
// too. Local\ keeps the event in the person's own logon session.
func Name(socketPath string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(socketPath))))
	return `Local\tuios-daemon-stop-` + hex.EncodeToString(sum[:12])
}
