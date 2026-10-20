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
// Unknown keys are refused, so a typo is not a silent no-op. Entries a later
// tuios runs, [[wasm]] and event processes, are refused with a message that
// says so.

// NativeManifestName is the file name of a native plugin manifest. In a
// folder with both files, it wins.
const NativeManifestName = "tuios-plugin.toml"

// FormatTuios marks a plugin read from a native manifest.
const FormatTuios = "tuios"

// kernelPlacesCallers is true where the daemon reads a caller's pid from
// the kernel (procinfo.PeerPID), so it can hold a native plugin's processes
// to their grants. Elsewhere the runner does not start them.
const kernelPlacesCallers = runtime.GOOS == "linux" || runtime.GOOS == "darwin"

// nativeSchema is the schema version this build reads.
const nativeSchema = 1

// PluginGrants are the grants a native manifest may ask for. respond is not
// one: a plugin never answers a prompt for the person. files is for a later
// tuios, since a copy is held to the folder of a pane and a plugin has none.
var PluginGrants = []string{"read", "write", "fan", "admin"}

type rawNative struct {
	Schema      *int      `toml:"schema"`
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
	Wasm    []any        `toml:"wasm"`
	Herdr   *struct {
		Manifest string `toml:"manifest"`
	} `toml:"herdr"`
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
			return nil, errf("plugin_manifest_parse_failed", "unknown key %s in %s", strings.Join(strict.Errors[0].Key(), "."), NativeManifestName)
		}
		return nil, errf("plugin_manifest_parse_failed", "%v", err)
	}
	switch {
	case n.Schema == nil:
		return nil, missingField("schema")
	case *n.Schema > nativeSchema:
		return nil, errf("plugin_requires_newer_tuios", "the manifest has schema %d. This tuios reads schema %d. Update tuios", *n.Schema, nativeSchema)
	case *n.Schema < nativeSchema:
		return nil, errf("plugin_manifest_parse_failed", "schema must be %d", nativeSchema)
	case len(n.Wasm) > 0:
		return nil, errf("plugin_requires_newer_tuios", "the plugin has [[wasm]] entries. This tuios does not run WASM plugins yet")
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
	}
	p.Format, p.Grants = FormatTuios, grants
	if n.MinTuios != nil {
		v := strings.TrimSpace(*n.MinTuios)
		if _, ok := parseVersion(v); !ok {
			return nil, errf("invalid_plugin_min_tuios", "min_tuios must be a version such as 0.9.0")
		}
		p.MinTuios = v
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
		return errf("plugin_requires_newer_tuios", "the plugin has an event process. This tuios does not run them yet")
	default:
		return errf("plugin_manifest_parse_failed", "unknown run %q, expected startup, action or pane", *x.Run)
	}
	return nil
}

// nativeGrants checks and sorts the grants a manifest asks for.
func nativeGrants(names []string) ([]string, *Error) {
	out := []string{}
	for _, g := range names {
		switch {
		case g == "respond":
			return nil, errf("invalid_plugin_grant", "a plugin cannot hold respond. Only the person answers a prompt")
		case !slices.Contains(PluginGrants, g):
			return nil, errf("invalid_plugin_grant", "unknown grant %q. A plugin can ask for %s", g, strings.Join(PluginGrants, ", "))
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
