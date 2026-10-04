package herdrplugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// HostVersion is the herdr release whose plugin format this package follows.
// A manifest whose min_herdr_version is newer is refused, as herdr refuses
// it.
const HostVersion = "0.9.3"

// ManifestName is the file name of a plugin manifest.
const ManifestName = "herdr-plugin.toml"

// Limits on ids, as herdr has them.
const (
	maxPluginIDChars = 120
	maxLocalIDChars  = 120
)

// Error is a failure in herdr's shape: a code a program reads and a message
// a person reads.
type Error struct {
	Code string `json:"code"`
	Msg  string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

func errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Placements a plugin pane can ask for.
const (
	PlacementOverlay = "overlay"
	PlacementPopup   = "popup"
	PlacementSplit   = "split"
	PlacementTab     = "tab"
	PlacementZoomed  = "zoomed"
)

// Placements is every placement herdr knows.
var Placements = []string{PlacementOverlay, PlacementPopup, PlacementSplit, PlacementTab, PlacementZoomed}

// Platforms herdr knows.
var platformNames = []string{"linux", "macos", "windows"}

// actionContexts are the values of an action's contexts list.
var actionContexts = []string{"global", "workspace", "tab", "pane", "selection"}

// HookEvents are the event names an [[events]] entry can name: herdr's
// PLUGIN_HOOK_EVENT_KINDS. The high-volume events (pane.output_changed,
// pane.updated, layout.updated, workspace.metadata_updated) are not among
// them, in herdr either.
var HookEvents = []string{
	"workspace.created", "workspace.updated", "workspace.closed", "workspace.renamed",
	"workspace.moved", "workspace.reordered", "workspace.focused",
	"worktree.created", "worktree.opened", "worktree.removed",
	"tab.created", "tab.closed", "tab.renamed", "tab.moved", "tab.focused",
	"pane.created", "pane.closed", "pane.focused", "pane.moved", "pane.exited",
	"pane.agent_detected", "pane.agent_status_changed",
}

// Size is a popup width or height: cells ("60") or a share of the screen
// ("60%"), the form tuios's popup verb takes. It reads and writes herdr's
// form: a number for cells, a string such as "80%" for a share.
type Size string

// UnmarshalJSON reads a number of cells or a percentage string.
func (s *Size) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	out, err := sizeOf(v)
	if err != nil {
		return err
	}
	*s = out
	return nil
}

// MarshalJSON writes cells as a number and a share as a string.
func (s Size) MarshalJSON() ([]byte, error) {
	if n, err := strconv.Atoi(string(s)); err == nil {
		return json.Marshal(n)
	}
	return json.Marshal(string(s))
}

// ParseSize reads a size as herdr's CLI does: "120" is cells, "80%" a share
// from 1% to 100%.
func ParseSize(v string) (Size, error) {
	if p, ok := strings.CutSuffix(v, "%"); ok {
		n, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return "", errors.New("must be a number of cells or a percentage like 80%")
		}
		if n < 1 || n > 100 {
			return "", errors.New("percentage must be between 1% and 100%")
		}
		return Size(strconv.FormatUint(n, 10) + "%"), nil
	}
	n, err := strconv.ParseUint(v, 10, 16)
	if err != nil {
		return "", errors.New("must be a number of cells or a percentage like 80%")
	}
	return Size(strconv.FormatUint(n, 10)), nil
}

// sizeOf reads a decoded TOML or JSON value as a size, as herdr's
// deserializer does: an integer is cells, a string must be a percentage.
func sizeOf(v any) (Size, error) {
	switch x := v.(type) {
	case int64:
		if x < 0 || x > 65535 {
			return "", errors.New("cells must fit in 0..65535")
		}
		return Size(strconv.FormatInt(x, 10)), nil
	case float64:
		if x != float64(int64(x)) || x < 0 || x > 65535 {
			return "", errors.New("cells must be a whole number in 0..65535")
		}
		return Size(strconv.FormatInt(int64(x), 10)), nil
	case string:
		if !strings.HasSuffix(x, "%") {
			return "", errors.New("string sizes must be percentages like 80%; use a number for cells")
		}
		return ParseSize(x)
	}
	return "", errors.New("a size is a number of cells or a percentage like 80%")
}

// Command is one manifest entry that runs an argv: [[build]] or [[startup]].
type Command struct {
	Platforms []string `json:"platforms,omitempty"`
	Command   []string `json:"command"`
}

// Action is one [[actions]] entry.
type Action struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Contexts    []string `json:"contexts,omitempty"`
	Platforms   []string `json:"platforms,omitempty"`
	Command     []string `json:"command"`
}

// EventHook is one [[events]] entry.
type EventHook struct {
	On        string   `json:"on"`
	Platforms []string `json:"platforms,omitempty"`
	Command   []string `json:"command"`
}

// Pane is one [[panes]] entry.
type Pane struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Platforms   []string `json:"platforms,omitempty"`
	Placement   string   `json:"placement"`
	Width       *Size    `json:"width,omitempty"`
	Height      *Size    `json:"height,omitempty"`
	Command     []string `json:"command"`
}

// LinkHandler is one [[link_handlers]] entry.
type LinkHandler struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Pattern   string   `json:"pattern"`
	Action    string   `json:"action"`
	Platforms []string `json:"platforms,omitempty"`
}

// Source is where a plugin came from, in herdr's PluginSourceInfo shape.
type Source struct {
	Kind           string `json:"kind"`
	Owner          string `json:"owner,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Subdir         string `json:"subdir,omitempty"`
	RequestedRef   string `json:"requested_ref,omitempty"`
	ResolvedCommit string `json:"resolved_commit,omitempty"`
	ManagedPath    string `json:"managed_path,omitempty"`
	InstalledMS    uint64 `json:"installed_unix_ms,omitempty"`
}

// Plugin is one loaded manifest, in herdr's InstalledPluginInfo shape, so a
// tool built for herdr reads tuios's plugin.list as it reads herdr's.
type Plugin struct {
	PluginID        string        `json:"plugin_id"`
	Name            string        `json:"name"`
	Version         string        `json:"version"`
	MinHerdrVersion string        `json:"min_herdr_version"`
	Description     string        `json:"description,omitempty"`
	ManifestPath    string        `json:"manifest_path"`
	PluginRoot      string        `json:"plugin_root"`
	Enabled         bool          `json:"enabled"`
	Platforms       []string      `json:"platforms,omitempty"`
	Build           []Command     `json:"build,omitempty"`
	Startup         []Command     `json:"startup,omitempty"`
	Actions         []Action      `json:"actions,omitempty"`
	Events          []EventHook   `json:"events,omitempty"`
	Panes           []Pane        `json:"panes,omitempty"`
	LinkHandlers    []LinkHandler `json:"link_handlers,omitempty"`
	Source          Source        `json:"source"`
	Warnings        []string      `json:"warnings,omitempty"`
}

// rawManifest is herdr-plugin.toml as written. Unknown keys are ignored, as
// herdr's serde types ignore them.
type rawManifest struct {
	ID              *string          `toml:"id"`
	Name            *string          `toml:"name"`
	Version         *string          `toml:"version"`
	MinHerdrVersion *string          `toml:"min_herdr_version"`
	Description     *string          `toml:"description"`
	Platforms       *[]string        `toml:"platforms"`
	Build           []rawCommand     `toml:"build"`
	Startup         []rawCommand     `toml:"startup"`
	Actions         []rawAction      `toml:"actions"`
	Events          []rawEvent       `toml:"events"`
	Panes           []rawPane        `toml:"panes"`
	LinkHandlers    []rawLinkHandler `toml:"link_handlers"`
}

type rawCommand struct {
	Platforms *[]string `toml:"platforms"`
	Command   *[]string `toml:"command"`
}

type rawAction struct {
	ID          *string   `toml:"id"`
	Title       *string   `toml:"title"`
	Description *string   `toml:"description"`
	Contexts    []string  `toml:"contexts"`
	Platforms   *[]string `toml:"platforms"`
	Command     *[]string `toml:"command"`
}

type rawEvent struct {
	On        *string   `toml:"on"`
	Platforms *[]string `toml:"platforms"`
	Command   *[]string `toml:"command"`
}

type rawPane struct {
	ID          *string   `toml:"id"`
	Title       *string   `toml:"title"`
	Description *string   `toml:"description"`
	Platforms   *[]string `toml:"platforms"`
	Placement   *string   `toml:"placement"`
	Width       any       `toml:"width"`
	Height      any       `toml:"height"`
	Command     *[]string `toml:"command"`
}

type rawLinkHandler struct {
	ID        *string   `toml:"id"`
	Title     *string   `toml:"title"`
	Pattern   *string   `toml:"pattern"`
	Action    *string   `toml:"action"`
	Platforms *[]string `toml:"platforms"`
}

// missingField is serde's message for a required field that is absent,
// which herdr reports as plugin_manifest_parse_failed.
func missingField(name string) *Error {
	return errf("plugin_manifest_parse_failed", "missing field `%s`", name)
}

// Load reads the manifest at path, a plugin folder or the manifest file, and
// validates it as herdr's load_plugin_manifest does: the same checks in the
// same order, with the same error codes. A valid manifest that names no
// platforms, or an event herdr cannot hook, loads with a warning.
func Load(path string) (*Plugin, *Error) {
	manifest := path
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		manifest = filepath.Join(path, ManifestName)
	}
	abs, err := filepath.Abs(manifest)
	if err != nil {
		return nil, errf("plugin_manifest_not_found", "%v", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		return nil, errf("plugin_manifest_not_found", "%v", err)
	}
	data, err := os.ReadFile(abs) //nolint:gosec // the path is a plugin the person listed
	if err != nil {
		return nil, errf("plugin_manifest_read_failed", "%v", err)
	}
	p, perr := Parse(data)
	if perr != nil {
		return nil, perr
	}
	p.ManifestPath = abs
	p.PluginRoot = filepath.Dir(abs)
	return p, nil
}

// Parse validates manifest bytes. The paths of the result are empty.
func Parse(data []byte) (*Plugin, *Error) {
	var raw rawManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, errf("plugin_manifest_parse_failed", "%v", err)
	}
	for _, f := range []struct {
		name string
		v    *string
	}{{"id", raw.ID}, {"name", raw.Name}, {"version", raw.Version}} {
		if f.v == nil {
			return nil, missingField(f.name)
		}
	}
	// Every command is required, so a missing one is a parse failure before
	// any field is checked, as serde reports it.
	if perr := checkRawCommands(&raw); perr != nil {
		return nil, perr
	}
	p := &Plugin{Source: Source{Kind: "local"}}
	var ok bool
	if p.PluginID, ok = normalizeID(*raw.ID, maxPluginIDChars, true); !ok {
		return nil, errf("invalid_plugin_id", "invalid plugin id")
	}
	var perr *Error
	if p.Name, perr = nonEmpty(*raw.Name, "invalid_plugin_name", "plugin name is required"); perr != nil {
		return nil, perr
	}
	if p.Version, perr = nonEmpty(*raw.Version, "invalid_plugin_version", "plugin version is required"); perr != nil {
		return nil, perr
	}
	if p.MinHerdrVersion, perr = checkMinVersion(raw.MinHerdrVersion); perr != nil {
		return nil, perr
	}
	if raw.Description != nil {
		p.Description = strings.TrimSpace(*raw.Description)
	}
	if p.Platforms, perr = platforms(raw.Platforms); perr != nil {
		return nil, perr
	}
	for _, b := range raw.Build {
		c, perr := command(b)
		if perr != nil {
			return nil, perr
		}
		p.Build = append(p.Build, c)
	}
	for _, s := range raw.Startup {
		c, perr := command(s)
		if perr != nil {
			return nil, perr
		}
		p.Startup = append(p.Startup, c)
	}
	for _, a := range raw.Actions {
		act, perr := action(a)
		if perr != nil {
			return nil, perr
		}
		p.Actions = append(p.Actions, act)
	}
	if id := duplicate(p.Actions, func(a Action) string { return a.ID }); id != "" {
		return nil, errf("duplicate_plugin_action_id", "duplicate action id '%s'", id)
	}
	slices.SortStableFunc(p.Actions, func(a, b Action) int { return strings.Compare(a.ID, b.ID) })
	for _, e := range raw.Events {
		ev, perr := event(e)
		if perr != nil {
			return nil, perr
		}
		p.Events = append(p.Events, ev)
	}
	slices.SortStableFunc(p.Events, func(a, b EventHook) int {
		if c := strings.Compare(a.On, b.On); c != 0 {
			return c
		}
		return slices.Compare(trimmedArgs(a.Command), trimmedArgs(b.Command))
	})
	for _, r := range raw.Panes {
		pane, perr := paneOf(r)
		if perr != nil {
			return nil, perr
		}
		p.Panes = append(p.Panes, pane)
	}
	if id := duplicate(p.Panes, func(x Pane) string { return x.ID }); id != "" {
		return nil, errf("duplicate_plugin_pane_id", "duplicate pane id '%s'", id)
	}
	slices.SortStableFunc(p.Panes, func(a, b Pane) int { return strings.Compare(a.ID, b.ID) })
	for _, r := range raw.LinkHandlers {
		h, perr := linkHandler(r)
		if perr != nil {
			return nil, perr
		}
		p.LinkHandlers = append(p.LinkHandlers, h)
	}
	if id := duplicate(p.LinkHandlers, func(h LinkHandler) string { return h.ID }); id != "" {
		return nil, errf("duplicate_plugin_link_handler_id", "duplicate link handler id '%s'", id)
	}
	for _, h := range p.LinkHandlers {
		if !slices.ContainsFunc(p.Actions, func(a Action) bool { return a.ID == h.Action }) {
			return nil, errf("invalid_plugin_link_handler_action", "link handler '%s' references unknown action '%s'", h.ID, h.Action)
		}
	}
	for _, e := range p.Events {
		if !slices.Contains(HookEvents, e.On) {
			p.Warnings = append(p.Warnings, fmt.Sprintf("unknown event '%s'", e.On))
		}
	}
	if p.Platforms == nil {
		p.Warnings = append(p.Warnings, "manifest does not declare platforms; platform support unknown")
	}
	return p, nil
}

// checkRawCommands reports the first entry with no command.
func checkRawCommands(raw *rawManifest) *Error {
	for _, c := range raw.Build {
		if c.Command == nil {
			return missingField("command")
		}
	}
	for _, c := range raw.Startup {
		if c.Command == nil {
			return missingField("command")
		}
	}
	for _, a := range raw.Actions {
		switch {
		case a.ID == nil:
			return missingField("id")
		case a.Title == nil:
			return missingField("title")
		case a.Command == nil:
			return missingField("command")
		}
		for _, c := range a.Contexts {
			if !slices.Contains(actionContexts, c) {
				return errf("plugin_manifest_parse_failed", "unknown variant `%s`, expected one of `global`, `workspace`, `tab`, `pane`, `selection`", c)
			}
		}
	}
	for _, e := range raw.Events {
		switch {
		case e.On == nil:
			return missingField("on")
		case e.Command == nil:
			return missingField("command")
		}
	}
	for _, p := range raw.Panes {
		switch {
		case p.ID == nil:
			return missingField("id")
		case p.Title == nil:
			return missingField("title")
		case p.Command == nil:
			return missingField("command")
		}
		if p.Placement != nil && !slices.Contains(Placements, *p.Placement) {
			return errf("plugin_manifest_parse_failed", "unknown variant `%s`, expected one of `overlay`, `popup`, `split`, `tab`, `zoomed`", *p.Placement)
		}
		for _, v := range []any{p.Width, p.Height} {
			if v == nil {
				continue
			}
			if _, err := sizeOf(v); err != nil {
				return errf("plugin_manifest_parse_failed", "%v", err)
			}
		}
	}
	for _, h := range raw.LinkHandlers {
		for _, f := range []struct {
			name string
			v    *string
		}{{"id", h.ID}, {"title", h.Title}, {"pattern", h.Pattern}, {"action", h.Action}} {
			if f.v == nil {
				return missingField(f.name)
			}
		}
	}
	return nil
}

func command(c rawCommand) (Command, *Error) {
	pl, perr := platforms(c.Platforms)
	if perr != nil {
		return Command{}, perr
	}
	argv, perr := argvOf(*c.Command)
	if perr != nil {
		return Command{}, perr
	}
	return Command{Platforms: pl, Command: argv}, nil
}

func action(a rawAction) (Action, *Error) {
	id, ok := normalizeID(*a.ID, maxLocalIDChars, false)
	if !ok {
		return Action{}, errf("invalid_plugin_action_id", "invalid action id")
	}
	title, perr := nonEmpty(*a.Title, "invalid_plugin_action_title", "action title is required")
	if perr != nil {
		return Action{}, perr
	}
	pl, perr := platforms(a.Platforms)
	if perr != nil {
		return Action{}, perr
	}
	argv, perr := argvOf(*a.Command)
	if perr != nil {
		return Action{}, perr
	}
	return Action{ID: id, Title: title, Description: trimmed(a.Description), Contexts: a.Contexts, Platforms: pl, Command: argv}, nil
}

func event(e rawEvent) (EventHook, *Error) {
	on, perr := nonEmpty(*e.On, "invalid_plugin_event", "event name is required")
	if perr != nil {
		return EventHook{}, perr
	}
	pl, perr := platforms(e.Platforms)
	if perr != nil {
		return EventHook{}, perr
	}
	argv, perr := argvOf(*e.Command)
	if perr != nil {
		return EventHook{}, perr
	}
	return EventHook{On: on, Platforms: pl, Command: argv}, nil
}

func paneOf(r rawPane) (Pane, *Error) {
	id, ok := normalizeID(*r.ID, maxLocalIDChars, false)
	if !ok {
		return Pane{}, errf("invalid_plugin_pane_id", "invalid pane id")
	}
	title, perr := nonEmpty(*r.Title, "invalid_plugin_pane_title", "pane title is required")
	if perr != nil {
		return Pane{}, perr
	}
	pl, perr := platforms(r.Platforms)
	if perr != nil {
		return Pane{}, perr
	}
	argv, perr := argvOf(*r.Command)
	if perr != nil {
		return Pane{}, perr
	}
	p := Pane{ID: id, Title: title, Description: trimmed(r.Description), Platforms: pl, Placement: PlacementOverlay, Command: argv}
	if r.Placement != nil {
		p.Placement = *r.Placement
	}
	if r.Width != nil {
		s, _ := sizeOf(r.Width)
		p.Width = &s
	}
	if r.Height != nil {
		s, _ := sizeOf(r.Height)
		p.Height = &s
	}
	if p.Placement != PlacementPopup && (p.Width != nil || p.Height != nil) {
		return Pane{}, errf("invalid_plugin_pane_size", "pane width and height are only supported when placement is popup")
	}
	return p, nil
}

func linkHandler(r rawLinkHandler) (LinkHandler, *Error) {
	id, ok := normalizeID(*r.ID, maxLocalIDChars, false)
	if !ok {
		return LinkHandler{}, errf("invalid_plugin_link_handler_id", "invalid link handler id")
	}
	title, perr := nonEmpty(*r.Title, "invalid_plugin_link_handler_title", "link handler title is required")
	if perr != nil {
		return LinkHandler{}, perr
	}
	pattern, perr := nonEmpty(*r.Pattern, "invalid_plugin_link_handler_pattern", "link handler pattern is required")
	if perr != nil {
		return LinkHandler{}, perr
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return LinkHandler{}, errf("invalid_plugin_link_handler_pattern", "%v", err)
	}
	act, ok := normalizeID(*r.Action, maxLocalIDChars, false)
	if !ok {
		return LinkHandler{}, errf("invalid_plugin_link_handler_action", "invalid link handler action")
	}
	pl, perr := platforms(r.Platforms)
	if perr != nil {
		return LinkHandler{}, perr
	}
	return LinkHandler{ID: id, Title: title, Pattern: pattern, Action: act, Platforms: pl}, nil
}

// checkMinVersion is herdr's validate_min_herdr_version against HostVersion.
func checkMinVersion(v *string) (string, *Error) {
	if v == nil {
		return "", errf("invalid_plugin_min_herdr_version", "plugin min_herdr_version is required")
	}
	s, perr := nonEmpty(*v, "invalid_plugin_min_herdr_version", "plugin min_herdr_version is required")
	if perr != nil {
		return "", perr
	}
	want, ok := parseVersion(s)
	if !ok {
		return "", errf("invalid_plugin_min_herdr_version", "plugin min_herdr_version must be a semantic version like %s", HostVersion)
	}
	have, _ := parseVersion(HostVersion)
	if slices.Compare(want[:], have[:]) > 0 {
		return "", errf("plugin_requires_newer_herdr", "plugin requires Herdr %d.%d.%d or newer; tuios follows Herdr %s", want[0], want[1], want[2], HostVersion)
	}
	return fmt.Sprintf("%d.%d.%d", want[0], want[1], want[2]), nil
}

// parseVersion reads major.minor.patch with an optional leading v, as
// herdr's Version::parse does. Nothing else is accepted.
func parseVersion(s string) ([3]uint64, bool) {
	var out [3]uint64
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// platforms reads a platforms list. Absent is nil: every platform. Present
// and empty is refused, as herdr refuses it.
func platforms(raw *[]string) ([]string, *Error) {
	if raw == nil {
		return nil, nil
	}
	if len(*raw) == 0 {
		return nil, errf("invalid_plugin_platform", "platforms must not be an empty array; omit the field to leave platforms undeclared")
	}
	for _, p := range *raw {
		if !slices.Contains(platformNames, p) {
			return nil, errf("plugin_manifest_parse_failed", "invalid_plugin_platform: unknown platform '%s'", p)
		}
	}
	return slices.Clone(*raw), nil
}

func argvOf(argv []string) ([]string, *Error) {
	if len(argv) == 0 || slices.Contains(argv, "") {
		return nil, errf("invalid_plugin_command", "command must contain non-empty argv strings")
	}
	return slices.Clone(argv), nil
}

func nonEmpty(v, code, msg string) (string, *Error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", errf(code, "%s", msg)
	}
	return v, nil
}

func trimmed(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func trimmedArgs(argv []string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = strings.TrimSpace(a)
	}
	return out
}

func duplicate[T any](items []T, id func(T) string) string {
	seen := map[string]bool{}
	for _, it := range items {
		k := id(it)
		if seen[k] {
			return k
		}
		seen[k] = true
	}
	return ""
}

// NormalizeID checks a plugin id as herdr does: trimmed, 1 to 120
// characters of ASCII letters, digits, colon, dot, underscore or hyphen.
func NormalizeID(v string) (string, bool) { return normalizeID(v, maxPluginIDChars, true) }

// NormalizeLocalID checks an action, pane or link handler id: the plugin id
// rules without the dot, which joins a plugin id to an action id.
func NormalizeLocalID(v string) (string, bool) { return normalizeID(v, maxLocalIDChars, false) }

func normalizeID(v string, maxChars int, dot bool) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" || len([]rune(v)) > maxChars {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == ':', c == '_', c == '-':
		case c == '.' && dot:
		default:
			return "", false
		}
	}
	return v, true
}

// CurrentPlatform is herdr's name for this machine's platform.
func CurrentPlatform() string {
	switch runtime.GOOS {
	case "linux":
		return "linux"
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	}
	// herdr builds for the three. Another unix runs what linux runs.
	return "linux"
}

// Supported checks an entry's platforms against this machine: the entry's
// own list when it has one, else the plugin's, else every platform. It
// returns herdr's platform_unsupported error.
func (p *Plugin) Supported(item []string, subject string) *Error {
	list := item
	if list == nil {
		list = p.Platforms
	}
	if list == nil || slices.Contains(list, CurrentPlatform()) {
		return nil
	}
	return errf("platform_unsupported", "%s does not support the current platform (%s)", subject, CurrentPlatform())
}

// EffectivePlatforms is the platforms list that applies to an entry.
func (p *Plugin) EffectivePlatforms(item []string) []string {
	if item != nil {
		return item
	}
	return p.Platforms
}

// Action finds an action by id.
func (p *Plugin) Action(id string) (Action, bool) {
	for _, a := range p.Actions {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}

// Pane finds a pane entry by id.
func (p *Plugin) Pane(id string) (Pane, bool) {
	for _, x := range p.Panes {
		if x.ID == id {
			return x, true
		}
	}
	return Pane{}, false
}
