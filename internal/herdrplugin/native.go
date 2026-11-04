package herdrplugin

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// The native manifest, tuios-plugin.toml.
//
// A native plugin is a herdr plugin with a schema of tuios's own: the same
// build, startup, action and pane entries, plus the pane grants its
// processes hold when they call tuios. It is read into herdr's shape and
// checked by the same code, so the runner, the palette and the install
// prompt treat both formats alike. Only the grants differ: a herdr plugin's
// processes hold the default grants, and a native plugin's hold the grants
// the person approved at install (Plugin.Held), or none.
//
// Unknown keys are refused, so a typo is not a silent no-op, and an older
// tuios never ignores a key or a grant a newer one added. Entries a later
// tuios runs, [[wasm]] and event processes, are refused with a message that
// says so. docs/PLUGINS.md is the contract.

// NativeManifestName is the file name of a native plugin manifest. In a
// folder with both files, it wins.
const NativeManifestName = "tuios-plugin.toml"

// FormatTuios marks a plugin read from a native manifest.
const FormatTuios = "tuios"

// kernelPlacesCallers is true where the daemon reads a caller's pid from
// the kernel (procinfo.PeerPID), so it can hold a native plugin's processes
// to their grants. Elsewhere the runner does not start them.
const kernelPlacesCallers = runtime.GOOS == "linux" || runtime.GOOS == "darwin"

// nativeAPI is the plugin API version this build reads, the api key of a
// manifest.
const nativeAPI = 1

// RunningVersion is the version of this tuios, which the binary sets. A
// manifest whose min_tuios is newer is refused. A value that is not a release
// version, such as "dev", checks nothing.
var RunningVersion string

// orUpdate ends a refusal that a newer tuios may not make.
const orUpdate = "Check the spelling. If the plugin needs a newer tuios, run tuios update"

// PluginGrants are the grants a native manifest may ask for. respond is not
// one: a plugin never answers a prompt for the person. files is for a later
// tuios, since a copy is held to the folder of a pane and a plugin has none.
var PluginGrants = []string{"read", "write", "fan", "ui", "admin"}

type rawNative struct {
	API         *int      `toml:"api"`
	Schema      *int      `toml:"schema"` // the old name of api, shipped in v0.12
	ID          *string   `toml:"id"`
	Name        *string   `toml:"name"`
	Version     *string   `toml:"version"`
	Description *string   `toml:"description"`
	MinTuios    *string   `toml:"min_tuios"`
	Platforms   *[]string `toml:"platforms"`
	Permissions struct {
		Grants []string `toml:"grants"`
	} `toml:"permissions"`
	Build   []rawCommand `toml:"build"`
	Process []rawProcess `toml:"process"`
	Cell    []rawCell    `toml:"cell"`
	Theme   []rawTheme   `toml:"theme"`
	Saver   []rawSaver   `toml:"saver"`
	Wasm    []any        `toml:"wasm"`
	Herdr   *struct {
		Manifest string `toml:"manifest"`
	} `toml:"herdr"`
}

// rawSaver is one [[saver]]: a screen saver as a frames file.
type rawSaver struct {
	ID   *string `toml:"id"`
	File *string `toml:"file"`
}

// parseSavers checks the [[saver]] entries.
func parseSavers(raw []rawSaver, root string) ([]Saver, *Error) {
	var out []Saver
	for _, s := range raw {
		if s.ID == nil {
			return nil, missingField("id")
		}
		id, ok := NormalizeLocalID(*s.ID)
		if !ok {
			return nil, errf("plugin_manifest_parse_failed", "invalid [[saver]] id %q", *s.ID)
		}
		if s.File == nil || strings.TrimSpace(*s.File) == "" {
			return nil, missingField("file")
		}
		rel := filepath.FromSlash(strings.TrimSpace(*s.File))
		if !filepath.IsLocal(rel) || !confined(filepath.Join(root, rel), root) {
			return nil, errf("plugin_manifest_parse_failed", "[[saver]] file must be in the plugin folder, not %q", *s.File)
		}
		if slices.ContainsFunc(out, func(x Saver) bool { return x.ID == id }) {
			return nil, errf("plugin_manifest_parse_failed", "two [[saver]] entries have the id %s", id)
		}
		out = append(out, Saver{ID: id, File: filepath.Join(root, rel)})
	}
	return out, nil
}

// confined reports whether path, a file a manifest names in root, stays in
// root once its links are resolved. IsLocal alone passes a link that git
// checked out, such as theme.json -> /dev/zero or -> ~/.ssh/id_ed25519.
// A file that is not there is confined: reading it fails, and tuios plugins
// check says it is missing. A link that does not resolve is not.
func confined(path, root string) bool {
	if _, err := os.Lstat(path); err != nil {
		return true
	}
	if _, err := filepath.EvalSymlinks(path); err != nil {
		return false
	}
	return Inside(path, root)
}

// rawTheme is one [[theme]]: a theme file the plugin brings.
type rawTheme struct {
	File *string `toml:"file"`
}

// parseThemes checks the [[theme]] entries: each a .json file in the plugin
// folder.
func parseThemes(raw []rawTheme, root string) ([]string, *Error) {
	var out []string
	for _, t := range raw {
		if t.File == nil || strings.TrimSpace(*t.File) == "" {
			return nil, missingField("file")
		}
		rel := filepath.FromSlash(strings.TrimSpace(*t.File))
		path := filepath.Join(root, rel)
		if !filepath.IsLocal(rel) || !strings.EqualFold(filepath.Ext(rel), ".json") || !confined(path, root) {
			return nil, errf("plugin_manifest_parse_failed", "[[theme]] file must be a .json file in the plugin folder, not %q", *t.File)
		}
		out = append(out, path)
	}
	return out, nil
}

// rawCell is one [[cell]]: a dock cell the plugin may set.
type rawCell struct {
	ID   *string `toml:"id"`
	Side *string `toml:"side"`
}

// maxCells bounds the cells of one plugin.
const maxCells = 8

// Sides of a cell.
var cellSides = []string{"left", "center", "right"}

// parseCells checks the [[cell]] entries.
func parseCells(raw []rawCell, grants []string) ([]Cell, []string, *Error) {
	if len(raw) > maxCells {
		return nil, nil, errf("plugin_manifest_parse_failed", "the manifest has %d [[cell]] entries. A plugin can have %d", len(raw), maxCells)
	}
	var out []Cell
	for _, c := range raw {
		if c.ID == nil {
			return nil, nil, missingField("id")
		}
		id, ok := NormalizeLocalID(*c.ID)
		if !ok {
			return nil, nil, errf("plugin_manifest_parse_failed", "invalid [[cell]] id %q", *c.ID)
		}
		side := "right"
		if c.Side != nil {
			side = *c.Side
		}
		if !slices.Contains(cellSides, side) {
			return nil, nil, errf("plugin_manifest_parse_failed", "unknown side %q. Use left, center or right. %s", side, orUpdate)
		}
		if slices.ContainsFunc(out, func(x Cell) bool { return x.ID == id }) {
			return nil, nil, errf("plugin_manifest_parse_failed", "two [[cell]] entries have the id %s", id)
		}
		out = append(out, Cell{ID: id, Side: side})
	}
	var warn []string
	if len(out) > 0 && !slices.Contains(grants, "ui") && !slices.Contains(grants, "admin") {
		warn = []string{"[[cell]] needs the ui grant. Add ui to [permissions] grants"}
	}
	return out, warn, nil
}

// rawProcess is one [[process]]: an action, a pane or a startup command, by
// its run key.
type rawProcess struct {
	ID          *string   `toml:"id"`
	Run         *string   `toml:"run"`
	Title       *string   `toml:"title"`
	Description *string   `toml:"description"`
	Contexts    []string  `toml:"contexts"`
	Platforms   *[]string `toml:"platforms"`
	Placement   *string   `toml:"placement"`
	Width       any       `toml:"width"`
	Height      any       `toml:"height"`
	On          any       `toml:"on"`
	Command     *[]string `toml:"command"`
}

// parseNative validates a tuios-plugin.toml. root is its folder, where a
// [herdr] manifest is read from.
func parseNative(data []byte, root string) (*Plugin, *Error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var n rawNative
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&n); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) && len(strict.Errors) > 0 {
			return nil, errf("plugin_manifest_parse_failed", "unknown key %s in %s. %s", strings.Join(strict.Errors[0].Key(), "."), NativeManifestName, orUpdate)
		}
		return nil, errf("plugin_manifest_parse_failed", "%v", err)
	}
	var warnings []string
	if n.Schema != nil {
		if n.API != nil {
			return nil, errf("plugin_manifest_parse_failed", "the manifest has both api and schema. Remove schema")
		}
		n.API = n.Schema
		warnings = append(warnings, "schema is an old name. Use api = 1")
	}
	switch {
	case n.API == nil:
		return nil, missingField("api")
	case *n.API > nativeAPI:
		return nil, errf("plugin_requires_newer_tuios", "the manifest has api %d. This tuios reads api %d. Run tuios update", *n.API, nativeAPI)
	case *n.API < nativeAPI:
		return nil, errf("plugin_manifest_parse_failed", "api must be %d", nativeAPI)
	case len(n.Wasm) > 0:
		return nil, errf("plugin_requires_newer_tuios", "the plugin has [[wasm]] entries. This tuios does not run WASM plugins")
	}
	if n.MinTuios != nil {
		v := strings.TrimSpace(*n.MinTuios)
		want, ok := parseVersion(v)
		if !ok {
			return nil, errf("invalid_plugin_min_tuios", "min_tuios must be a version such as 0.9.0")
		}
		if have, ok := parseVersion(RunningVersion); ok && slices.Compare(have[:], want[:]) < 0 {
			return nil, errf("plugin_requires_newer_tuios", "the plugin needs tuios %s or later. This is tuios %s. Run tuios update", v, RunningVersion)
		}
	}
	grants, perr := nativeGrants(n.Permissions.Grants)
	if perr != nil {
		return nil, perr
	}
	var p *Plugin
	if n.Herdr != nil {
		if p, perr = herdrPart(n, root); perr != nil {
			return nil, perr
		}
	} else {
		host := HostVersion
		raw := rawManifest{ID: n.ID, Name: n.Name, Version: n.Version, MinHerdrVersion: &host, Description: n.Description, Platforms: n.Platforms, Build: n.Build}
		for _, x := range n.Process {
			if perr := addProcess(&raw, x); perr != nil {
				return nil, perr
			}
		}
		if p, perr = parseRaw(&raw); perr != nil {
			return nil, perr
		}
		p.MinHerdrVersion = ""
		p.Warnings = slices.DeleteFunc(p.Warnings, func(w string) bool { return w == noPlatformsWarning })
	}
	p.Format, p.Grants = FormatTuios, grants
	cells, warn, perr := parseCells(n.Cell, grants)
	if perr != nil {
		return nil, perr
	}
	p.Cells = cells
	if p.Themes, perr = parseThemes(n.Theme, root); perr != nil {
		return nil, perr
	}
	if p.Savers, perr = parseSavers(n.Saver, root); perr != nil {
		return nil, perr
	}
	p.Warnings = append(append(p.Warnings, warnings...), warn...)
	if n.MinTuios != nil {
		p.MinTuios = strings.TrimSpace(*n.MinTuios)
	}
	return p, nil
}

// herdrPart reads the herdr manifest a [herdr] table names. The native file
// then adds only identity and grants: it may not also list commands.
func herdrPart(n rawNative, root string) (*Plugin, *Error) {
	if len(n.Build) > 0 || len(n.Process) > 0 {
		return nil, errf("plugin_manifest_parse_failed", "with [herdr] manifest, the commands come from the herdr manifest. Remove [[build]] and [[process]] from %s", NativeManifestName)
	}
	path := filepath.Join(root, filepath.FromSlash(n.Herdr.Manifest))
	if n.Herdr.Manifest == "" || !Inside(path, root) {
		return nil, errf("plugin_manifest_parse_failed", "[herdr] manifest must name a file in the plugin folder")
	}
	data, err := os.ReadFile(path) //nolint:gosec // inside the plugin folder, checked above
	if err != nil {
		return nil, errf("plugin_manifest_read_failed", "%v", err)
	}
	p, perr := Parse(data)
	if perr != nil {
		return nil, perr
	}
	if n.ID != nil && strings.TrimSpace(*n.ID) != p.PluginID {
		return nil, errf("plugin_manifest_parse_failed", "%s names the plugin %s and the herdr manifest names it %s. The ids must match", NativeManifestName, strings.TrimSpace(*n.ID), p.PluginID)
	}
	return p, nil
}

// addProcess turns one [[process]] into the herdr entry its run key names.
func addProcess(raw *rawManifest, x rawProcess) *Error {
	if x.Run == nil {
		return missingField("run")
	}
	switch *x.Run {
	case "startup":
		raw.Startup = append(raw.Startup, rawCommand{Platforms: x.Platforms, Command: x.Command})
	case "action":
		raw.Actions = append(raw.Actions, rawAction{ID: x.ID, Title: x.Title, Description: x.Description, Contexts: x.Contexts, Platforms: x.Platforms, Command: x.Command})
	case "pane":
		raw.Panes = append(raw.Panes, rawPane{ID: x.ID, Title: x.Title, Description: x.Description, Platforms: x.Platforms, Placement: x.Placement, Width: x.Width, Height: x.Height, Command: x.Command})
	case "event":
		names, perr := eventNames(x.On)
		if perr != nil {
			return perr
		}
		for _, on := range names {
			raw.Events = append(raw.Events, rawEvent{On: &on, Platforms: x.Platforms, Command: x.Command})
		}
	default:
		return errf("plugin_manifest_parse_failed", "unknown run %q. Use startup, action, pane or event. %s", *x.Run, orUpdate)
	}
	return nil
}

// eventNames reads the on key of an event process: one name or a list, each
// in HookEvents. An unknown name is refused, so a typo is not a hook that
// never runs.
func eventNames(on any) ([]string, *Error) {
	var names []string
	switch v := on.(type) {
	case string:
		names = []string{v}
	case []any:
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, errf("plugin_manifest_parse_failed", "on must be event names, such as [\"pane.focused\"]")
			}
			names = append(names, s)
		}
	}
	if len(names) == 0 {
		return nil, errf("plugin_manifest_parse_failed", "an event process needs on, such as on = [\"pane.focused\"]")
	}
	for _, n := range names {
		if !slices.Contains(HookEvents, n) {
			return nil, errf("plugin_manifest_parse_failed", "unknown event %q. The events are %s. %s", n, strings.Join(HookEvents, ", "), orUpdate)
		}
	}
	return names, nil
}

// nativeGrants checks and sorts the grants a manifest asks for.
func nativeGrants(names []string) ([]string, *Error) {
	out := []string{}
	for _, g := range names {
		switch {
		case g == "respond":
			return nil, errf("invalid_plugin_grant", "a plugin cannot hold respond. Only the person answers a prompt")
		case !slices.Contains(PluginGrants, g):
			return nil, errf("invalid_plugin_grant", "unknown grant %q. A plugin can ask for %s. %s", g, strings.Join(PluginGrants, ", "), orUpdate)
		case !slices.Contains(out, g):
			out = append(out, g)
		}
	}
	slices.Sort(out)
	return out, nil
}

// HasManifest reports whether dir holds a plugin manifest of either format.
func HasManifest(dir string) bool {
	return fileExists(filepath.Join(dir, NativeManifestName)) || fileExists(filepath.Join(dir, ManifestName))
}
