//go:build darwin || linux

package service

import (
	"os"
	"os/user"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
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

// ownHome is the home folder this process runs with: HOME, or the
// account's home when HOME is unset.
func ownHome() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return passwdHome()
}

// samePath reports whether a and b name one folder, links resolved, so a
// home under a linked /home still counts as the account's.
func samePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// managerHomeKnob makes a process with a home of its own use the folders and
// the service manager of the account anyway, as tuios did before. Only
// TestDaemonRunsAsAService sets it, and only on a machine whose user manager
// it may change (TUIOS_E2E_OWN_USER_MANAGER=1).
const managerHomeKnob = "service_manager_home"

func useManagerHome() bool { return testknob.String(managerHomeKnob) != "" }

// checkHome refuses to touch the user's service manager from a process whose
// HOME is not the home of the account, such as a test or an isolated server.
// Such a process writes its service files under its own HOME, where the
// manager never reads them, and it must not change the person's services.
// An e2e test once wrote the real unit this way, pointed at its temp HOME,
// and the unit failed in a loop after the test removed that folder.
func checkHome() error {
	if useManagerHome() {
		return nil
	}
	own, account := ownHome(), passwdHome()
	if !samePath(own, account) {
		return errfmt.Errorf("this tuios runs with HOME=%s, and your service manager runs with %s. tuios installs and starts a service only from the home of your login", own, account)
	}
	return nil
}
