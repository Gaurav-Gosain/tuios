package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// These tests cover the integrations beyond the first four: every target's
// round trip, and each file shape's handling of the user's own content.

// userFiles is a file of the user's own for each target that edits a shared
// file, with something tuios must keep in it.
var userFiles = map[string]string{
	ClaudeCode:  userClaudeSettings,
	Codex:       `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`,
	GeminiCLI:   `{"general": {"vimMode": true}}`,
	Antigravity: `{"mine": {"PreInvocation": [{"type": "command", "command": "log-it", "timeout": 3}]}}`,
	Crush:       `{"$schema": "https://charm.land/crush.json", "hooks": {"PreToolUse": [{"name": "guard", "matcher": "^bash$", "command": "/usr/local/bin/guard && echo ok", "timeout": 10}]}, "options": {"tui": {"compact_mode": true}}}`,
	CursorAgent: `{"version": 1, "hooks": {"stop": [{"command": "./notify.sh"}], "sessionStart": [{"command": "./audit.sh"}]}}`,
	Devin:       `{"theme": "light", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "say bye"}]}]}}`,
	Droid:       `{"model": "sonnet", "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}]}}`,
	Qoder:       `{"permissions": {"allow": ["Read"]}}`,
	Qwen:        `{"general": {"checkpointing": {"enabled": true}}, "hooks": {"SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "qwen-log"}]}]}}`,
	Kimi:        "default_model = \"kimi-k2\"\n\n[[hooks]]\nevent = \"PreToolUse\"\nmatcher = \"Shell\"\ncommand = \".kimi/hooks/safety-check.sh\"\ntimeout = 10\n\n[loop_control]\nmax_steps = 50 # a comment\n",
}

// keeps says what of the user's file must survive install and uninstall.
func keeps(t *testing.T, id, got string) {
	t.Helper()
	want := map[string][]string{
		ClaudeCode:  {`"say done"`, `"theme": "dark"`},
		Codex:       {`"say done"`},
		GeminiCLI:   {`"vimMode": true`},
		Antigravity: {`"mine"`, `"log-it"`},
		Crush:       {`"$schema"`, `"guard"`, `/usr/local/bin/guard && echo ok`, `"compact_mode": true`},
		CursorAgent: {`"./notify.sh"`, `"./audit.sh"`},
		Devin:       {`"say bye"`, `"theme": "light"`},
		Droid:       {`"echo hi"`, `"model": "sonnet"`},
		Qoder:       {`"Read"`},
		Qwen:        {`"qwen-log"`, `"checkpointing"`},
		Kimi:        {"default_model = \"kimi-k2\"", "safety-check.sh", "max_steps = 50 # a comment"},
	}[id]
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("%s lost %q:\n%s", id, w, got)
		}
	}
}

// TestEveryTargetRoundTrips installs, reinstalls, reports and uninstalls every
// integration in a home of its own, with the user's own file in place where
// the integration shares one.
func TestEveryTargetRoundTrips(t *testing.T) {
	for _, tg := range Targets() {
		t.Run(tg.ID, func(t *testing.T) {
			env := testEnv(t)
			if err := os.MkdirAll(tg.ConfigDir(env), 0o755); err != nil {
				t.Fatal(err)
			}
			user, shared := userFiles[tg.ID]
			if shared {
				writeFile(t, tg.Path(env), user)
			}
			if st := tg.Status(env, "tuios"); st.Installed || st.Current || !st.ConfigDirExists {
				t.Fatalf("status before install: %+v", st)
			}
			res, err := tg.Install(env, "tuios")
			if err != nil || !res.Changed {
				t.Fatalf("install: %+v %v", res, err)
			}
			if shared && res.Backup != tg.Path(env)+BackupSuffix {
				t.Fatalf("install of a shared file reported no backup: %+v", res)
			}
			installed := readFile(t, tg.Path(env))
			if !strings.Contains(installed, "agent-hook") {
				t.Fatalf("nothing of tuios's in %s:\n%s", tg.Path(env), installed)
			}
			if strings.Contains(installed, "__TUIOS_") {
				t.Fatalf("a placeholder was left in %s", tg.Path(env))
			}
			if shared {
				keeps(t, tg.ID, installed)
			}
			st := tg.Status(env, "tuios")
			if !st.Installed || !st.Current || st.Version != tg.Version || st.Reports != tg.Reports {
				t.Fatalf("status after install: %+v", st)
			}
			if other := tg.Status(env, "/opt/other/tuios"); other.Current {
				t.Fatalf("an install for another binary read as current: %+v", other)
			}
			snapshot := map[string]string{}
			for _, p := range tg.Paths(env) {
				snapshot[p] = readFile(t, p)
			}
			if res, err := tg.Install(env, "tuios"); err != nil || res.Changed {
				t.Fatalf("second install: %+v %v", res, err)
			}
			for p, want := range snapshot {
				if readFile(t, p) != want {
					t.Fatalf("a second install rewrote %s", p)
				}
			}
			res, err = tg.Uninstall(env)
			if err != nil || !res.Changed {
				t.Fatalf("uninstall: %+v %v", res, err)
			}
			if st := tg.Status(env, "tuios"); st.Installed {
				t.Fatalf("status after uninstall: %+v", st)
			}
			if shared {
				after := readFile(t, tg.Path(env))
				keeps(t, tg.ID, after)
				if strings.Contains(after, "agent-hook") {
					t.Fatalf("uninstall left tuios's entries:\n%s", after)
				}
			} else if _, err := os.Stat(tg.Path(env)); !os.IsNotExist(err) {
				t.Fatalf("uninstall left %s behind", tg.Path(env))
			}
			if res, err := tg.Uninstall(env); err != nil || res.Changed {
				t.Fatalf("second uninstall: %+v %v", res, err)
			}
		})
	}
}

// TestJSONTargetsGiveBackTheUsersFile checks install then uninstall of a JSON
// file returns the same JSON the user had.
func TestJSONTargetsGiveBackTheUsersFile(t *testing.T) {
	for _, id := range []string{Antigravity, Crush, CursorAgent, Devin, Droid, Qoder, Qwen} {
		t.Run(id, func(t *testing.T) {
			env := testEnv(t)
			tg := mustTarget(t, id)
			writeFile(t, tg.Path(env), userFiles[id])
			if _, err := tg.Install(env, "tuios"); err != nil {
				t.Fatal(err)
			}
			if _, err := tg.Uninstall(env); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, tg.Path(env)); !sameJSON([]byte(userFiles[id]), []byte(got)) {
				t.Fatalf("uninstall did not give back the user's file:\n%s", got)
			}
		})
	}
}

func TestCrushWritesAFlatHookAfterTheUsers(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, Crush)
	writeFile(t, tg.Path(env), userFiles[Crush])
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, tg.Path(env))
	want := `{"command":"tuios agent-hook crush --integration 1","name":"tuios","timeout":5}`
	if !strings.Contains(compactJSON(t, got), want) {
		t.Fatalf("crush.json has no flat tuios hook:\n%s", got)
	}
	if strings.Index(got, "guard") > strings.Index(got, "agent-hook") {
		t.Fatal("the tuios hook went before the user's")
	}
	if strings.Contains(got, `"hooks": [`) && strings.Contains(got, `"type": "command"`) {
		t.Fatalf("crush.json got Claude Code's nested shape:\n%s", got)
	}
}

func TestCursorHooksFileGetsAVersion(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, CursorAgent)
	if err := os.MkdirAll(tg.ConfigDir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	got := compactJSON(t, readFile(t, tg.Path(env)))
	if got != `{"hooks":{"sessionStart":[{"command":"tuios agent-hook cursor-agent --integration 1"}]},"version":1}` {
		t.Fatalf("hooks.json = %s", got)
	}
}

func TestAntigravityOwnsOneNamedBlock(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, Antigravity)
	writeFile(t, tg.Path(env), userFiles[Antigravity])
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	got := compactJSON(t, readFile(t, tg.Path(env)))
	if !strings.Contains(got, `"tuios":{"PreInvocation":[{"command":"tuios agent-hook antigravity --integration 1","timeout":5,"type":"command"}]}`) {
		t.Fatalf("hooks.json = %s", got)
	}

	// A block named tuios that tuios did not write is refused and kept.
	own := `{"tuios": {"PreInvocation": [{"type": "command", "command": "my-script"}]}}`
	writeFile(t, tg.Path(env), own)
	if _, err := tg.Install(env, "tuios"); err == nil {
		t.Fatal("installed over the user's own tuios block")
	}
	if res, err := tg.Uninstall(env); err != nil || res.Changed {
		t.Fatalf("uninstall removed the user's block: %+v %v", res, err)
	}
	if readFile(t, tg.Path(env)) != own {
		t.Fatal("the user's block changed")
	}
}

func TestOwnedJSONHookFilesAreWholeFiles(t *testing.T) {
	cases := map[string]string{
		Copilot: `{"hooks":{"SessionStart":[{"bash":"tuios agent-hook copilot --integration 1","powershell":"tuios agent-hook copilot --integration 1","timeoutSec":5,"type":"command"}]},"version":1}`,
		Grok:    `{"hooks":{"SessionStart":[{"hooks":[{"command":"tuios agent-hook grok --integration 1","timeout":5,"type":"command"}]}]}}`,
	}
	for id, want := range cases {
		t.Run(id, func(t *testing.T) {
			env := testEnv(t)
			tg := mustTarget(t, id)
			mine := filepath.Join(filepath.Dir(tg.Path(env)), "mine.json")
			writeFile(t, mine, `{"hooks":{}}`)
			if _, err := tg.Install(env, "tuios"); err != nil {
				t.Fatal(err)
			}
			if got := compactJSON(t, readFile(t, tg.Path(env))); got != want {
				t.Fatalf("%s = %s\nwant %s", tg.Path(env), got, want)
			}
			if _, err := tg.Uninstall(env); err != nil {
				t.Fatal(err)
			}
			if readFile(t, mine) != `{"hooks":{}}` {
				t.Fatal("uninstall touched another hook file")
			}
			// A file of the user's at tuios's path is never overwritten.
			writeFile(t, tg.Path(env), `{"hooks":{"SessionStart":[]}}`)
			if _, err := tg.Install(env, "tuios"); err == nil {
				t.Fatal("overwrote a file tuios did not write")
			}
			if res, err := tg.Uninstall(env); err != nil || res.Changed {
				t.Fatalf("uninstall removed a file tuios did not write: %+v %v", res, err)
			}
		})
	}
}

func TestKimiBlockKeepsTheUsersTOML(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, Kimi)
	user := userFiles[Kimi]
	writeFile(t, tg.Path(env), user)
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, tg.Path(env))
	if !strings.HasPrefix(got, user) {
		t.Fatalf("install changed the user's part of config.toml:\n%s", got)
	}
	if n := strings.Count(got, "[[hooks]]"); n != 1+len(tg.Events) {
		t.Fatalf("%d [[hooks]] tables, want the user's one and %d", n, len(tg.Events))
	}
	if !strings.Contains(got, `command = "tuios agent-hook kimi --integration 1"`) {
		t.Fatalf("config.toml:\n%s", got)
	}
	if _, err := tg.Uninstall(env); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, tg.Path(env)); got != user {
		t.Fatalf("uninstall did not give back the user's file byte for byte:\n%q\nwant\n%q", got, user)
	}

	inline := "hooks = []\n"
	writeFile(t, tg.Path(env), inline)
	if _, err := tg.Install(env, "tuios"); err == nil || !strings.Contains(err.Error(), "inline") {
		t.Fatalf("installed next to an inline hooks array: %v", err)
	}
	if readFile(t, tg.Path(env)) != inline {
		t.Fatal("a refused install changed the file")
	}
}

// TestSharedFilesStayLinked checks a config a dotfile manager links into place
// stays a link through install and uninstall, for the TOML and flat JSON shapes
// as for Claude Code's.
func TestSharedFilesStayLinked(t *testing.T) {
	for _, id := range []string{Kimi, Crush, Qwen} {
		t.Run(id, func(t *testing.T) {
			env := testEnv(t)
			tg := mustTarget(t, id)
			real := filepath.Join(t.TempDir(), "dotfiles", "config")
			writeFile(t, real, userFiles[id])
			if err := os.MkdirAll(tg.ConfigDir(env), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, tg.Path(env)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := tg.Install(env, "tuios"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readFile(t, real), "agent-hook") {
				t.Fatal("install did not write through the link")
			}
			if _, err := tg.Uninstall(env); err != nil {
				t.Fatal(err)
			}
			if fi, err := os.Lstat(tg.Path(env)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatal("the link was replaced by a regular file")
			}
		})
	}
}

func TestTOMLStringEscapes(t *testing.T) {
	if got := tomlString(`C:\Program Files\tuios "x"`); got != `"C:\\Program Files\\tuios \"x\""` {
		t.Fatalf("tomlString = %s", got)
	}
}

func TestHermesTurnsThePluginOn(t *testing.T) {
	cases := []struct {
		name, before, installed string
	}{
		{"no file", "", "plugins:\n  enabled:\n    - tuios-agent-state\n"},
		{"no plugins key", "model: x\n", "model: x\nplugins:\n  enabled:\n    - tuios-agent-state\n"},
		{"empty flow list", "plugins:\n  enabled: []\nmodel: x\n", "plugins:\n  enabled:\n    - tuios-agent-state\nmodel: x\n"},
		{"a list of the user's", "plugins:\n  enabled:\n    - mine # keep\n  disabled: []\nmodel: x\n", "plugins:\n  enabled:\n    - mine # keep\n    - tuios-agent-state\n  disabled: []\nmodel: x\n"},
		{"compact list", "plugins:\n    enabled:\n    - mine\n", "plugins:\n    enabled:\n    - mine\n    - tuios-agent-state\n"},
		{"no enabled key", "plugins:\n  disabled: []\n", "plugins:\n  enabled:\n    - tuios-agent-state\n  disabled: []\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			tg := mustTarget(t, Hermes)
			config := filepath.Join(tg.ConfigDir(env), "config.yaml")
			if err := os.MkdirAll(tg.ConfigDir(env), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.before != "" {
				writeFile(t, config, tc.before)
			}
			res, err := tg.Install(env, "tuios")
			if err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, config); got != tc.installed {
				t.Fatalf("config.yaml after install:\n%q\nwant\n%q", got, tc.installed)
			}
			if len(res.Paths) != 3 {
				t.Fatalf("install wrote %v, want the plugin, its manifest and the config", res.Paths)
			}
			manifest := readFile(t, filepath.Join(tg.ConfigDir(env), "plugins", "tuios-agent-state", "plugin.yaml"))
			if !strings.Contains(manifest, "name: tuios-agent-state") {
				t.Fatalf("plugin.yaml:\n%s", manifest)
			}
			if _, err := tg.Uninstall(env); err != nil {
				t.Fatal(err)
			}
			after := readFile(t, config)
			if strings.Contains(after, "tuios-agent-state") {
				t.Fatalf("uninstall left the plugin on:\n%s", after)
			}
			if tc.before != "" && strings.Contains(tc.before, "mine") && !strings.Contains(after, "mine # keep") && !strings.Contains(after, "- mine") {
				t.Fatalf("uninstall lost the user's plugin:\n%s", after)
			}
			if _, err := os.Stat(filepath.Join(tg.ConfigDir(env), "plugins", "tuios-agent-state")); !os.IsNotExist(err) {
				t.Fatal("uninstall left the plugin directory")
			}
		})
	}
}

func TestHermesRefusesAnInlineList(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, Hermes)
	config := filepath.Join(tg.ConfigDir(env), "config.yaml")
	writeFile(t, config, "plugins:\n  enabled: [mine]\n")
	_, err := tg.Install(env, "tuios")
	if err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("install into an inline list: %v", err)
	}
	if _, err := os.Stat(tg.Path(env)); !os.IsNotExist(err) {
		t.Fatal("a refused install still wrote the plugin")
	}
}

// TestOpenCodePluginIsUnchanged pins the opencode plugin tuios writes to the
// bytes the version 3 release of it wrote, the one that feeds the model and
// cost to the pane's agent metadata. Kilo shares its template, and an install that
// renders differently would read as out of date and be rewritten for every
// user although nothing about it changed. A change here goes with a version
// bump for both.
func TestOpenCodePluginIsUnchanged(t *testing.T) {
	tg := mustTarget(t, OpenCode)
	for cmd, want := range map[string]string{
		"tuios":               "58bdce7dbdf055982573d6f7ce02e9957e58650999e9f1171d27e5b7c8b77e76",
		"/opt/my tuios/tuios": "3e844bb67f86ca5869265c0050f4fca7eb64e81bd2cc0136e18de31b66fed42b",
	} {
		sum := sha256.Sum256(tg.format.(ownedFile).render(tg, cmd))
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("the opencode plugin for %q changed: sha256 %s", cmd, got)
		}
	}
	kilo := string(mustTarget(t, Kilo).format.(ownedFile).render(mustTarget(t, Kilo), "tuios"))
	for _, want := range []string{`["agent-hook", "kilo", "--integration", "3"]`, "TUIOS_INTEGRATION_ID=kilo", "Reports Kilo's session state"} {
		if !strings.Contains(kilo, want) {
			t.Errorf("the Kilo plugin does not say %q", want)
		}
	}
}

// TestManifestIDsMatchTheBundledManifests keeps the list TUIOS_AGENT is read
// against complete, and every integration tied to a manifest.
func TestManifestIDsMatchTheBundledManifests(t *testing.T) {
	reg, errs := harness.Load()
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	ids := reg.IDs()
	slices.Sort(ids)
	got := slices.Clone(ManifestIDs)
	slices.Sort(got)
	if !slices.Equal(ids, got) {
		t.Fatalf("ManifestIDs = %v\nmanifests  = %v", got, ids)
	}
	covered := map[string]bool{}
	for _, tg := range Targets() {
		if !slices.Contains(ids, tg.ID) {
			t.Errorf("integration %s has no manifest", tg.ID)
		}
		if id, ok := Canonical(tg.ID); !ok || id != tg.ID {
			t.Errorf("Canonical(%s) = %s %v", tg.ID, id, ok)
		}
		if !slices.Contains(HarnessIDs(), tg.ID) {
			t.Errorf("%s has an installer and is not in HarnessIDs", tg.ID)
		}
		if tg.Reports != ReportsState && tg.Reports != ReportsSession {
			t.Errorf("%s reports %q", tg.ID, tg.Reports)
		}
		covered[tg.ID] = true
	}
	for _, u := range UnsupportedHarnesses() {
		if covered[u.Harness] || !slices.Contains(ids, u.Harness) {
			t.Errorf("unsupported %s is an integration, or not a manifest", u.Harness)
		}
		covered[u.Harness] = true
	}
	for _, id := range ids {
		if !covered[id] {
			t.Errorf("manifest %s has neither an integration nor a reason it has none", id)
		}
	}
}

// TestIdentityTargetsReportSessionsOnly holds the split: a session target's
// every fixture report is identity only, and a state target reports states.
func TestIdentityTargetsReportSessionsOnly(t *testing.T) {
	for _, tg := range Targets() {
		for _, tc := range loadHookCases(t, tg.ID) {
			if tc.Want == nil {
				continue
			}
			if tg.Reports == ReportsSession && (!tc.Want.SessionOnly || tc.Want.State != "") {
				t.Errorf("%s/%s: a session integration reports %+v", tg.ID, tc.Name, *tc.Want)
			}
		}
	}
}

func TestStdoutAnswer(t *testing.T) {
	for id, want := range map[string]string{GeminiCLI: "{}\n", Antigravity: "{}\n", "agy": "{}\n", ClaudeCode: "", Kimi: "", Crush: ""} {
		if got := StdoutAnswer(id); got != want {
			t.Errorf("StdoutAnswer(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestNewTargetsNeedTheirConfigDir(t *testing.T) {
	env := testEnv(t)
	for _, id := range []string{Amp, Pi, Hermes, Kimi, Crush} {
		if _, err := mustTarget(t, id).Install(env, "tuios"); !errors.Is(err, ErrNoConfigDir) {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestConfigDirOverrides(t *testing.T) {
	env := testEnv(t)
	vars := map[string]string{
		"XDG_CONFIG_HOME":            "/x",
		"PI_CODING_AGENT_DIR":        "/pi",
		"KIMI_CODE_HOME":             "/kimi",
		"QWEN_HOME":                  "/qwen",
		"QODER_CONFIG_DIR":           "/qoder",
		"COPILOT_HOME":               "/copilot",
		"CURSOR_CONFIG_DIR":          "/cursor",
		"GROK_HOME":                  "/grok",
		"HERMES_HOME":                "/hermes",
		"ANTIGRAVITY_CLI_CONFIG_DIR": "/agy",
	}
	env.Getenv = func(k string) string { return vars[k] }
	want := map[string]string{
		Amp:         "/x/amp/plugins/tuios-agent-state.ts",
		Kilo:        "/x/kilo/plugin/tuios-agent-state.js",
		Crush:       "/x/crush/crush.json",
		Devin:       "/x/devin/config.json",
		Pi:          "/pi/extensions/tuios-agent-state.ts",
		Kimi:        "/kimi/config.toml",
		Qwen:        "/qwen/settings.json",
		Qoder:       "/qoder/settings.json",
		Copilot:     "/copilot/hooks/tuios.json",
		CursorAgent: "/cursor/hooks.json",
		Grok:        "/grok/hooks/tuios.json",
		Hermes:      "/hermes/plugins/tuios-agent-state/__init__.py",
		Antigravity: "/agy/hooks.json",
	}
	for id, path := range want {
		if got := mustTarget(t, id).Path(env); got != filepath.FromSlash(path) {
			t.Errorf("%s path = %s, want %s", id, got, path)
		}
	}
}

type jsonRaw = json.RawMessage

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func compactRaw(raw []byte) []byte {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

// compactJSON writes a document on one line with every object's keys sorted,
// so a test can compare it with a literal.
func compactJSON(t *testing.T, s string) string {
	t.Helper()
	root, err := parseObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	var sorted func(raw []byte) string
	sorted = func(raw []byte) string {
		o, err := parseObject(raw)
		if err != nil {
			var items []jsonRaw
			if jsonUnmarshal(raw, &items) == nil {
				parts := make([]string, len(items))
				for i, it := range items {
					parts[i] = sorted(it)
				}
				return "[" + strings.Join(parts, ",") + "]"
			}
			return string(compactRaw(raw))
		}
		keys := slices.Clone(o.keys)
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = `"` + k + `":` + sorted(o.vals[k])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return sorted(root.compact())
}
