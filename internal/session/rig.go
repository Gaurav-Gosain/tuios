package session

import (
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/sockpath"
)

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

func accountHome() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}
