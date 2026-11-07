package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"tuios.dev/swatch"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/roles"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// guios takes its theme from tuios. A setting of `theme = tuios` (the
// default) colors guios with the theme the TUI has: the one config.toml
// names in appearance.theme (appearance.theme_light while the window is
// light), Omarchy's while tuios follows Omarchy, or the one `tuios
// set-config` gave the daemon since. Any other setting names one tuios
// theme by its id, as `tuios list-themes` prints it. Every role of the
// palette derives from the theme's terminal colors (ghosttyLook), so the
// theme reaches the sidebar, the chips, the menus and the agents view as
// well as the terminals (DESIGN.md section 1). Where tuios names no theme,
// guios shows its own (ownLook).
//
// The theme follows three things live, and none of them polls: a change
// of config.toml (one inotify watch on its folder), an option-set event of
// the daemon (on the event connection guios has open anyway), and a switch
// of the Omarchy theme (swatch's watch, one inotify watch). Each reads what
// it must off the main thread and redraws only when a color changed.

const tuiosThemeName = "tuios"

// tuiosSource is where the tuios theme comes from: config.toml's
// appearance.theme and theme_light ([0] and [1]), and the values
// set-option told the daemon after the file last changed them. The newer
// of the two wins, as the TUI applies whichever came last.
type tuiosSource struct {
	mu   sync.Mutex
	file [2]string
	told [2]*string
}

var tuiosSrc tuiosSource

// omarchyGen counts switches of the Omarchy theme guios saw: the omarchy
// id keeps its name while its colors change.
var omarchyGen atomic.Uint64

func init() {
	tuiosSrc.file[0], tuiosSrc.file[1] = readTuiosFile()
}

// readTuiosFile is appearance.theme and theme_light as config.toml has
// them, both empty when there is no config.toml. It never writes one.
func readTuiosFile() (string, string) {
	path, err := config.GetConfigPath()
	if err != nil {
		return "", ""
	}
	if _, err := os.Stat(path); err != nil {
		return "", ""
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return "", ""
	}
	return cfg.Appearance.Theme, cfg.Appearance.ThemeLight
}

// resolve is the id of the tuios theme for windows of an appearance, ""
// for none. The caller holds s.mu.
func (s *tuiosSource) resolve(dark bool) string {
	pick := func(i int) string {
		if s.told[i] != nil {
			return *s.told[i]
		}
		return s.file[i]
	}
	id := pick(0)
	if l := pick(1); !dark && l != "" {
		id = l
	}
	if theme.FollowsOmarchy(id) {
		id = theme.OmarchyID
	}
	if id == theme.NoneID {
		id = ""
	}
	return id
}

// tuiosThemeID is the tuios theme in force for windows of an appearance.
func tuiosThemeID(dark bool) string {
	tuiosSrc.mu.Lock()
	defer tuiosSrc.mu.Unlock()
	return tuiosSrc.resolve(dark)
}

// themeID is the tuios theme a guios setting shows: the TUI's for
// "tuios", else the one it names. A name tuios does not know (one of
// kero's or a Ghostty theme, as older settings have it) shows the TUI's.
func themeID(setting string, dark bool) string {
	if setting != tuiosThemeName && tuiosThemeExists(setting) {
		return setting
	}
	return tuiosThemeID(dark)
}

func tuiosThemeExists(id string) bool {
	if id == "" {
		return false
	}
	theme.EnsureRegistry()
	return theme.Exists(id)
}

// themeKey is what the looks are built from: the tuios themes the
// settings show, and the Omarchy theme's switches while one is Omarchy's.
func (a *App) themeKey() string {
	ids := [2]string{themeID(a.set.LightTheme, false), themeID(a.set.DarkTheme, true)}
	key := ids[0] + "\x00" + ids[1]
	if ids[0] == theme.OmarchyID || ids[1] == theme.OmarchyID {
		key += "\x00" + itoa(int(omarchyGen.Load()))
	}
	return key
}

// tuiosLook is the look of a tuios theme, nil for none or one guios
// cannot read.
func tuiosLook(id string) *look {
	th := tuiosTermTheme(id)
	if th == nil {
		return nil
	}
	return ghosttyLook(th)
}

// tuiosTermTheme is a tuios theme as a terminal theme, nil for none.
func tuiosTermTheme(id string) *terminal.Theme {
	th, ok := roles.FromTheme(id)
	if !ok {
		return nil
	}
	return toTerm(th)
}

// previewLooks are the looks Settings previews, by theme id, built once
// for each until a theme changes its colors.
var previewLooks struct {
	sync.Mutex
	gen   uint64
	looks map[string]*look
	// terms are the menu's swatches, by theme id.
	termGen uint64
	terms   map[string]*terminal.Theme
}

// swatchTheme is a tuios theme as a terminal theme, kept for the menu's
// swatches, which draw every theme while the menu is open.
func swatchTheme(id string) *terminal.Theme {
	gen := omarchyGen.Load()
	previewLooks.Lock()
	defer previewLooks.Unlock()
	if previewLooks.terms == nil || previewLooks.termGen != gen {
		previewLooks.terms, previewLooks.termGen = map[string]*terminal.Theme{}, gen
	}
	th, ok := previewLooks.terms[id]
	if !ok {
		th = tuiosTermTheme(id)
		previewLooks.terms[id] = th
	}
	return th
}

// previewLook is the look of a tuios theme for a preview, guios's own for
// none.
func previewLook(id string, dark bool) *look {
	gen := omarchyGen.Load()
	previewLooks.Lock()
	defer previewLooks.Unlock()
	if previewLooks.looks == nil || previewLooks.gen != gen {
		previewLooks.looks, previewLooks.gen = map[string]*look{}, gen
	}
	key := id
	if id == "" {
		key = "\x00" + string(rune('0'+b2i(dark)))
	}
	if l, ok := previewLooks.looks[key]; ok {
		return l
	}
	l := tuiosLook(id)
	if l == nil {
		l = ownLook(dark)
	}
	previewLooks.looks[key] = l
	return l
}

// tuiosOptionSet takes a theme option that set-option gave the daemon.
func (a *App) tuiosOptionSet(key, value string) {
	i := 0
	if key == "appearance.theme_light" {
		i = 1
	}
	tuiosSrc.mu.Lock()
	v := value
	tuiosSrc.told[i] = &v
	tuiosSrc.mu.Unlock()
	a.retheme()
}

// shareTheme makes id the tuios theme of windows of an appearance:
// appearance.theme for dark ones, theme_light for light ones, as the
// TUI's picker writes a pair. It goes to the daemon as tuios set-config
// does, so a TUI on the session takes it and saves it to config.toml.
// With no TUI there, guios saves it to config.toml itself, as the TUI's
// settings page would, and the TUI that attaches next reads it there.
// guios follows the change as it follows any (option-set and the file).
func (a *App) shareTheme(id string, dark bool) {
	key := client.ThemeOptions[0]
	if !dark {
		key = client.ThemeOptions[1]
	}
	cl := a.local().cl
	go func() {
		applied := false
		if cl != nil {
			applied, _ = cl.SetThemeOption(key, id)
		}
		if applied {
			return
		}
		if err := saveTuiosTheme(key, id); err != nil {
			a.update(func() { a.toast(locales.ToastThemeNotSaved(err.Error())) })
		}
	}()
}

// saveTuiosTheme writes one theme option to config.toml, only that key,
// as the TUI's settings page saves.
func saveTuiosTheme(key, value string) error {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return err
	}
	if err := config.SetOptionValue(cfg, key, value); err != nil {
		return err
	}
	_, err = config.SaveUserConfig(cfg)
	return err
}

// followTuiosTheme watches config.toml's folder, and the applied Omarchy
// theme on a machine with Omarchy, and recolors the app as they change.
// Each watch is one inotify watch and a goroutine blocked on it: no timer
// runs, and an event that changes no theme draws nothing.
func (a *App) followTuiosTheme() {
	if path, err := config.GetConfigPath(); err == nil {
		go func() {
			w, err := mygo.WatchFiles(context.Background(), filepath.Dir(path), mygo.FileWatchOptions{HintsOnly: true})
			if err != nil {
				return
			}
			defer w.Close()
			for {
				if _, err := w.Next(context.Background()); err != nil {
					return
				}
				th, light := readTuiosFile()
				tuiosSrc.mu.Lock()
				moved := false
				for i, v := range [2]string{th, light} {
					if v != tuiosSrc.file[i] {
						// The file's newer value wins over set-option's.
						tuiosSrc.file[i], tuiosSrc.told[i], moved = v, nil, true
					}
				}
				tuiosSrc.mu.Unlock()
				if moved {
					a.update(a.retheme)
				}
			}
		}()
	}
	if theme.OmarchyInstalled() {
		if w, err := swatch.Watch(theme.OmarchySource()); err == nil {
			go func() {
				for p := range w.Changes() {
					if theme.SetOmarchyPalette(p) {
						omarchyGen.Add(1)
						a.update(a.retheme)
					}
				}
			}()
		}
	}
}

// retheme gives every terminal the window's terminal themes again, and
// draws the windows anew, when the themes the settings show changed
// colors without a setting changing. It runs on the main thread, and does
// nothing when the looks stay the same.
func (a *App) retheme() {
	old := currentLooks.Load()
	if l := a.looks(); l == old {
		return
	}
	light, dark := a.termThemes()
	for _, e := range a.terms {
		if e.term != nil {
			e.term.SetTheme(light, dark)
		}
	}
	if t := a.quickTermFor(a.quick.pane); t != nil {
		t.SetTheme(light, dark)
	}
	for _, w := range []*mygo.Window{a.win, a.setWin, a.quick.win} {
		if w != nil {
			w.Invalidate()
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
