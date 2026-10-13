package netutil

import (
	"os"
	"os/user"
)

// CurrentAccount names the account a session's shells run as, for the
// messages both servers print about who can reach them.
func CurrentAccount() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return "the user running this server"
}
