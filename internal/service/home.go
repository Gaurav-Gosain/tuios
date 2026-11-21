//go:build darwin || linux

package service

import (
	"os"
	"os/user"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

func passwdHome() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func ownHome() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return passwdHome()
}

func samePath(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

const managerHomeKnob = "service_manager_home"

func useManagerHome() bool { return testknob.String(managerHomeKnob) != "" }

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
