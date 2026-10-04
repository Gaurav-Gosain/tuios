package herdrplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// Where a plugin was found.
const (
	// OriginConfig is a folder named in [plugins] dirs of config.toml, which
	// tuios plugins link adds to.
	OriginConfig = "config"
	// OriginTuios is a folder under the tuios plugins folder.
	OriginTuios = "tuios"
	// OriginHerdr is an entry of herdr's own registry, plugins.json.
	OriginHerdr = "herdr"
	// OriginHerdrManaged is a checkout herdr plugin install made.
	OriginHerdrManaged = "herdr-managed"
)

// Dirs are the places Discover reads and the folders a plugin's commands
// get for config and state.
type Dirs struct {
	// TuiosConfig is the folder of tuios's config.toml.
	TuiosConfig string
	// TuiosState is tuios's state folder.
	TuiosState string
	// HerdrConfig is herdr's config folder. Discover only reads it.
	HerdrConfig string
	// Extra are the folders and manifests [plugins] dirs names.
	Extra []string
}

// DefaultDirs works out the folders from the environment. configPath is
// tuios's config.toml.
func DefaultDirs(configPath string, extra []string) Dirs {
	return Dirs{
		TuiosConfig: filepath.Dir(configPath),
		TuiosState:  stateDir("tuios"),
		HerdrConfig: HerdrConfigDir(),
		Extra:       extra,
	}
}

// HerdrConfigDir is herdr's config folder, as herdr finds it:
// $XDG_CONFIG_HOME/herdr, else ~/.config/herdr (%APPDATA%\herdr on Windows).
func HerdrConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "herdr")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "herdr")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr")
}

func stateDir(app string) string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, app)
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, app)
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", app)
}

// PluginsDir is the tuios plugins folder: one plugin per folder in it.
func (d Dirs) PluginsDir() string { return filepath.Join(d.TuiosConfig, "plugins") }

// ConfigDir is a plugin's HERDR_PLUGIN_CONFIG_DIR. tuios keeps its own, so
// it never writes into herdr's folders.
func (d Dirs) ConfigDir(pluginID string) string {
	return filepath.Join(d.TuiosConfig, "plugin-config", PathComponent(pluginID))
}

// StateDir is a plugin's HERDR_PLUGIN_STATE_DIR.
func (d Dirs) StateDir(pluginID string) string {
	return filepath.Join(d.TuiosState, "plugins", PathComponent(pluginID))
}

// EnsureUserDirs makes the config and state folders of a plugin, as herdr
// does before it runs a command.
func (d Dirs) EnsureUserDirs(pluginID string) error {
	for _, dir := range []string{d.ConfigDir(pluginID), d.StateDir(pluginID)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// PathComponent is a plugin id made safe as one folder name, as herdr's
// plugin_config_path_component makes it: [a-z0-9._-] kept, every other byte
// %XX, so two ids never share a folder.
func PathComponent(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	s := b.String()
	if strings.HasSuffix(s, ".") {
		s = strings.TrimSuffix(s, ".") + "%2E"
	}
	switch {
	case s == "":
		return "%plugin"
	case s == "." || s == "..":
		return "%" + s
	}
	return s
}

// Entry is one plugin Discover found. Plugin is nil when the manifest did
// not load, and Err says why.
type Entry struct {
	ID      string  `json:"plugin_id"`
	Origin  string  `json:"origin"`
	Path    string  `json:"manifest_path"`
	Enabled bool    `json:"enabled"`
	Plugin  *Plugin `json:"plugin,omitempty"`
	Err     *Error  `json:"error,omitempty"`
}

// Runnable reports whether the entry loaded and is enabled.
func (e Entry) Runnable() bool { return e.Plugin != nil && e.Enabled && e.Err == nil }

// registryEntry is the part of herdr's plugins.json Discover reads.
type registryEntry struct {
	PluginID     string `json:"plugin_id"`
	ManifestPath string `json:"manifest_path"`
	Source       Source `json:"source"`
}

// Discover lists every plugin on this machine, sorted by id. enabled is the
// list of ids config.toml enables. herdr's own enabled flag is not read: a
// plugin herdr runs is off in tuios until the person enables it here.
//
// A plugin id found in two places is listed once, from the first place in
// this order: [plugins] dirs, the tuios plugins folder, herdr's registry,
// herdr's managed checkouts. The later copy is listed with the error
// plugin_id_shadowed and never runs.
//
// Discover reads files only. It runs no plugin command.
func Discover(d Dirs, enabled []string) []Entry {
	var out []Entry
	seen := map[string]bool{}
	byID := map[string]bool{}
	add := func(origin, path string, src *Source) {
		e := Entry{Origin: origin, Path: path}
		p, perr := Load(path)
		if p != nil {
			e.Path = p.ManifestPath
		}
		key := e.Path
		if seen[key] {
			return
		}
		seen[key] = true
		switch {
		case perr != nil:
			e.Err = perr
			e.ID = guessID(path)
		default:
			if src != nil && src.Kind != "" {
				p.Source = *src
			}
			e.ID, e.Plugin = p.PluginID, p
			if byID[p.PluginID] {
				e.Err = errf("plugin_id_shadowed", "another plugin with id %s was found first. This copy does not run", p.PluginID)
			}
			byID[p.PluginID] = true
		}
		e.Enabled = slices.Contains(enabled, e.ID)
		if e.Plugin != nil {
			e.Plugin.Enabled = e.Enabled && e.Err == nil
		}
		out = append(out, e)
	}
	for _, p := range d.Extra {
		add(OriginConfig, expandHome(p), nil)
	}
	for _, dir := range subdirs(d.PluginsDir()) {
		if fileExists(filepath.Join(dir, ManifestName)) {
			add(OriginTuios, dir, nil)
		}
	}
	if d.HerdrConfig != "" {
		var reg []registryEntry
		if data, err := os.ReadFile(filepath.Join(d.HerdrConfig, "plugins.json")); err == nil {
			_ = json.Unmarshal(data, &reg)
		}
		for _, r := range reg {
			if r.ManifestPath == "" {
				continue
			}
			src := r.Source
			add(OriginHerdr, r.ManifestPath, &src)
		}
		for _, dir := range subdirs(filepath.Join(d.HerdrConfig, "plugins", "github")) {
			if fileExists(filepath.Join(dir, ManifestName)) {
				add(OriginHerdrManaged, dir, &Source{Kind: "github", ManagedPath: dir})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Find is the entry for id that runs, the first found, or nil.
func Find(entries []Entry, id string) *Entry {
	for i := range entries {
		if entries[i].ID == id && (entries[i].Err == nil || entries[i].Err.Code != "plugin_id_shadowed") {
			return &entries[i]
		}
	}
	return nil
}

// guessID names an entry whose manifest did not load: its folder's name.
func guessID(path string) string {
	if strings.HasSuffix(path, ManifestName) {
		path = filepath.Dir(path)
	}
	return filepath.Base(path)
}

func subdirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// SamePath reports whether two plugin paths name the same file: each with
// ~ expanded, made absolute, and with its links resolved where it exists.
func SamePath(a, b string) bool {
	return canonical(a) == canonical(b)
}

func canonical(p string) string {
	p = expandHome(p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p
}
