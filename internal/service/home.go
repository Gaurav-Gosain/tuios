//go:build darwin || linux

package service

import (
	"os"
	"os/user"
)

// passwdHome is the home folder the account database names. The user's
// service manager runs with it, whatever HOME this process has.
func passwdHome() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}
