package session

import (
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

// The docs rig.
//
// The docs site's screenshots are made by a rig: a script that starts a
// throwaway daemon and a client, and plays a tape in the client. A tape's
// keys do not act for the person (inbox_peek.go in internal/app): the daemon
// takes a call signed with the attach nonce as the person's, and an agent in
// a pane can play a tape in the person's own client. So a tape could not open
// an agent's thread, press a key in a TML form, or dock a surface, and the
// rig could not show those screens.
//
// A rig turns that one guard off, for tape keys, and only where everything
// the keys can reach belongs to the rig:
//
//   - The daemon is a rig when it starts with TUIOS_RIG=1 and rigRefusal
//     finds nothing: XDG_CONFIG_HOME and XDG_STATE_HOME are set to absolute
//     folders that are not the defaults of the account's home (accountHome,
//     not HOME, which the rig may move), and its socket is outside
//     the person's socket folders (sockpath.PersonDirs). So a rig never
//     restores the person's sessions, reads their config or their hosts, or
//     serves the socket their client attaches to. A refusal is logged and
//     the daemon starts as an ordinary one: the guard stays on.
//   - The variable is read once, at start. No verb, config key or reload
//     turns a running daemon into a rig.
//   - The client honours it only when it was itself started with
//     TUIOS_RIG=1 and the daemon's attach reply says rig. The person's
//     client has no such variable, and the person's daemon never says rig.
//   - Only a tape's Press acts for the person. send-keys, which any process
//     of the user can run against any client, stays refused in a rig too.
//     So do the confirmations that never take a key that was not typed:
//     plugin approval, the integration and upgrade asks, and the machine
//     sheet.
//   - A rig never vouches for the person over a host link: a connection it
//     opens to another machine is never marked human.
//
// An agent can start a rig. What it gains is a daemon of its own that it
// could already drive with every verb: the rig holds none of the person's
// sessions, and no machine trusts it as the person.

// rigRefusal says why the daemon on socketPath may not be a rig, or "" when
// it may. getenv reads the daemon's environment, home is the user's home,
// and personDirs are the person's socket folders.
func rigRefusal(socketPath string, getenv func(string) string, home string, personDirs []string) string {
	for _, v := range []struct{ key, def string }{
		{"XDG_CONFIG_HOME", filepath.Join(home, ".config")},
		{"XDG_STATE_HOME", filepath.Join(home, ".local", "state")},
	} {
		dir := getenv(v.key)
		if dir == "" || !filepath.IsAbs(dir) {
			return v.key + " is not set to an absolute folder of the rig's own"
		}
		if filepath.Clean(dir) == filepath.Clean(v.def) {
			return v.key + " is the home's own " + v.def
		}
	}
	sock := filepath.Clean(socketPath)
	for _, dir := range personDirs {
		if dir == "" {
			continue
		}
		if rel, err := filepath.Rel(filepath.Clean(dir), sock); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "the socket " + socketPath + " is in the person's socket folder " + dir
		}
	}
	return ""
}

// startRig reads TUIOS_RIG once, at start, and makes the daemon a rig when
// rigRefusal allows it.
func (d *Daemon) startRig(socketPath string) {
	if os.Getenv("TUIOS_RIG") != "1" {
		return
	}
	if why := rigRefusal(socketPath, os.Getenv, accountHome(), sockpath.PersonDirs()); why != "" {
		log.Printf("Warning: TUIOS_RIG is set, and this daemon is not a rig: %s. A tape's keys do not act for the person here.", why)
		return
	}
	d.rig = true
	log.Printf("This daemon is a docs rig: a tape's keys act for the person in a client started with TUIOS_RIG=1. Do not use it for real work.")
}

// accountHome is the home folder the account database names, where the
// person's own config and state are. HOME does not decide it: a docs rig
// with a home of its own (the docs site's short_home scenes) runs with HOME
// at a throwaway folder and the XDG folders inside it, and those are the
// rig's, not the person's. A HOME moved the other way, to a throwaway folder
// with XDG_CONFIG_HOME at the person's ~/.config, is refused. Only when the
// database has no entry does HOME stand in, which is the rule as it was.
func accountHome() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}
