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
	// ConfigDir is the harness's configuration directory.
	ConfigDir func(Env) string
	// File is the file tuios edits or writes, relative to ConfigDir.
	File string
	// Events are the hook events registered, for a hooks-file target. A
	// plugin target has none.
	Events []HookEvent
}

func (t *Target) plugin() bool { return len(t.Events) == 0 }

// Path is the file this target edits or writes.
func (t *Target) Path(env Env) string { return filepath.Join(t.ConfigDir(env), t.File) }

var targets = []*Target{
	{
		ID: ClaudeCode, Name: "Claude Code", Binary: "claude", Version: 1,
		Source:    "https://code.claude.com/docs/en/hooks (settings.json hooks: event, matcher group, command hook, timeout in seconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CLAUDE_CONFIG_DIR", ".claude") },
		File:      "settings.json",
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PermissionRequest", 5}, {"PostToolUse", 5}, {"PostToolUseFailure", 5},
			{"PermissionDenied", 5}, {"ElicitationResult", 5}, {"Notification", 5},
			{"Stop", 5}, {"StopFailure", 5}, {"SessionEnd", 5},
		},
	},
	{
		ID: Codex, Name: "Codex", Binary: "codex", Version: 1,
		Source:    "https://learn.chatgpt.com/docs/hooks (~/.codex/hooks.json, same shape as Claude Code's, timeout in seconds, Interrupt and SessionEnd capped at 3)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CODEX_HOME", ".codex") },
		File:      "hooks.json",
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PermissionRequest", 5}, {"PostToolUse", 5}, {"Stop", 5},
			{"Interrupt", 2}, {"SessionEnd", 2},
		},
	},
	{
		ID: GeminiCLI, Name: "Gemini CLI", Binary: "gemini", Version: 1,
		Source:    "https://geminicli.com/docs/hooks/reference/ (~/.gemini/settings.json hooks, timeout in milliseconds, no enable flag)",
		ConfigDir: func(e Env) string { return filepath.Join(e.Home, ".gemini") },
		File:      "settings.json",
		Events: []HookEvent{
			{"SessionStart", 5000}, {"BeforeAgent", 5000}, {"BeforeTool", 5000},
			{"AfterTool", 5000}, {"Notification", 5000}, {"AfterAgent", 5000},
			{"SessionEnd", 5000},
		},
	},
	{
		ID: OpenCode, Name: "opencode", Binary: "opencode", Version: 1,
		Source: "https://opencode.ai/docs/plugins/ (global plugins load from ~/.config/opencode/plugins)",
		ConfigDir: func(e Env) string {
			if x := strings.TrimSpace(e.env("XDG_CONFIG_HOME")); x != "" {
				return filepath.Join(x, "opencode")
			}
			return filepath.Join(e.Home, ".config", "opencode")
		},
		File: filepath.Join("plugins", "tuios-agent-state.js"),
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

// renderPlugin fills the opencode plugin template.
func renderPlugin(tuios string, version int) []byte {
	cmd, _ := json.Marshal(tuios)
	out := strings.ReplaceAll(openCodePluginTemplate, "__TUIOS_COMMAND__", string(cmd))
	out = strings.ReplaceAll(out, "__TUIOS_VERSION__", strconv.Itoa(version))
	return []byte(out)
}

// Result is what one install or uninstall did.
type Result struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	// Backup is the copy of the file as it was before, empty when nothing
	// was rewritten or there was no file.
	Backup string   `json:"backup,omitempty"`
	Notes  []string `json:"notes,omitempty"`
}

// ErrNoConfigDir is returned by Install when the harness has never run here.
var ErrNoConfigDir = errors.New("configuration directory not found")

// Install writes this build's managed entries. It is idempotent: a second
// install with nothing changed writes nothing. Entries from an older version
// are replaced, and nothing that tuios did not write is touched.
func (t *Target) Install(env Env, tuios string) (Result, error) {
	dir := t.ConfigDir(env)
	res := Result{Harness: t.ID, Path: t.Path(env)}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return res, fmt.Errorf("%w: %s. Install %s and run it once, then try again", ErrNoConfigDir, dir, t.Name)
	}
	var want []byte
	if t.plugin() {
		want = renderPlugin(tuios, t.Version)
		have, err := readOptional(res.Path)
		if err != nil {
			return res, err
		}
		if have != nil && !isManagedPlugin(have) {
			return res, fmt.Errorf("%s exists and was not written by tuios, so it is left alone", res.Path)
		}
		if bytes.Equal(have, want) {
			return res, nil
		}
	} else {
		have, err := readOptional(res.Path)
		if err != nil {
			return res, err
		}
		out, changed, err := editHooks(have, t.Events, HookCommand(tuios, t.ID, t.Version), true)
		if err != nil {
			return res, fmt.Errorf("failed to read %s: %w. It was left unchanged", res.Path, err)
		}
		if !changed {
			res.Notes = t.notes(env)
			return res, nil
		}
		if have != nil {
			res.Backup = res.Path + BackupSuffix
		}
		want = out
	}
	if err := writeAtomic(res.Path, want); err != nil {
		return res, err
	}
	res.Changed = true
	res.Notes = t.notes(env)
	return res, nil
}

// Uninstall removes what Install wrote and nothing else. A harness with
// nothing of tuios's installed is not an error.
func (t *Target) Uninstall(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.Path(env)}
	have, err := readOptional(res.Path)
	if err != nil || have == nil {
		return res, err
	}
	if t.plugin() {
		if !isManagedPlugin(have) {
			return res, nil
		}
		if err := os.Remove(res.Path); err != nil {
			return res, err
		}
		res.Changed = true
		return res, nil
	}
	out, changed, err := editHooks(have, nil, "", false)
	if err != nil {
		return res, fmt.Errorf("failed to read %s: %w. It was left unchanged", res.Path, err)
	}
	if !changed {
		return res, nil
	}
	if err := writeAtomic(res.Path, out); err != nil {
		return res, err
	}
	res.Changed = true
	res.Backup = res.Path + BackupSuffix
	return res, nil
}

// Status is whether a harness's integration is in place and current.
type Status struct {
	Harness         string   `json:"harness"`
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	ConfigDirExists bool     `json:"config_dir_exists"`
	Installed       bool     `json:"installed"`
	Current         bool     `json:"current"`
	Version         int      `json:"version,omitempty"`
	WantVersion     int      `json:"want_version"`
	Binary          string   `json:"binary"`
	BinaryPath      string   `json:"binary_path,omitempty"`
	TuiosOnPath     bool     `json:"tuios_on_path"`
	Notes           []string `json:"notes,omitempty"`
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

// isManagedPlugin reports whether a plugin file is one tuios wrote.
func isManagedPlugin(data []byte) bool {
	return bytes.Contains(data, []byte("TUIOS_INTEGRATION_ID="))
}

// Status reports what is installed. tuios is the command a current install
// runs, so an install pointing at another binary reads as not current.
func (t *Target) Status(env Env, tuios string) Status {
	st := Status{Harness: t.ID, Name: t.Name, Path: t.Path(env), WantVersion: t.Version, Binary: t.Binary}
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
	have, err := readOptional(st.Path)
	if err != nil {
		st.Notes = append(st.Notes, "cannot read "+st.Path+": "+err.Error())
		return st
	}
	if t.plugin() {
		if have != nil && isManagedPlugin(have) {
			st.Installed = true
			st.Version = parseVersion(string(have))
			st.Current = bytes.Equal(have, renderPlugin(tuios, t.Version))
		}
	} else if have != nil {
		entries, err := findManaged(have)
		if err != nil {
			st.Notes = append(st.Notes, "cannot parse "+st.Path+": "+err.Error())
			return st
		}
		st.Installed = len(entries) > 0
		st.Current = managedCurrent(entries, t.Events, HookCommand(tuios, t.ID, t.Version))
		for _, e := range entries {
			if v := parseVersion(e.Command); v > st.Version {
				st.Version = v
			}
		}
	}
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
