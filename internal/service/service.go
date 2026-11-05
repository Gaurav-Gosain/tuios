// Package service runs the tuios daemon as a service of the user: a systemd
// user unit on Linux, a launchd agent on macOS, and a Task Scheduler task on
// Windows. `tuios daemon install-service` writes it.
//
// It is adapted from Kero's internal/service (https://github.com/egoist/kero,
// MIT, Copyright (c) 2026 EGOIST). See NOTICE.
//
// The service runs `tuios daemon --service` in the foreground. The flag tells
// the daemon that a service manager started it, so a daemon that finds
// another one already serving the socket exits 0 and the manager does not
// start it again and again.
//
// What the unit must get right, and why (docs/SESSIONS.md has the long form):
//
//   - KillMode=process. The daemon ends its panes itself when it stops, after
//     it saves them. A pane program that outlives it on purpose (a job started
//     with nohup or setsid), the restart-server worker started from a pane,
//     and the keeper of a live upgrade (internal/session/handoff_keeper_unix.go)
//     all sit in the unit's cgroup. control-group would kill each of them
//     when the main process ends.
//   - Restart=on-failure. kill-server and restart-server stop the daemon with
//     exit status 0, and the manager leaves it stopped. A crash restarts it.
//   - A live upgrade execs the new image in the same process, so the manager
//     sees the same main pid and nothing changes for it.
//   - The command runs through the user's login shell, so the panes get the
//     PATH a login gives, not the manager's short one.
//   - DISPLAY, WAYLAND_DISPLAY and XAUTHORITY are unset, so the daemon has no
//     display of its own and [daemon] display = "follow" gives each session
//     the display of the client that attached last.
package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupported says this system has no service manager tuios knows.
var ErrUnsupported = errors.New("tuios runs the daemon as a service only with systemd (Linux), launchd (macOS) or Task Scheduler (Windows)")

// DefaultName is the service of the default server.
const DefaultName = "tuios-daemon"

// Name is the service for a server: tuios-daemon, or tuios-daemon-NAME for a
// named server, which has a daemon and a socket of its own.
func Name(server string) string {
	if server == "" {
		return DefaultName
	}
	return DefaultName + "-" + server
}

// Spec is what a service runs.
type Spec struct {
	// Name is the unit name without its suffix. See Name.
	Name string
	// Binary is the absolute path of the tuios the service runs.
	Binary string
	// Shell is the login shell the command runs through ($SHELL). Empty uses
	// /bin/sh.
	Shell string
	// Socket is the socket the daemon serves. The unit names it in a comment,
	// so a client starts the service only for its own socket.
	Socket string
	// Dir is the working directory, the home folder.
	Dir string
	// App, on macOS, is the TUIOS Daemon app (tuios daemon-app). When it is
	// set, the agent opens the app, which starts the daemon, so the app's
	// Local Network permission covers the panes.
	App string
	// Env are KEY=VALUE pairs the unit sets for the daemon: the variables
	// that choose its socket and folders, where they differ from what the
	// service manager has. See CarriedEnv.
	Env []string
}

// Status is how a service stands.
type Status struct {
	// Supported says this system has a service manager tuios knows.
	Supported bool   `json:"supported"`
	Name      string `json:"name"`
	// File is the unit, the property list, or the task's name.
	File      string `json:"file,omitempty"`
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	// PID is the service's main process, 0 when it runs none or the manager
	// does not say.
	PID int `json:"pid,omitzero"`
	// Detail is what the manager says, such as systemctl's "active".
	Detail string `json:"detail,omitempty"`
	// Socket is the socket the installed service serves.
	Socket string `json:"socket,omitempty"`
	// Linger says systemd keeps the user's services running with no login
	// and starts them at boot. It is true where no such setting exists.
	Linger bool `json:"linger"`
	// User is the account name, for the linger instruction.
	User string `json:"user,omitempty"`
	// Binary is the tuios the installed service runs.
	Binary string `json:"binary,omitempty"`
}

// socketMarker starts the comment line that names the socket.
const socketMarker = "# Socket: "

// binaryMarker starts the comment line that names the binary.
const binaryMarker = "# Binary: "

// carriedKeys are the variables that choose the daemon's socket and folders.
// The unit sets each one that differs from the service manager's, so a daemon
// the service starts serves the socket a client of the installer reaches.
var carriedKeys = []string{
	"TUIOS_SERVER", "HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME",
	"XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
}

// CarriedEnv returns the variables of carriedKeys whose value in getenv
// differs from manager's. A key the process does not set is not carried.
func CarriedEnv(getenv func(string) string, manager map[string]string) []string {
	var out []string
	for _, k := range carriedKeys {
		v := getenv(k)
		if v == "" || v == manager[k] {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}

// loginShells are the shells whose -l -c runs a command the same way.
var loginShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "mksh": true}

// LoginCommand is the command line that runs the daemon through the login
// shell: shell -l -c 'exec BINARY daemon --service'. exec keeps the pid, so
// the service manager's main process is the daemon. fish reads '\” as a
// POSIX shell does, but not a backslash inside quotes, so a path with one
// runs through /bin/sh. A shell that is not known runs /bin/sh too.
func LoginCommand(shell, binary string) []string {
	base := filepath.Base(shell)
	if !filepath.IsAbs(shell) || !loginShells[base] || (base == "fish" && strings.Contains(binary, `\`)) {
		shell = "/bin/sh"
	}
	return []string{shell, "-l", "-c", "exec '" + strings.ReplaceAll(binary, "'", `'\''`) + "' daemon --service"}
}

// readMarkers reads the socket and binary comment lines of a service file.
func readMarkers(path string) (socket, binary string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	// A unit has the marker at the start of a line, a property list inside
	// an XML comment with the path escaped.
	value := func(line, marker string) (string, bool) {
		_, after, ok := strings.Cut(line, marker)
		if !ok {
			return "", false
		}
		v := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(after), "-->"))
		return unescapeMarker(v), true
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := value(line, socketMarker); ok && socket == "" {
			socket = v
		}
		if v, ok := value(line, binaryMarker); ok && binary == "" {
			binary = v
		}
	}
	return socket, binary
}

// sameSocket reports whether two socket paths name one file.
func sameSocket(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// check refuses a spec that cannot make a working service.
func (s Spec) check() error {
	if !filepath.IsAbs(s.Binary) || strings.ContainsAny(s.Binary+s.Socket+s.Dir+strings.Join(s.Env, ""), "\n\r") {
		return errors.New("the binary must be an absolute path, and no path or variable may hold a line break")
	}
	return nil
}
