package integration

import (
	"bytes"
	_ "embed"
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
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/tarpack"
)

//go:generate go run ../tarpack/gen.go assets.tar.gz assets

//go:embed assets.tar.gz
var assetArchive []byte

var assets = sync.OnceValue(func() map[string][]byte {
	files, err := tarpack.Unpack(assetArchive)
	if err != nil {
		panic(fmt.Sprintf("integration: embedded assets: %v", err))
	}
	return files
})

const (
	openCodePluginTemplate  = "assets/opencode/tuios-agent-state.js"
	openCodeV2IndexTemplate = "assets/opencode/index.js"
	openCodeV2TuiTemplate   = "assets/opencode/tui.js"
	ampPluginTemplate       = "assets/amp/tuios-agent-state.ts"
	piExtensionTemplate     = "assets/pi/tuios-agent-state.ts"
	ompExtensionTemplate    = "assets/omp/tuios-omp-agent-state.ts"
	hermesPluginTemplate    = "assets/hermes/__init__.py"
	hookSocketSnippet       = "assets/common/hook_socket.js"
	hermesManifestTemplate  = "assets/hermes/plugin.yaml"
)

// Env is what the installers read from the machine, so a test can point them
// at a temporary home.
type Env struct {
	Home     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	// HookSocket reports the socket tool hook commands forward events
	// through (DetectHookSocket), nil for none: the hooks then start tuios
	// for each event.
	HookSocket func() string
}

var systemHookSocket = sync.OnceValue(func() string { return DetectHookSocket(exec.LookPath) })

// SystemEnv is the running process's own environment.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{Home: home, Getenv: os.Getenv, LookPath: exec.LookPath, HookSocket: systemHookSocket}
}

func (e Env) env(name string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(name)
}

func (e Env) dirFromEnv(name string, rest ...string) string {
	if v := strings.TrimSpace(e.env(name)); v != "" {
		if v == "~" || strings.HasPrefix(v, "~/") {
			v = filepath.Join(e.Home, strings.TrimPrefix(v, "~"))
		}
		return v
	}
	return filepath.Join(append([]string{e.Home}, rest...)...)
}

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

	format   fileFormat
	extra    []extraFile
	ownedDir string
}

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

func renderTemplate(name string) func(t *Target, run Runner) []byte {
	return func(t *Target, run Runner) []byte {
		return renderSource(assets()[name], t, run.Program)
	}
}

func renderSource(src []byte, t *Target, tuios string) []byte {
	cmd, _ := jsonx.Marshal(tuios)
	out := strings.ReplaceAll(string(src), "__TUIOS_COMMAND__", string(cmd))
	out = strings.ReplaceAll(out, "__TUIOS_VERSION__", strconv.Itoa(t.Version))
	out = strings.ReplaceAll(out, "__TUIOS_HARNESS__", t.ID)
	out = strings.ReplaceAll(out, "__TUIOS_NAME__", t.Name)
	if strings.Contains(out, "__TUIOS_HOOK_SOCKET__") {
		env, _ := jsonx.Marshal(HookEnv)
		socket := strings.ReplaceAll(string(assets()[hookSocketSnippet]), "__TUIOS_HOOK_ENV__", string(env))
		out = strings.Replace(out, "__TUIOS_HOOK_SOCKET__", socket, 1)
	}
	return []byte(out)
}

func renderOpenCodePlugin(t *Target, run Runner) []byte {
	out := renderTemplate(openCodePluginTemplate)(t, run)
	return append(out, []byte(`
export default {
  id: "tuios-agent-state-v1",
  server: TuiosAgentState,
  setup() {},
};
`)...)
}

func renderJSON(top map[string]any, build func(command string, ev HookEvent) any) func(t *Target, run Runner) []byte {
	return func(t *Target, run Runner) []byte {
		cmd := run.command(t.ID, t.Version)
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

func powershellCommand(cmd string) string {
	if strings.HasPrefix(cmd, `"`) || strings.HasPrefix(cmd, "'") {
		return "& " + cmd
	}
	return cmd
}

var targets = []*Target{
	{
		ID: ClaudeCode, Name: "Claude Code", Binary: "claude", Version: 3, Reports: ReportsState,
		Source:    "https://code.claude.com/docs/en/hooks (settings.json hooks: event, matcher group, command hook, timeout in seconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CLAUDE_CONFIG_DIR", ".claude") },
		File:      "settings.json",
		format:    nestedHooks{},
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PermissionRequest", ApprovalHookTimeout}, {"PostToolUse", 5}, {"PostToolUseFailure", 5},
			{"PermissionDenied", 5}, {"ElicitationResult", 5}, {"Notification", 5},
			{"Stop", 5}, {"StopFailure", 5}, {"SessionEnd", 5},
			{"SubagentStart", 5}, {"SubagentStop", 5},
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
		ID: OpenCode, Name: "opencode", Binary: "opencode", Version: 7, Reports: ReportsState,
		Source:    "https://opencode.ai/v2/docs/cli/plugins (V2 CLI plugin; V1 server entrypoint requires 1.18.29+)",
		ConfigDir: func(e Env) string { return e.xdgConfig("opencode") },
		File:      filepath.Join("plugins", "tuios-agent-state.js"),
		format:    ownedFile{render: renderOpenCodePlugin},
		extra: []extraFile{
			{file: filepath.Join("plugins", "tuios-agent-state", "index.js"), format: ownedFile{render: renderTemplate(openCodeV2IndexTemplate)}},
			{file: filepath.Join("plugins", "tuios-agent-state", "tui.js"), format: ownedFile{render: renderTemplate(openCodeV2TuiTemplate)}},
		},
		ownedDir: filepath.Join("plugins", "tuios-agent-state"),
	},
	{
		ID: Amp, Name: "Amp", Binary: "amp", Version: 3, Reports: ReportsState,
		Source:    "https://ampcode.com/manual/plugin-api (TypeScript plugins run by Bun from ~/.config/amp/plugins; session.start, agent.start, agent.end, tool.call, tool.result)",
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
		ID: Copilot, Name: "GitHub Copilot CLI", Binary: "copilot", Version: 2, Reports: ReportsState,
		Source:    "https://docs.github.com/en/copilot/reference/hooks-reference (every ~/.copilot/hooks/*.json is loaded; version 1, command hooks with bash, powershell and timeoutSec; PascalCase event names get snake_case payloads)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("COPILOT_HOME", ".copilot") },
		File:      filepath.Join("hooks", "tuios.json"),
		format: ownedFile{render: renderJSON(map[string]any{"version": 1}, func(cmd string, ev HookEvent) any {
			return map[string]any{"type": "command", "bash": cmd, "powershell": powershellCommand(cmd), "timeoutSec": ev.Timeout}
		})},
		Events: []HookEvent{
			{"SessionStart", 5}, {"UserPromptSubmit", 5}, {"PreToolUse", 5},
			{"PostToolUse", 5}, {"PostToolUseFailure", 5}, {"notification", 5},
			{"Stop", 5}, {"ErrorOccurred", 5}, {"SessionEnd", 5},
		},
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
		ID: CursorAgent, Name: "Cursor Agent", Binary: "cursor-agent", Version: 2, Reports: ReportsState,
		Source:    "https://cursor.com/docs/hooks (~/.cursor/hooks.json, version 1, each event a list of {command}; permission hooks block on an empty answer, so none is registered)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("CURSOR_CONFIG_DIR", ".cursor") },
		File:      "hooks.json",
		format: flatHooks{version1: true, entry: func(cmd string, _ HookEvent) map[string]any {
			return map[string]any{"command": cmd}
		}},
		Events: []HookEvent{
			{"sessionStart", 0}, {"beforeSubmitPrompt", 0}, {"postToolUse", 0},
			{"postToolUseFailure", 0}, {"stop", 0}, {"sessionEnd", 0},
		},
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
		ID: Kilo, Name: "Kilo", Binary: "kilo", Version: 7, Reports: ReportsState,
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
		ID: Pi, Name: "Pi", Binary: "pi", Version: 5, Reports: ReportsState,
		Source:    "herdr src/integration/assets/pi (TypeScript extensions load from ~/.pi/agent/extensions, or PI_CODING_AGENT_DIR/extensions)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("PI_CODING_AGENT_DIR", ".pi", "agent") },
		File:      filepath.Join("extensions", "tuios-agent-state.ts"),
		format:    ownedFile{render: renderTemplate(piExtensionTemplate)},
	},
	{
		ID: OMP, Name: "oh-my-pi", Binary: "omp", Version: 2, Reports: ReportsState,
		Source:    "https://github.com/can1357/oh-my-pi/blob/main/docs/extension-loading.md (TypeScript extensions load from ~/.omp/agent/extensions, or PI_CODING_AGENT_DIR/extensions)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("PI_CODING_AGENT_DIR", ".omp", "agent") },
		File:      filepath.Join("extensions", "tuios-omp-agent-state.ts"),
		format:    ownedFile{render: renderTemplate(ompExtensionTemplate)},
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
		ID: Qwen, Name: "Qwen Code", Binary: "qwen", Version: 2, Reports: ReportsState,
		Source:    "https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md (~/.qwen/settings.json hooks in Claude Code's nested shape, matcher *, a timeout of 1000 or more read as milliseconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("QWEN_HOME", ".qwen") },
		File:      "settings.json",
		format:    nestedHooks{matcher: "*"},
		Events: []HookEvent{
			{"SessionStart", 5000}, {"UserPromptSubmit", 5000}, {"PreToolUse", 5000},
			{"PermissionRequest", ApprovalHookTimeout * 1000}, {"PostToolUse", 5000},
			{"PostToolUseFailure", 5000}, {"PermissionDenied", 5000}, {"Notification", 5000},
			{"Stop", 5000}, {"StopFailure", 5000}, {"SessionEnd", 5000},
		},
	},
	{
		ID: Vibe, Name: "Mistral Vibe", Binary: "vibe", Version: 1, Reports: ReportsState,
		Source:    "https://github.com/mistralai/mistral-vibe ([[hooks]] tables in ~/.vibe/hooks.toml or $VIBE_HOME/hooks.toml: name, type, command, timeout in seconds)",
		ConfigDir: func(e Env) string { return e.dirFromEnv("VIBE_HOME", ".vibe") },
		File:      "hooks.toml",
		format:    tomlBlock{named: true},
		Events: []HookEvent{
			{"session_start", 5}, {"pre_agent", 5}, {"pre_tool", 5},
			{"post_tool", 5}, {"notification", 5}, {"post_agent", 5},
			{"session_end", 5},
		},
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
		{"goose", "its hooks have no event for a prompt that waits on the person (goose issue 12007), and a working report from a hook would outrank the screen that shows one"},
		{"kiro", "its CLI hooks live in per-agent files with no documented user-wide location or payload"},
		{"maki", "it has Lua plugins but no documented user-wide plugin file tuios could own without editing the user's init.lua"},
	}
}

// HookCommand is the command a managed hook entry runs. tuios is the program
// to run, normally "tuios" so an upgrade that moves the binary keeps working.
func HookCommand(tuios, harnessID string, version int) string {
	return shellWord(tuios) + " agent-hook " + harnessID + " " + managedMarker + " " + strconv.Itoa(version)
}

func shellWord(s string) string {
	if runtime.GOOS == "windows" {
		if s != "" && !strings.ContainsAny(s, " \t'\"\\$`&|;<>()*?[]{}!#~") {
			return s
		}
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return shquote.Word(s)
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
	Backup     string   `json:"backup,omitempty"`
	Notes      []string `json:"notes,omitempty"`
	notWritten []string
}

// ErrNoConfigDir is returned by Install when the harness has never run here.
var ErrNoConfigDir = errors.New("configuration directory not found")

type filePlan struct {
	path    string
	have    []byte
	out     []byte
	changed bool
	remove  bool
	owned   bool
}

func (t *Target) plan(env Env, tuios string, install bool) ([]filePlan, error) {
	dir := t.ConfigDir(env)
	var plans []filePlan
	for _, f := range t.files(env) {
		path := filepath.Join(dir, f.file)
		have, err := readOptional(path)
		if err != nil {
			return nil, err
		}
		out, changed, remove, err := f.format.apply(t, have, t.runner(env, tuios), install)
		switch {
		case errors.Is(err, errNotOurs):
			return nil, errfmt.Errorf("%s %w", path, err)
		case err != nil:
			return nil, errfmt.Errorf("failed to read %s: %w. It was left unchanged", path, err)
		}
		plans = append(plans, filePlan{path: path, have: have, out: out, changed: changed, remove: remove, owned: f.format.owned()})
	}
	return plans, nil
}

func (t *Target) carryOut(env Env, res *Result, plans []filePlan, removing bool) error {
	for i, p := range plans {
		if !p.changed {
			continue
		}
		var err error
		if p.remove {
			err = os.Remove(p.path)
		} else {
			err = writeAtomic(p.path, p.have, p.out)
		}
		if err != nil {
			for _, rest := range plans[i:] {
				if rest.changed {
					res.notWritten = append(res.notWritten, rest.path)
				}
			}
			return err
		}
		res.Changed = true
		res.Paths = append(res.Paths, p.path)
		if !p.owned && !p.remove && p.have != nil && res.Backup == "" {
			res.Backup = p.path + BackupSuffix
		}
	}
	if removing && t.ownedDir != "" {
		_ = os.Remove(filepath.Join(t.ConfigDir(env), t.ownedDir))
	}
	return nil
}

// Install writes this build's managed entries. It is idempotent: a second
// install with nothing changed writes nothing. Entries from an older version
// are replaced, and nothing that tuios did not write is touched.
func (t *Target) Install(env Env, tuios string) (Result, error) {
	return retryChanged(func() (Result, error) { return t.install(env, tuios) })
}

func (t *Target) install(env Env, tuios string) (Result, error) {
	dir := t.ConfigDir(env)
	res := Result{Harness: t.ID, Path: t.Path(env)}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return res, errfmt.Errorf("%w: %s. Install %s and run it once, then try again", ErrNoConfigDir, dir, t.Name)
	}
	if strings.TrimSpace(env.env("PI_CODING_AGENT_DIR")) != "" && (t.ID == Pi || t.ID == OMP) {
		other := Pi
		foreignDir := filepath.Join(env.Home, ".pi") + string(os.PathSeparator)
		if t.ID == Pi {
			other = OMP
			foreignDir = filepath.Join(env.Home, ".omp") + string(os.PathSeparator)
		}
		foreign, _ := LookupTarget(other)
		_, foreignErr := os.Stat(foreign.Path(env))
		owner, _ := Canonical(env.env(AgentHintEnv))
		_, ompConfigErr := os.Stat(filepath.Join(dir, "config.yml"))
		if owner == other || strings.HasPrefix(filepath.Clean(dir)+string(os.PathSeparator), foreignDir) ||
			foreignErr == nil || (t.ID == Pi && ompConfigErr == nil) {
			return res, errfmt.Errorf("%s is %s's agent directory. Refusing to install %s there. Unset PI_CODING_AGENT_DIR or choose the intended agent directory", dir, foreign.Name, t.Name)
		}
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
	return retryChanged(func() (Result, error) { return t.uninstall(env) })
}

func (t *Target) uninstall(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.Path(env)}
	plans, err := t.plan(env, "", false)
	if err != nil {
		return res, err
	}
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
	Reports         string `json:"reports"`
	ConfigDirExists bool   `json:"config_dir_exists"`
	Installed       bool   `json:"installed"`
	Current         bool   `json:"current"`
	Version         int    `json:"version,omitempty"`
	WantVersion     int    `json:"want_version"`
	Binary          string `json:"binary"`
	BinaryPath      string `json:"binary_path,omitempty"`
	// Program is the program the installed hooks run, as the file names it,
	// "" when nothing is installed or no program could be read from it.
	Program string `json:"program,omitempty"`
	// OtherProgram says the install is this build's version and is exactly
	// what tuios would write, except that it runs Program rather than the
	// program status was asked about: an install made with --command. It is
	// not out of date.
	OtherProgram bool `json:"other_program,omitempty"`
	// Differs says, in words, how an install of this build's version differs
	// from the one this tuios writes: a hook missing, a hook too many, or a
	// command of another shape. It is empty when the install is current or
	// carries another version, which the version itself explains.
	Differs []string `json:"differs,omitempty"`
	// Unreadable says a file of the integration could not be read or parsed,
	// so the rest of the status is not known. Notes says which file.
	Unreadable  bool     `json:"unreadable,omitempty"`
	TuiosOnPath bool     `json:"tuios_on_path"`
	Notes       []string `json:"notes,omitempty"`
	// MCP is the MCP server registration, for a harness tuios can register
	// one with. See mcp.go.
	MCP *MCPStatus `json:"mcp,omitempty"`
	// StatusLine is the status line slot, for a harness tuios can feed from
	// one (Claude Code). See statusline.go.
	StatusLine *StatusLineStatus `json:"status_line,omitempty"`
	// Skill is the skill stub, for a harness install writes one for. See
	// skill.go.
	Skill *SkillStatus `json:"skill,omitempty"`
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
	if t.SupportsStatusLine() {
		sl := t.StatusLineState(env, tuios)
		st.StatusLine = &sl
	}
	if t.SupportsSkill() {
		sk := t.SkillState(env)
		st.Skill = &sk
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
	files := make([][]byte, 0, len(t.files(env)))
	for _, f := range t.files(env) {
		path := filepath.Join(t.ConfigDir(env), f.file)
		have, err := readOptional(path)
		if err != nil {
			st.Notes = append(st.Notes, "cannot read "+path+": "+err.Error())
			st.Unreadable = true
			return st
		}
		files = append(files, have)
	}
	installed, current, version, err := t.stateOf(env, files, tuios)
	if err != nil {
		st.Notes = append(st.Notes, err.Error())
		st.Unreadable = true
		return st
	}
	st.Installed, st.Current, st.Version = installed, current, version
	if st.Current {
		st.Program = tuios
	} else if st.Installed {
		for _, prog := range installedPrograms(t, files) {
			if st.Program == "" {
				st.Program = prog
			}
			if _, cur, _, err := t.stateOf(env, files, prog); err == nil && cur {
				st.Program, st.OtherProgram = prog, true
				break
			}
		}
		if !st.OtherProgram && st.Version == st.WantVersion {
			st.Differs = t.differences(env, files, tuios)
		}
	}
	st.Notes = append(st.Notes, t.notes(env)...)
	return st
}

func (t *Target) stateOf(env Env, files [][]byte, tuios string) (installed, current bool, version int, err error) {
	allCurrent := true
	for i, f := range t.files(env) {
		ins, cur, v, err := f.format.state(t, files[i], t.runner(env, tuios))
		if err != nil {
			return false, false, 0, errfmt.Errorf("cannot parse %s: %w", filepath.Join(t.ConfigDir(env), f.file), err)
		}
		if i == 0 {
			version = v
		}
		installed = installed || ins
		allCurrent = allCurrent && cur
	}
	return installed, installed && allCurrent, version, nil
}

func (t *Target) differences(env Env, files [][]byte, tuios string) []string {
	run := t.runner(env, tuios)
	var out []string
	for i, f := range t.files(env) {
		if _, cur, _, err := f.format.state(t, files[i], run); err != nil || cur {
			continue
		}
		path := filepath.Join(t.ConfigDir(env), f.file)
		d, ok := f.format.(hookDiffer)
		if !ok || files[i] == nil {
			out = append(out, path+" is not the file this tuios writes")
			continue
		}
		words, err := d.differs(t, files[i], run)
		if err != nil || len(words) == 0 {
			out = append(out, path+" is not the file this tuios writes")
			continue
		}
		out = append(out, words...)
	}
	return out
}

func installedPrograms(t *Target, files [][]byte) []string {
	hook := regexp.MustCompile(`"((?:[^"\\]|\\.)*? agent-hook ` + regexp.QuoteMeta(t.ID) + ` ` + managedMarker + ` \d+'?)"`)
	var out []string
	add := func(p string) {
		p = unshellWord(strings.TrimPrefix(p, "& "))
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, data := range files {
		for _, m := range pluginProgramRe.FindAllSubmatch(data, -1) {
			var p string
			if jsonx.Unmarshal(m[1], &p) == nil {
				add(p)
			}
		}
		for _, m := range hook.FindAllSubmatch(data, -1) {
			var cmd string
			if jsonx.Unmarshal([]byte(`"`+string(m[1])+`"`), &cmd) == nil {
				add(hookProgram(cmd, t.ID))
			}
		}
	}
	return out
}

func hookProgram(cmd, id string) string {
	if strings.HasPrefix(cmd, "sh -c ") {
		words, err := splitWords(cmd)
		if err != nil || len(words) < 3 {
			return ""
		}
		cmd = words[2]
		if i := strings.LastIndex(cmd, "| exec "); i >= 0 {
			cmd = cmd[i+len("| exec "):]
		}
	}
	if i := strings.LastIndex(cmd, " agent-hook "+id+" "); i >= 0 {
		return cmd[:i]
	}
	return ""
}

var pluginProgramRe = regexp.MustCompile(`(?:const TUIOS|_TUIOS) = ("(?:[^"\\]|\\.)*")`)

func unshellWord(s string) string {
	switch {
	case len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'':
		return strings.ReplaceAll(s[1:len(s)-1], `'\''`, `'`)
	case len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"':
		return strings.ReplaceAll(s[1:len(s)-1], `\"`, `"`)
	}
	return s
}

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

func (t *Target) runner(env Env, tuios string) Runner {
	run := Runner{Program: tuios}
	if env.HookSocket != nil {
		run.Socket = env.HookSocket()
	}
	return run
}
