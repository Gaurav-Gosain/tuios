// Package procsetup sets the process globals a tuios client reads: where the
// plugin themes, screen savers and sketch program are, how the daemon reaps a
// plugin command, how a machine installs a release, and the input handler.
//
// cmd/tuios calls Setup before it runs a command. A test that runs a client
// in its own process calls it too, so the client it runs has the same
// globals as the binary's (testing.md, blocker B8).
package procsetup

import (
	"image/color"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/mermaid"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Options are the parts of the setup that only the tuios binary has.
type Options struct {
	// Reaper is the argv the daemon runs a plugin command under. Nil runs
	// none. See Reaper and session.PluginReaper.
	Reaper []string
	// Releases installs the release of this version on a machine. Nil
	// leaves app.MachineReleases unset.
	Releases hostsync.Releases
}

// Setup sets the process globals. It only stores values: the plugin files
// are read when a theme, a saver or a diagram asks for them.
func Setup(o Options) {
	session.PluginReaper = o.Reaper
	theme.ExtraFiles = ThemeFiles
	app.PluginSavers = SaverFiles
	// The add sheet installs the release of this version with the helpers
	// tuios update and hosts sync use.
	if o.Releases != nil {
		app.MachineReleases = o.Releases
	}
	app.SketchProgram = func() string {
		prog, e, err := FindSketch()
		if err != nil || e == nil || !e.Runnable() {
			return ""
		}
		return prog
	}
	app.MermaidPicture = drawMermaidPicture
	app.SetInputHandler(input.HandleInput)
}

// drawMermaidPicture draws a Mermaid block of the agents view as a picture,
// in the colours of the view, clear where the diagram draws nothing.
func drawMermaidPicture(src string, cols, rows, cellW, cellH int, pal overlay.Palette, ansi [16]color.Color, bg color.Color) ([]byte, int, int, error) {
	th := mermaid.Theme(pal, ansi, bg, pal.Fg)
	pic, err := mermaid.Draw(src, th, mermaid.PictureOptions{Cols: cols, Rows: rows, CellW: cellW, CellH: cellH})
	return pic.PNG, pic.Cols, pic.Rows, err
}

// Reaper is the argv the daemon runs a plugin command under, or nil where no
// native plugin runs. On Linux it names /proc/self/exe, which still runs this
// build after an update replaced the file. It names the running executable,
// so only the tuios binary passes it to Setup.
func Reaper() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{"/proc/self/exe", herdrplugin.ReaperArg}
	case "darwin":
		if exe, err := os.Executable(); err == nil {
			return []string{exe, herdrplugin.ReaperArg}
		}
	}
	return nil
}

// Plugins lists the plugins as config.toml says, and the path of that file.
func Plugins() ([]herdrplugin.Entry, string, error) {
	path, err := config.GetConfigPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.PluginsInFile(path)
	if err != nil {
		return nil, path, err
	}
	return herdrplugin.Discover(herdrplugin.DefaultDirs(path, cfg.Dirs), cfg.Enabled), path, nil
}

// ThemeFiles lists the theme files of the enabled plugins that may run, for
// internal/theme. It reads the plugin files each time the themes load.
func ThemeFiles() []string {
	entries, _, err := Plugins()
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Runnable() {
			out = append(out, e.Plugin.Themes...)
		}
	}
	return out
}

// SaverFiles maps each screen saver of the enabled plugins that may run, by
// its name plugin/ID/SAVER, to its frames file, for internal/app.
func SaverFiles() map[string]string {
	entries, _, err := Plugins()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		if !e.Runnable() {
			continue
		}
		for _, s := range e.Plugin.Savers {
			out[config.ScreensaverPluginPrefix+e.ID+"/"+s.ID] = s.File
		}
	}
	return out
}

// FindSketch looks up the sketch plugin. e is nil when it is not installed.
// prog is its program when it is installed and runnable, and err is set when
// the plugins cannot be listed or the program is not there.
func FindSketch() (prog string, e *herdrplugin.Entry, err error) {
	entries, _, err := Plugins()
	if err != nil {
		return "", nil, err
	}
	e = herdrplugin.Find(entries, herdrplugin.SketchID)
	if e == nil || e.Plugin == nil {
		return "", nil, nil
	}
	if !e.Runnable() {
		return "", e, nil
	}
	prog = filepath.Join(e.Plugin.PluginRoot, "bin", release.ExecutableName("sketch"))
	if _, err := os.Stat(prog); err != nil {
		return prog, e, err
	}
	return prog, e, nil
}
