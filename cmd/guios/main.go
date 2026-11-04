// kero is a terminal for coding agents: sessions grouped by project in a
// sidebar that shows which work, which need you and which are done. The
// sessions run in kerod, a daemon of their own, so quitting kero leaves
// them running, and opening it again shows everything as it was.
//
// The same executable is the kero CLI (kero help), which drives sessions
// from a shell or an agent, and kerod itself (kero __kerod).
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/clilink"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("guios", client.Version)
		return
	}
	// The sessions live in the tuios daemon, which this app starts with
	// the tuios binary when none runs (findTuios, ensureDaemon).
	tuios, err := findTuios(os.Getenv("GUIOS_TUIOS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "guios:", err)
		os.Exit(1)
	}
	cli.Tuios = tuios
	client.StartDaemon = func() error { return ensureDaemon(tuios) }
	// `kero` alone in a terminal, through the CLI's link to the app's
	// executable: open the app as the Finder would.
	if len(os.Args) == 1 && term.IsTerminal(int(os.Stdin.Fd())) {
		if exe, err := os.Executable(); err == nil {
			if real, err := filepath.EvalSymlinks(exe); err == nil {
				if i := strings.Index(real, ".app/Contents/MacOS/"); i >= 0 {
					if exec.Command("/usr/bin/open", real[:i+4]).Run() == nil {
						return
					}
				}
				// On Windows the command is a copy of the app's executable
				// (package clilink), which knows what it is a copy of.
				if bin := filepath.Dir(real); runtime.GOOS == "windows" && strings.EqualFold(bin, filepath.Join(proto.Dir(), "bin")) {
					if app := clilink.Target(bin); app != "" && exec.Command(app).Start() == nil {
						return
					}
				}
			}
		}
	}
	runApp()
}

func runApp() {
	servePprof()
	// Started from a pane, as Kero Dev by `mygo dev` in the installed Kero:
	// the pane's variables name its kerod, not this build's.
	if os.Getenv("TUIOS_PANE_ID") != "" {
		for _, k := range []string{"TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_WINDOW"} {
			os.Unsetenv(k)
		}
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	cli.SyncSkill()
	// The updater's menu and settings need the plugin. With no updates
	// in mygo.json it checks nothing: guios has no signed releases yet.
	mygo.Use(native.Plugin)
	a := newApp()
	loadFonts()
	mygo.Theme.SetSource(mygo.ThemeSource(a.set.Appearance))
	mygo.App.SetLocale(localeTag(a.set.Language))
	mygo.App.SetMenu(a.menu())
	// Windows draw anew in another language; the menu and the titles,
	// set once, are made again in it.
	mygo.App.OnLocaleChanged(func(string) {
		a.update(func() {
			mygo.App.SetMenu(a.menu())
			if a.setWin != nil {
				a.setWin.SetTitle(locales.SettingsTitle())
			}
			if a.tray != nil {
				a.tray.SetMenu(a.trayMenu())
			}
		})
	})
	mygo.App.OnSecondInstance(func([]string, string) { a.showWindow() })
	mygo.App.OnDidBecomeActive(func() {
		mygo.ClearNotifications()
		a.update(func() { a.active = true; a.reportView() })
	})
	mygo.App.OnDidResignActive(func() {
		a.update(func() { a.active = false; a.reportView() })
	})
	mygo.App.OnActivate(func(hasVisible bool) {
		if !hasVisible {
			a.showWindow()
		}
	})
	mygo.App.OnWindowAllClosed(a.lastWindowClosed)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.savePrefs()
		a.backupOnQuit()
	})
	// Windows leaves an icon in the notification area after its app until
	// the pointer passes over it.
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.tray != nil {
			a.tray.Destroy()
		}
	})
	// No Dock icon from the start for the menu bar alone.
	if runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(activationPolicy(a.set))
	}
	mygo.App.WhenReady(func() {
		a.openWindow()
		if onNewApp != nil {
			onNewApp(a)
		}
		a.applyTray()
		a.applyGlobalShortcuts()
		a.idleTime = mygo.Power.IdleTime
		go a.watchReturn()
		a.connect()
		a.syncHosts()
		go a.watchConfig()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// openWindow makes the main window: as Kero starts, and again after
// closing it left Kero running in the menu bar or the notification area.
func (a *App) openWindow() {
	a.win = mygo.NewWindow(framed(mygo.WindowOptions{
		Title:     "guios",
		Width:     1180,
		Height:    760,
		MinWidth:  560,
		MinHeight: 360,
		StateKey:  "main",
		Vibrancy:  vibrancyFor(a.set),
		Content:   ui.View(a.view),
	}))
	a.winOpen = true
	a.updateTitle()
	a.win.OnFocus(func() { a.update(func() { a.focused = true; a.claimShown(); a.reportView() }) })
	a.win.OnBlur(func() { a.update(func() { a.focused = false; a.reportView() }) })
	a.win.OnClosed(a.windowClosed)
}

// newSessionIn starts a session in a directory of a host, in its project.
func (a *App) newSessionIn(h *host, dir string) { a.create(h, proto.CreateParams{Dir: dir}, "") }
