//go:build !windows

package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"

	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// cliLinks are where installCLI puts the tuios command, and where Settings
// looks for it.
func cliLinks() []string {
	return []string{"/usr/local/bin/tuios", "/opt/homebrew/bin/tuios", filepath.Join(proto.Home(), ".local/bin/tuios")}
}

// installCLI links the tuios command to the app's tuios: /usr/local/bin's
// on macOS, asking for an administrator's password when the directory
// needs it; ~/.local/bin's on Linux, which needs none.
func (a *App) installCLI() {
	// The tuios the app found: the one inside the app bundle when it has
	// one, else one already installed, which then needs no link.
	exe, err := filepath.Abs(cli.Tuios)
	if err != nil {
		a.toast(err.Error())
		return
	}
	go func() {
		link := "/usr/local/bin/tuios"
		if runtime.GOOS != "darwin" {
			link = filepath.Join(proto.Home(), ".local/bin/tuios")
			os.MkdirAll(filepath.Dir(link), 0o755)
		}
		os.Remove(link)
		err := os.Symlink(exe, link)
		if err != nil && runtime.GOOS == "darwin" {
			// The paths go in as arguments, quoted by AppleScript itself,
			// so no app location can break the script.
			err = exec.Command("/usr/bin/osascript",
				"-e", "on run argv",
				"-e", `do shell script "mkdir -p /usr/local/bin && ln -sf " & quoted form of item 1 of argv & " " & quoted form of item 2 of argv with administrator privileges`,
				"-e", "end run",
				exe, link).Run()
		}
		a.update(func() {
			if err != nil {
				a.toast(locales.CliInstallFailed())
				return
			}
			a.toast(locales.CliInstalledIn(proto.ShortDir(filepath.Dir(link))))
		})
	}()
}

// cliInstalled returns where the kero command is installed, and what it
// runs, "" when it is nowhere.
func cliInstalled() string {
	for _, p := range cliLinks() {
		if t, err := os.Readlink(p); err == nil {
			return p + " → " + proto.ShortDir(t)
		}
	}
	return ""
}
