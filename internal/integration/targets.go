package integration

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

//go:embed assets/opencode/tuios-agent-state.js
var openCodePluginTemplate string

//go:embed assets/amp/tuios-agent-state.ts
var ampPluginTemplate string

//go:embed assets/pi/tuios-agent-state.ts
var piExtensionTemplate string

//go:embed assets/hermes/__init__.py
var hermesPluginTemplate string

//go:embed assets/hermes/plugin.yaml
var hermesManifestTemplate string

// Env is what the installers read from the machine, so a test can point them
// at a temporary home.
type Env struct {
	Home     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

// SystemEnv is the running process's own environment.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Home: home, Getenv: os.Getenv, LookPath: exec.LookPath}
}

func (e Env) env(name string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(name)
}

// dirFromEnv is the directory an override variable names, else home joined
// with rest.
func (e Env) dirFromEnv(name string, rest ...string) string {
	if v := strings.TrimSpace(e.env(name)); v != "" {
		if v == "~" || strings.HasPrefix(v, "~/") {
			v = filepath.Join(e.Home, strings.TrimPrefix(v, "~"))
		}
		return v
	}
	return filepath.Join(append([]string{e.Home}, rest...)...)
}

// xdgConfig is app's directory under XDG_CONFIG_HOME, else ~/.config.
func (e Env) xdgConfig(app string) string {
	if x := strings.TrimSpace(e.env("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, app)
	}
	return filepath.Join(e.Home, ".config", app)
}

// What an integration reports.
const (
	// ReportsState is an integration whose hooks cover the whole turn, so the
	// pane's state is taken from them.
	ReportsState = "state"
	// ReportsSession is an integration that names the conversation only,
	// through set-agent-session, and leaves the state to the screen rules.
	ReportsSession = "session"
)

// Target is one harness tuios can wire itself into.
type Target struct {
	// ID is the harness id, as the manifests name it.
	ID string
	// Name is how the harness calls itself.
	Name string
	// Binary is the program that starts it, checked on PATH by doctor.
	Binary string
	// Version is the integration version this build installs. It goes up
	// whenever what is installed changes, so status can say an older install
	// is out of date.
	Version int
	// Source cites the format the installer writes.
	Source string
	// Reports is ReportsState or ReportsSession.
	Reports string
	// ConfigDir is the harness's configuration directory. Install refuses when
	// it does not exist: the harness has not run here.
	ConfigDir func(Env) string
	// File is the file tuios edits or writes, relative to ConfigDir.
	File string
	// Events are the hook events registered, for a target whose file lists
	// hook commands. A plugin registers its events in its own code and lists
	// none here.
	Events []HookEvent

	// format is how File is written.
	format fileFormat
	// extra are more files the integration needs, written after File and
	// removed before it.
	extra []extraFile
	// ownedDir, when set, is a directory relative to ConfigDir that holds
	// only tuios's files. Uninstall removes it once it is empty.
	ownedDir string
}

// extraFile is a second file an integration writes, such as a plugin's
// manifest or the setting that turns a plugin on.
type extraFile struct {
	file   string
	format fileFormat
}

// Path is the file this target edits or writes.
func (t *Target) Path(env Env) string { return filepath.Join(t.ConfigDir(env), t.File) }

// Paths lists every file the integration touches, File first.
func (t *Target) Paths(env Env) []string {
	out := []string{t.Path(env)}
	for _, x := range t.extra {
		out = append(out, filepath.Join(t.ConfigDir(env), x.file))
	}
	return out
}

func (t *Target) files(env Env) []extraFile {
	return append([]extraFile{{file: t.File, format: t.format}}, t.extra...)
}

// renderTemplate fills a plugin template: the program to run, the version,
// and for the opencode template, which Kilo shares, the harness.
func renderTemplate(tmpl string) func(t *Target, tuios string) []byte {
	return func(t *Target, tuios string) []byte {
		cmd, _ := json.Marshal(tuios)
		out := strings.ReplaceAll(tmpl, "__TUIOS_COMMAND__", string(cmd))
		out = strings.ReplaceAll(out, "__TUIOS_VERSION__", strconv.Itoa(t.Version))
		out = strings.ReplaceAll(out, "__TUIOS_HARNESS__", t.ID)
		out = strings.ReplaceAll(out, "__TUIOS_NAME__", t.Name)
		return []byte(out)
	}
}

// renderJSON renders a hook file tuios owns whole, from its events. build
// makes the object for one event.
func renderJSON(top map[string]any, build func(command string, ev HookEvent) any) func(t *Target, tuios string) []byte {
	return func(t *Target, tuios string) []byte {
		cmd := HookCommand(tuios, t.ID, t.Version)
		hooks := newObject()
		for _, ev := range t.Events {
			v, _ := marshalPlain([]any{build(cmd, ev)})
			hooks.set(ev.Name, v)
		}
		root := newObject()
		keys := make([]string, 0, len(top))
		for k := range top {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			v, _ := marshalPlain(top[k])
			root.set(k, v)
		}
		root.set("hooks", hooks.compact())
		out, _ := root.render()
		return out
	}
}

// powershellCommand makes a command line PowerShell runs: a quoted program
// path is a string there unless called with &.
func powershellCommand(cmd string) string {
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, "'") {
		return "& " + cmd
	}
	return cmd
}

var targets = []*Target{
	{
		// Version 2 gives PermissionRequest room to hold its prompt for the
		// Inbox ([agents.approvals]): 310 seconds, past the daemon's longest
		// hold of 300. With approvals off the hook still returns in well under
		// a second, so the longer limit costs nothing.
		ID: ClaudeCode, Name: "Claude Code", Binary: "claude", Version: 2, Reports: ReportsState,
		Source:    "https://code.claude.com/docs/en/hooks (settings.json hooks: event, matcher group, command hook, timeout in seconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CLAUDE_CONFIG_DIR", ".claude") },
		File:      "settings.json",
		format:    nestedHooks{},
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PermissionRequest", ApprovalHookTimeout}, {"PostToolUse", 5}, {"PostToolUseFailure", 5},
			{"PermissionDenied", 5}, {"ElicitationResult", 5}, {"Notification", 5},
			{"Stop", 5}, {"StopFailure", 5}, {"SessionEnd", 5},
		},
	},
	{
		ID: Codex, Name: "Codex", Binary: "codex", Version: 1, Reports: ReportsState,
		Source:    "https://learn.chatgpt.com/docs/hooks (~/.codex/hooks.json, same shape as Claude Code's, timeout in seconds, Interrupt and SessionEnd capped at 3)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CODEX_HOME", ".codex") },
		File:      "hooks.json",
		format:    nestedHooks{},
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PermissionRequest", 5}, {"PostToolUse", 5}, {"Stop", 5},
			{"Interrupt", 2}, {"SessionEnd", 2},
		},
	},
	{
		ID: GeminiCLI, Name: "Gemini CLI", Binary: "gemini", Version: 1, Reports: ReportsState,
		Source:    "https://geminicli.com/docs/hooks/reference/ (~/.gemini/settings.json hooks, timeout in milliseconds, no enable flag)",
		ConfigDir: func(e Env) string { return filepath.Join(e.Home, ".gemini") },
		File:      "settings.json",
		format:    nestedHooks{},
		Events: []HookEvent{
			{"SessionStart", 5000}, {"BeforeAgent", 5000}, {"BeforeTool", 5000},
			{"AfterTool", 5000}, {"Notification", 5000}, {"AfterAgent", 5000},
			{"SessionEnd", 5000},
		},
	},
	{
		// Version 2 offers permission requests to the Inbox and sends the
		// person's reply back to opencode.
		ID: OpenCode, Name: "opencode", Binary: "opencode", Version: 2, Reports: ReportsState,
		Source:    "https://opencode.ai/docs/plugins/ (global plugins load from ~/.config/opencode/plugins)",
		ConfigDir: func(e Env) string { return e.xdgConfig("opencode") },
		File:      filepath.Join("plugins", "tuios-agent-state.js"),
		format:    ownedFile{render: renderTemplate(openCodePluginTemplate)},
	},
	{
		ID: Amp, Name: "Amp", Binary: "amp", Version: 1, Reports: ReportsState,
		Source:    "https://ampcode.com/manual/plugin-api (TypeScript plugins run by Bun from ~/.config/amp/plugins; session.start, agent.start, agent.end)",
		ConfigDir: func(e Env) string { return e.xdgConfig("amp") },
		File:      filepath.Join("plugins", "tuios-agent-state.ts"),
		format:    ownedFile{render: renderTemplate(ampPluginTemplate)},
	},
	{
		ID: Antigravity, Name: "Antigravity CLI", Binary: "agy", Version: 1, Reports: ReportsSession,
		Source:    "herdr src/integration/targets.rs install_antigravity_cli (~/.gemini/config/hooks.json keyed by hook name, PreInvocation takes a flat handler list, timeout in seconds, stdout a JSON object)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("ANTIGRAVITY_CLI_CONFIG_DIR", ".gemini", "config") },
		File:      "hooks.json",
		format:    namedBlock{key: "tuios"},
		Events:    []HookEvent{{"PreInvocation", 5}},
	},
	{
		ID: Copilot, Name: "GitHub Copilot CLI", Binary: "copilot", Version: 1, Reports: ReportsSession,
		Source:    "https://docs.github.com/en/copilot/reference/hooks-configuration (every ~/.copilot/hooks/*.json is loaded; version 1, command hooks with bash, powershell and timeoutSec)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("COPILOT_HOME", ".copilot") },
		File:      filepath.Join("hooks", "tuios.json"),
		format: ownedFile{render: renderJSON(map[string]any{"version": 1}, func(cmd string, ev HookEvent) any {
			return map[string]any{"type": "command", "bash": cmd, "powershell": powershellCommand(cmd), "timeoutSec": ev.Timeout}
		})},
		Events: []HookEvent{{"SessionStart", 5}},
	},
	{
		ID: Crush, Name: "Crush", Binary: "crush", Version: 1, Reports: ReportsSession,
		Source:    "https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md (~/.config/crush/crush.json hooks.PreToolUse, a flat list of {name, command, timeout} in seconds; PreToolUse is the only event)",
		ConfigDir: func(e Env) string { return e.xdgConfig("crush") },
		File:      "crush.json",
		format: flatHooks{entry: func(cmd string, ev HookEvent) map[string]any {
			return map[string]any{"name": "tuios", "command": cmd, "timeout": ev.Timeout}
		}},
		Events: []HookEvent{{"PreToolUse", 5}},
	},
	{
		ID: CursorAgent, Name: "Cursor Agent", Binary: "cursor-agent", Version: 1, Reports: ReportsSession,
		Source:    "https://cursor.com/docs/hooks (~/.cursor/hooks.json, version 1, each event a list of {command})",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CURSOR_CONFIG_DIR", ".cursor") },
		File:      "hooks.json",
		format: flatHooks{version1: true, entry: func(cmd string, _ HookEvent) map[string]any {
			return map[string]any{"command": cmd}
		}},
		Events: []HookEvent{{"sessionStart", 0}},
	},
	{
		ID: Devin, Name: "Devin CLI", Binary: "devin", Version: 1, Reports: ReportsSession,
		Source: "herdr src/integration/targets.rs install_devin (config.json hooks in Claude Code's shape, timeout in seconds)",
		ConfigDir: func(e Env) string {
			if x := strings.TrimSpace(e.env("XDG_CONFIG_HOME")); x != "" {
				return filepath.Join(x, "devin")
			}
			if runtime.GOOS == "windows" {
				if a := strings.TrimSpace(e.env("APPDATA")); a != "" {
					return filepath.Join(a, "devin")
				}
			}
			return filepath.Join(e.Home, ".config", "devin")
		},
		File:   "config.json",
		format: nestedHooks{},
		Events: []HookEvent{{"SessionStart", 5}, {"UserPromptSubmit", 5}},
	},
	{
		ID: Droid, Name: "Droid", Binary: "droid", Version: 1, Reports: ReportsSession,
		Source:    "herdr src/integration/targets.rs install_droid (~/.factory/settings.json hooks in Claude Code's shape, timeout in seconds)",
		ConfigDir: func(e Env) string { return filepath.Join(e.Home, ".factory") },
		File:      "settings.json",
		format:    nestedHooks{},
		Events:    []HookEvent{{"SessionStart", 5}},
	},
	{
		ID: Grok, Name: "Grok CLI", Binary: "grok", Version: 1, Reports: ReportsSession,
		Source:    "herdr src/integration/targets.rs install_grok (Grok merges every ~/.grok/hooks/*.json; Claude Code's hook shape, timeout in seconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("GROK_HOME", ".grok") },
		File:      filepath.Join("hooks", "tuios.json"),
		format: ownedFile{render: renderJSON(nil, func(cmd string, ev HookEvent) any {
			return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": ev.Timeout}}}
		})},
		Events: []HookEvent{{"SessionStart", 5}},
	},
	{
		ID: Hermes, Name: "Hermes Agent", Binary: "hermes", Version: 1, Reports: ReportsSession,
		Source: "herdr src/integration/assets/hermes (a plugin directory under ~/.hermes/plugins with plugin.yaml and __init__.py, turned on in config.yaml plugins.enabled)",
		ConfigDir: func(e Env) string {
			if runtime.GOOS == "windows" && strings.TrimSpace(e.env("HERMES_HOME")) == "" {
				if l := strings.TrimSpace(e.env("LOCALAPPDATA")); l != "" {
					return filepath.Join(l, "hermes")
				}
			}
			return e.dirFromEnv("HERMES_HOME", ".hermes")
		},
		File:   filepath.Join("plugins", "tuios-agent-state", "__init__.py"),
		format: ownedFile{render: renderTemplate(hermesPluginTemplate)},
		extra: []extraFile{
			{file: filepath.Join("plugins", "tuios-agent-state", "plugin.yaml"), format: ownedFile{render: renderTemplate(hermesManifestTemplate)}},
			{file: "config.yaml", format: yamlListItem{key: "plugins", sub: "enabled", item: "tuios-agent-state"}},
		},
		ownedDir: filepath.Join("plugins", "tuios-agent-state"),
	},
	{
		// Version 2: the opencode plugin it shares offers permission requests
		// to the Inbox.
		ID: Kilo, Name: "Kilo", Binary: "kilo", Version: 2, Reports: ReportsState,
		Source:    "herdr src/integration/assets/kilo (Kilo Code CLI is an opencode fork; plugins load from ~/.config/kilo/plugin)",
		ConfigDir: func(e Env) string { return e.xdgConfig("kilo") },
		File:      filepath.Join("plugin", "tuios-agent-state.js"),
		format:    ownedFile{render: renderTemplate(openCodePluginTemplate)},
	},
	{
		ID: Kimi, Name: "Kimi Code CLI", Binary: "kimi", Version: 1, Reports: ReportsState,
		Source:    "https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html ([[hooks]] tables in ~/.kimi-code/config.toml: event, matcher, command, timeout in seconds; needs Kimi Code CLI 0.14.0 or newer)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("KIMI_CODE_HOME", ".kimi-code") },
		File:      "config.toml",
		format:    tomlBlock{},
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PermissionRequest", 5}, {"PostToolUse", 5}, {"PostToolUseFailure", 5},
			{"PermissionResult", 5}, {"Stop", 5}, {"StopFailure", 5},
			{"Interrupt", 5}, {"SessionEnd", 5},
		},
	},
	{
		ID: Pi, Name: "Pi", Binary: "pi", Version: 1, Reports: ReportsState,
		Source:    "herdr src/integration/assets/pi (TypeScript extensions load from ~/.pi/agent/extensions, or PI_CODING_AGENT_DIR/extensions)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("PI_CODING_AGENT_DIR", ".pi", "agent") },
		File:      filepath.Join("extensions", "tuios-agent-state.ts"),
		format:    ownedFile{render: renderTemplate(piExtensionTemplate)},
	},
	{
		ID: Qoder, Name: "Qoder CLI", Binary: "qodercli", Version: 1, Reports: ReportsSession,
		Source:    "https://docs.qoder.com/zh/cli/hooks (~/.qoder/settings.json hooks in Claude Code's shape, matcher *, timeout in seconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("QODER_CONFIG_DIR", ".qoder") },
		File:      "settings.json",
		format:    nestedHooks{matcher: "*"},
		Events:    []HookEvent{{"SessionStart", 5}},
	},
	{
		ID: Qwen, Name: "Qwen Code", Binary: "qwen", Version: 1, Reports: ReportsSession,
		Source:    "herdr src/integration/targets.rs install_qwen (~/.qwen/settings.json hooks, Gemini CLI's shape, matcher *, timeout in milliseconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("QWEN_HOME", ".qwen") },
		File:      "settings.json",
		format:    nestedHooks{matcher: "*"},
		Events:    []HookEvent{{"SessionStart", 5000}},
	},
}

// Targets lists every harness with an installer, in a stable order.
func Targets() []*Target { return slices.Clone(targets) }

// LookupTarget finds the installer for a harness name or alias.
func LookupTarget(name string) (*Target, bool) {
	id, ok := Canonical(name)
	if !ok {
		return nil, false
	}
	for _, t := range targets {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// Unsupported is a harness with a manifest and no integration, and why.
type Unsupported struct {
	Harness string `json:"harness"`
	Reason  string `json:"reason"`
}

// UnsupportedHarnesses lists the bundled harnesses tuios has no integration
// for, each with the reason, so doctor can say so rather than leave them out.
// Their state comes from their manifests' screen and title rules.
func UnsupportedHarnesses() []Unsupported {
	return []Unsupported{
		{"aider", "its only hook is notifications-command, one command that replaces the user's own and carries no payload"},
		{"cline", "its hooks are one executable per event in a directory that has moved between releases, behind a setting, and a file tuios wrote would take the name of the user's own"},
		{"kiro", "its CLI hooks live in per-agent files with no documented user-wide location or payload"},
		{"maki", "it has Lua plugins but no documented user-wide plugin file tuios could own without editing the user's init.lua"},
	}
}

// HookCommand is the command a managed hook entry runs. tuios is the program
// to run, normally "tuios" so an upgrade that moves the binary keeps working.
func HookCommand(tuios, harnessID string, version int) string {
	return shellWord(tuios) + " agent-hook " + harnessID + " " + managedMarker + " " + strconv.Itoa(version)
}

// shellWord quotes a program path for the shell a harness runs its hook
// commands through, and leaves a plain word alone.
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`&|;<>()*?[]{}!#~") {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Result is what one install or uninstall did.
type Result struct {
	Harness string `json:"harness"`
	// Path is the integration's main file.
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	// Paths lists every file written or removed, for an integration that
	// touches more than one.
	Paths []string `json:"paths,omitempty"`
	// Backup is the copy of the file as it was before, empty when nothing
	// was rewritten or there was no file.
	Backup string   `json:"backup,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

// ErrNoConfigDir is returned by Install when the harness has never run here.
var ErrNoConfigDir = errors.New("configuration directory not found")

// filePlan is one file's part of an install or uninstall, worked out before
// anything is written so a failure on any file writes none.
type filePlan struct {
	path    string
	have    []byte
	out     []byte
	changed bool
	remove  bool
	owned   bool
}

// plan works out every file's new content.
func (t *Target) plan(env Env, tuios string, install bool) ([]filePlan, error) {
	dir := t.ConfigDir(env)
	var plans []filePlan
	for _, f := range t.files(env) {
		path := filepath.Join(dir, f.file)
		have, err := readOptional(path)
		if err != nil {
			return nil, err
		}
		out, changed, remove, err := f.format.apply(t, have, tuios, install)
		switch {
		case errors.Is(err, errNotOurs):
			return nil, fmt.Errorf("%s %w", path, err)
		case err != nil:
			return nil, fmt.Errorf("failed to read %s: %w. It was left unchanged", path, err)
		}
		plans = append(plans, filePlan{path: path, have: have, out: out, changed: changed, remove: remove, owned: f.format.owned()})
	}
	return plans, nil
}

// carryOut writes or removes each changed file, in order. removing says this is
// an uninstall, which also removes the owned directory once it is empty.
func (t *Target) carryOut(env Env, res *Result, plans []filePlan, removing bool) error {
	for _, p := range plans {
		if !p.changed {
			continue
		}
		if p.remove {
			if err := os.Remove(p.path); err != nil {
				return err
			}
		} else if err := writeAtomic(p.path, p.out); err != nil {
			return err
		}
		res.Changed = true
		res.Paths = append(res.Paths, p.path)
		if !p.owned && !p.remove && p.have != nil && res.Backup == "" {
			res.Backup = p.path + BackupSuffix
		}
	}
	if removing && t.ownedDir != "" {
		// Only removes an empty directory; one holding anything else stays.
		_ = os.Remove(filepath.Join(t.ConfigDir(env), t.ownedDir))
	}
	return nil
}

// Install writes this build's managed entries. It is idempotent: a second
// install with nothing changed writes nothing. Entries from an older version
// are replaced, and nothing that tuios did not write is touched.
func (t *Target) Install(env Env, tuios string) (Result, error) {
	dir := t.ConfigDir(env)
	res := Result{Harness: t.ID, Path: t.Path(env)}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return res, fmt.Errorf("%w: %s. Install %s and run it once, then try again", ErrNoConfigDir, dir, t.Name)
	}
	plans, err := t.plan(env, tuios, true)
	if err != nil {
		return res, err
	}
	if err := t.carryOut(env, &res, plans, false); err != nil {
		return res, err
	}
	res.Notes = t.notes(env)
	return res, nil
}

// Uninstall removes what Install wrote and nothing else. A harness with
// nothing of tuios's installed is not an error.
func (t *Target) Uninstall(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.Path(env)}
	plans, err := t.plan(env, "", false)
	if err != nil {
		return res, err
	}
	// The setting that turns a plugin on goes before the plugin itself.
	slices.Reverse(plans)
	if err := t.carryOut(env, &res, plans, true); err != nil {
		return res, err
	}
	return res, nil
}

// Status is whether a harness's integration is in place and current.
type Status struct {
	Harness string `json:"harness"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	// Reports is what the integration reports: state, or session for one
	// that names the conversation and leaves the state to the screen rules.
	Reports         string   `json:"reports"`
	ConfigDirExists bool     `json:"config_dir_exists"`
	Installed       bool     `json:"installed"`
	Current         bool     `json:"current"`
	Version         int      `json:"version,omitempty"`
	WantVersion     int      `json:"want_version"`
	Binary          string   `json:"binary"`
	BinaryPath      string   `json:"binary_path,omitempty"`
	TuiosOnPath     bool     `json:"tuios_on_path"`
	Notes           []string `json:"notes,omitempty"`
	// MCP is the MCP server registration, for a harness tuios can register
	// one with. See mcp.go.
	MCP *MCPStatus `json:"mcp,omitempty"`
}

var versionRe = regexp.MustCompile(`TUIOS_INTEGRATION_VERSION=(\d+)|` + managedMarker + ` (\d+)`)

func parseVersion(s string) int {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, _ := strconv.Atoi(m[1] + m[2])
	return v
}

// Status reports what is installed. tuios is the command a current install
// runs, so an install pointing at another binary reads as not current.
func (t *Target) Status(env Env, tuios string) Status {
	st := Status{Harness: t.ID, Name: t.Name, Path: t.Path(env), Reports: t.Reports, WantVersion: t.Version, Binary: t.Binary}
	if t.SupportsMCP() {
		m := t.MCPState(env, tuios)
		st.MCP = &m
	}
	if fi, err := os.Stat(t.ConfigDir(env)); err == nil && fi.IsDir() {
		st.ConfigDirExists = true
	}
	if env.LookPath != nil {
		if p, err := env.LookPath(t.Binary); err == nil {
			st.BinaryPath = p
		}
		_, err := env.LookPath("tuios")
		st.TuiosOnPath = err == nil
	}
	allCurrent := true
	for i, f := range t.files(env) {
		path := filepath.Join(t.ConfigDir(env), f.file)
		have, err := readOptional(path)
		if err != nil {
			st.Notes = append(st.Notes, "cannot read "+path+": "+err.Error())
			return st
		}
		installed, current, version, err := f.format.state(t, have, tuios)
		if err != nil {
			st.Notes = append(st.Notes, "cannot parse "+path+": "+err.Error())
			return st
		}
		if i == 0 {
			st.Version = version
		}
		st.Installed = st.Installed || installed
		allCurrent = allCurrent && current
	}
	st.Current = st.Installed && allCurrent
	st.Notes = append(st.Notes, t.notes(env)...)
	return st
}

// managedCurrent reports whether the managed entries are exactly one command
// per wanted event and that command is the current one.
func managedCurrent(entries []managedEntry, events []HookEvent, command string) bool {
	if len(entries) != len(events) {
		return false
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Command != command || seen[e.Event] {
			return false
		}
		seen[e.Event] = true
	}
	for _, ev := range events {
		if !seen[ev.Name] {
			return false
		}
	}
	return true
}

// notes are things about a harness's own configuration that stop the
// integration from working, or make it report twice.
func (t *Target) notes(env Env) []string {
	var out []string
	switch t.ID {
	case ClaudeCode:
		if data, _ := readOptional(t.Path(env)); bytes.Contains(data, []byte("tuios-agent-state.sh")) {
			out = append(out, "the older tuios-agent-state.sh shim is also wired in "+t.Path(env)+". It now runs the same reporter, so every event is reported twice. Remove its entries.")
		}
	case Codex:
		data, _ := readOptional(filepath.Join(t.ConfigDir(env), "config.toml"))
		if codexHooksDisabled(string(data)) {
			out = append(out, "hooks are turned off in "+filepath.Join(t.ConfigDir(env), "config.toml")+" ([features] hooks = false), so Codex runs none of them.")
		}
	}
	return out
}

// codexHooksDisabled reports whether a Codex config.toml turns hooks off. It
// reads the one key it needs line by line rather than parsing TOML, which is
// all a check that never writes the file needs.
func codexHooksDisabled(toml string) bool {
	inFeatures := false
	for line := range strings.SplitSeq(toml, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if strings.HasPrefix(line, "[") {
			inFeatures = line == "[features]"
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if (inFeatures && key == "hooks") || key == "features.hooks" {
			return val == "false"
		}
	}
	return false
}
