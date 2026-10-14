// Package serverscope names the daemon a tuios process belongs to.
//
// A named server is a daemon of its own: its socket sits in a folder of its
// own beside the default daemon's, and its saved sessions, resurrection
// archive, daemon log and every other file the daemon keeps live under a state
// folder of its own. The default server has no name and uses the folders it
// always has. Configuration is shared by every server.
//
// The scope is held in this process, never in XDG_RUNTIME_DIR or
// XDG_STATE_HOME. Those reach every program run in a pane, and a Wayland
// client, systemctl --user or an editor keeping its undo history would find
// its own folders moved. TUIOS_SERVER is what carries the name to the daemon
// tuios starts and to every tuios run from one of its panes.
package serverscope

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// Env is the variable that names the server when --server does not.
const Env = "TUIOS_SERVER"

// StateDirName is the folder under the state home that holds one folder per
// named server. Each of those stands in for the state home itself, so a named
// server's files are at <state home>/tuios-servers/<name>/tuios/...
const StateDirName = "tuios-servers"

// SocketDirName is the folder under the default daemon's socket folder that
// holds one socket folder per named server.
const SocketDirName = "servers"

// MaxNameLen keeps a name inside the Unix socket path budget, which the agent
// sockets beside the daemon's spend most of: with it, the longest of them under
// /run/user/<uid>/tuios/servers/<name> or /tmp/tuios-<uid>/servers/<name>
// stays under the 104 bytes macOS allows. The longest such socket is not the
// daemon's own or a session's agent link (agent-<uuid>.sock) but a host's
// agent-forwarding link (agent-link-<up to 32 chars>-<8 hex digits>.sock, see
// internal/session/ssh_agent_follow.go), ten bytes longer, which is why this
// is smaller than the budget would allow for the daemon's own sockets alone.
const MaxNameLen = 14

// SocketPathGrowth is the most a server name lengthens the path of any socket
// beside the daemon's: the servers/<name>/ between the default server's socket
// folder and the socket. Anything that sizes a socket folder for the default
// server has to leave this much more room for a named one.
const SocketPathGrowth = len(SocketDirName) + 1 + MaxNameLen + 1

var (
	name string
	// baseStateHome is the state home the default server uses. It is only
	// meaningful while a named server is applied, since xdg.StateHome is the
	// base itself whenever the process is on the default server.
	baseStateHome string
)

// Name is the server this process belongs to, or "" for the default one.
func Name() string { return name }

// Validate reports whether s can name a server: 1 to 14 letters, digits, '-'
// or '_'. The name becomes a folder, so nothing that could leave the folder it
// is joined to is accepted.
func Validate(s string) error {
	if s == "" {
		return fmt.Errorf("server name is empty")
	}
	if len(s) > MaxNameLen {
		return fmt.Errorf("server name %q is longer than %d characters", s, MaxNameLen)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("server name %q may only contain letters, digits, '-' and '_'", s)
		}
	}
	return nil
}

// Apply makes s the server this process belongs to and records it in Env, so
// a daemon started from here and every pane it runs belong to it too. An empty
// s is the default server, and clears Env.
//
// Applying "" to a process that was never scoped changes nothing at all: no
// variable is set or cleared and no folder moves.
func Apply(s string) error {
	if s == "" {
		if name == "" {
			return nil
		}
		name = ""
		xdg.StateHome = baseStateHome
		return os.Unsetenv(Env)
	}
	if err := Validate(s); err != nil {
		return err
	}
	if name == "" {
		baseStateHome = xdg.StateHome
	}
	name = s
	xdg.StateHome = filepath.Join(baseStateHome, StateDirName, s)
	return os.Setenv(Env, s)
}

// ApplyEnv applies the server TUIOS_SERVER names. Its error says what is wrong
// and what to do about it, since the variable is set somewhere the reader may
// not be looking: a pane's environment, a shell profile.
func ApplyEnv() error {
	v := os.Getenv(Env)
	if err := Apply(v); err != nil {
		return fmt.Errorf("%s is %q, which is not a valid server name. A name is 1 to %d letters, digits, '-' and '_'. Unset %s or set it to a valid name", Env, v, MaxNameLen, Env)
	}
	return nil
}

// BaseStateHome is the state home before any server scoped it: the one the
// default server keeps its files under, and the parent of every named
// server's.
func BaseStateHome() string {
	if name != "" {
		return baseStateHome
	}
	return xdg.StateHome
}

// SocketDir is the folder the current server's socket lives in, given the
// default server's.
func SocketDir(base string) string {
	if name == "" {
		return base
	}
	return filepath.Join(base, SocketDirName, name)
}
