// Package shell finds the user's login shell, as terminal apps take it.
package shell

import (
	"os"
	"sync"
)

var (
	once  sync.Once
	login string
)

// Login returns the user's login shell: the account's (dscl on macOS,
// NSS elsewhere, as getent passwd reads it, PowerShell on Windows),
// whatever $SHELL this process inherited from whoever started it, else
// $SHELL, else zsh.
func Login() string {
	once.Do(func() {
		login = account()
		if login == "" {
			login = os.Getenv("SHELL")
		}
		if login == "" {
			login = "/bin/zsh"
		}
	})
	return login
}
