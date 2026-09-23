package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv is a home directory of its own, with no environment overrides and
// every binary reported on PATH.
func testEnv(t *testing.T) Env {
	t.Helper()
	return Env{
		Home:     t.TempDir(),
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
	}
}

func mustTarget(t *testing.T, id string) *Target {
	t.Helper()
	tg, ok := LookupTarget(id)
	if !ok {
		t.Fatalf("no target %s", id)
	}
	return tg
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// userClaudeSettings is a settings file with the user's own configuration:
// keys tuios knows nothing about, a hook of their own on an event tuios also
// uses, a group mixing their hook with nothing of tuios's, and the older shim.
const userClaudeSettings = `{
  "theme": "dark",
  "permissions": {"allow": ["Bash(go test:*)"], "deny": []},
  "hooks": {
    "Stop": [
      {"matcher": "", "hooks": [{"type": "command", "command": "say done", "timeout": 3}]}
    ],
    "PreCompact": [
      {"hooks": [{"type": "command", "command": "~/bin/log-compact"}]}
    ]
  },
  "model": "opus",
  "env": {"FOO": "1"}
}
`

// TestClaudeInstallRoundTripKeepsTheUsersSettings installs, reinstalls and
// uninstalls against a real settings file and checks every step leaves the
// user's own entries exactly where they were.
func TestClaudeInstallRoundTripKeepsTheUsersSettings(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, "claude")
	path := tg.Path(env)
	writeFile(t, path, userClaudeSettings)

	res, err := tg.Install(env, "tuios")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Changed || res.Backup != path+BackupSuffix {
		t.Fatalf("install result %+v", res)
	}
	if got := readFile(t, path+BackupSuffix); got != userClaudeSettings {
		t.Fatalf("backup is not the file as it was:\n%s", got)
	}
	installed := readFile(t, path)

	// The user's keys keep their order and values.
	order := []string{`"theme"`, `"permissions"`, `"hooks"`, `"model"`, `"env"`}
	last := -1
	for _, k := range order {
		i := strings.Index(installed, k)
		if i < last {
			t.Fatalf("key %s moved:\n%s", k, installed)
		}
		last = i
	}
	for _, want := range []string{`"say done"`, `"~/bin/log-compact"`, `"Bash(go test:*)"`, `"FOO": "1"`} {
		if !strings.Contains(installed, want) {
			t.Fatalf("install lost %s:\n%s", want, installed)
		}
	}
	// One managed command per event, and the user's Stop hook still first.
	entries, err := findManaged([]byte(installed))
	if err != nil {
		t.Fatal(err)
	}
	if !managedCurrent(entries, tg.Events, HookCommand("tuios", ClaudeCode, tg.Version)) {
		t.Fatalf("managed entries are not current: %+v", entries)
	}
	if strings.Index(installed, "say done") > strings.Index(installed, "agent-hook claude-code") {
		t.Fatal("the managed Stop hook was put before the user's")
	}

	st := tg.Status(env, "tuios")
	if !st.Installed || !st.Current || st.Version != tg.Version {
		t.Fatalf("status after install: %+v", st)
	}

	// Installing again changes nothing and writes nothing.
	res, err = tg.Install(env, "tuios")
	if err != nil || res.Changed {
		t.Fatalf("second install: %+v %v", res, err)
	}
	if readFile(t, path) != installed {
		t.Fatal("a second install rewrote the file")
	}

	res, err = tg.Uninstall(env)
	if err != nil || !res.Changed {
		t.Fatalf("uninstall: %+v %v", res, err)
	}
	after := readFile(t, path)
	if !sameJSON([]byte(userClaudeSettings), []byte(after)) {
		t.Fatalf("uninstall did not give back the user's file:\n%s", after)
	}
	if st := tg.Status(env, "tuios"); st.Installed {
		t.Fatalf("status after uninstall: %+v", st)
	}
	// Uninstalling twice is not an error and changes nothing.
	if res, err := tg.Uninstall(env); err != nil || res.Changed {
		t.Fatalf("second uninstall: %+v %v", res, err)
	}
}

func TestInstallReplacesAnOlderVersion(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	old := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"tuios agent-hook claude-code --integration 0","timeout":5}]}],` +
		`"Notification":[{"hooks":[{"type":"command","command":"tuios agent-hook claude-code --integration 0"},{"type":"command","command":"notify-send hi"}]}]}}`
	writeFile(t, tg.Path(env), old)

	st := tg.Status(env, "tuios")
	if !st.Installed || st.Current || st.Version != 0 {
		t.Fatalf("status of an old install: %+v", st)
	}
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, tg.Path(env))
	if strings.Contains(got, "--integration 0") {
		t.Fatalf("the old entry survived:\n%s", got)
	}
	if !strings.Contains(got, "notify-send hi") {
		t.Fatalf("the user's hook in a shared group was lost:\n%s", got)
	}
	if st := tg.Status(env, "tuios"); !st.Current {
		t.Fatalf("status after upgrade: %+v", st)
	}
	// Pointing at another binary reads as not current.
	if st := tg.Status(env, "/opt/tuios/bin/tuios"); st.Current {
		t.Fatalf("an install for another binary read as current: %+v", st)
	}
}

func TestInstallRefusesAFileItCannotRead(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	broken := "{ \"theme\": \"dark\", // a comment\n}"
	writeFile(t, tg.Path(env), broken)
	if _, err := tg.Install(env, "tuios"); err == nil {
		t.Fatal("installed into a file it could not parse")
	}
	if readFile(t, tg.Path(env)) != broken {
		t.Fatal("a failed install changed the file")
	}
}

func TestInstallNeedsTheHarnessConfigDir(t *testing.T) {
	env := testEnv(t)
	for _, tg := range Targets() {
		if _, err := tg.Install(env, "tuios"); !errors.Is(err, ErrNoConfigDir) {
			t.Errorf("%s: install with no config dir: %v", tg.ID, err)
		}
	}
}

func TestClaudeConfigDirOverride(t *testing.T) {
	env := testEnv(t)
	custom := filepath.Join(env.Home, "elsewhere")
	env.Getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return custom
		}
		return ""
	}
	if got := mustTarget(t, ClaudeCode).Path(env); got != filepath.Join(custom, "settings.json") {
		t.Fatalf("path = %s", got)
	}
}

func TestCodexInstallCreatesHooksFileAndNotesDisabledHooks(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, Codex)
	dir := tg.ConfigDir(env)
	config := "model = \"gpt-5\"\n\n[features]\nhooks = false # off for now\n"
	writeFile(t, filepath.Join(dir, "config.toml"), config)

	res, err := tg.Install(env, "tuios")
	if err != nil || !res.Changed || res.Backup != "" {
		t.Fatalf("install: %+v %v", res, err)
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "hooks = false") {
		t.Fatalf("notes = %v, want the disabled-hooks note", res.Notes)
	}
	if readFile(t, filepath.Join(dir, "config.toml")) != config {
		t.Fatal("install edited config.toml")
	}
	got := readFile(t, tg.Path(env))
	if !strings.Contains(got, `"Interrupt"`) || !strings.Contains(got, "agent-hook codex --integration 1") {
		t.Fatalf("hooks.json:\n%s", got)
	}
	if res, err := tg.Uninstall(env); err != nil || !res.Changed {
		t.Fatalf("uninstall: %+v %v", res, err)
	}
	if got := readFile(t, tg.Path(env)); !sameJSON([]byte("{}"), []byte(got)) {
		t.Fatalf("uninstall left:\n%s", got)
	}
}

func TestGeminiInstallUsesMilliseconds(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, "gemini")
	writeFile(t, tg.Path(env), `{"general": {"vimMode": true}}`)
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, tg.Path(env))
	if !strings.Contains(got, `"timeout": 5000`) || !strings.Contains(got, `"vimMode": true`) {
		t.Fatalf("settings.json:\n%s", got)
	}
	if _, err := tg.Uninstall(env); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, tg.Path(env)); !sameJSON([]byte(`{"general": {"vimMode": true}}`), []byte(got)) {
		t.Fatalf("after uninstall:\n%s", got)
	}
}

func TestOpenCodePluginRoundTrip(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, OpenCode)
	dir := tg.ConfigDir(env)
	other := filepath.Join(dir, "plugins", "mine.js")
	writeFile(t, other, "export const Mine = async () => ({})\n")

	res, err := tg.Install(env, "/opt/my tuios/tuios")
	if err != nil || !res.Changed {
		t.Fatalf("install: %+v %v", res, err)
	}
	plugin := readFile(t, tg.Path(env))
	if !strings.Contains(plugin, `const TUIOS = "/opt/my tuios/tuios";`) || !strings.Contains(plugin, "TUIOS_INTEGRATION_VERSION=2") {
		t.Fatalf("plugin was not filled in:\n%s", plugin[:400])
	}
	if strings.Contains(plugin, "__TUIOS_") {
		t.Fatal("a placeholder was left in the plugin")
	}
	if st := tg.Status(env, "/opt/my tuios/tuios"); !st.Installed || !st.Current || st.Version != 2 {
		t.Fatalf("status: %+v", st)
	}
	if res, err := tg.Install(env, "/opt/my tuios/tuios"); err != nil || res.Changed {
		t.Fatalf("second install: %+v %v", res, err)
	}
	if res, err := tg.Uninstall(env); err != nil || !res.Changed {
		t.Fatalf("uninstall: %+v %v", res, err)
	}
	if _, err := os.Stat(tg.Path(env)); !os.IsNotExist(err) {
		t.Fatal("the plugin is still there")
	}
	if readFile(t, other) == "" {
		t.Fatal("uninstall touched another plugin")
	}
}

func TestOpenCodeLeavesAFileItDidNotWrite(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, OpenCode)
	writeFile(t, tg.Path(env), "// the user's own file\n")
	if _, err := tg.Install(env, "tuios"); err == nil {
		t.Fatal("overwrote a file tuios did not write")
	}
	if res, err := tg.Uninstall(env); err != nil || res.Changed {
		t.Fatalf("uninstall removed a file tuios did not write: %+v %v", res, err)
	}
}

func TestStatusNotesTheOldShim(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	writeFile(t, tg.Path(env), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/.config/tuios/integrations/tuios-agent-state.sh"}]}]}}`)
	st := tg.Status(env, "tuios")
	if st.Installed || len(st.Notes) != 1 || !strings.Contains(st.Notes[0], "tuios-agent-state.sh") {
		t.Fatalf("status: %+v", st)
	}
}

func TestHookCommandQuotesAPathWithSpaces(t *testing.T) {
	got := HookCommand("/Applications/My Tools/tuios", ClaudeCode, 1)
	want := "'/Applications/My Tools/tuios' agent-hook claude-code --integration 1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !isManagedCommand(got) {
		t.Fatal("a quoted command does not read as managed")
	}
	if isManagedCommand("tuios agent-hook claude-code") {
		t.Fatal("a hand-written command without the marker read as managed")
	}
}

func TestCodexHooksDisabled(t *testing.T) {
	cases := map[string]bool{
		"":                                    false,
		"[features]\nhooks = true\n":          false,
		"[features]\nhooks = false\n":         true,
		"features.hooks = false\n":            true,
		"[other]\nhooks = false\n":            false,
		"[features]\n# hooks = false\n":       false,
		"[features]\nweb_search = false\n":    false,
		"[features]\nhooks=false\n[x]\na=1\n": true,
	}
	for in, want := range cases {
		if got := codexHooksDisabled(in); got != want {
			t.Errorf("codexHooksDisabled(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestInstallKeepsTheBytesOfTheUsersCommands checks a user's hook command with
// &, < and > survives install and uninstall as the user wrote it. json.Marshal
// escapes those three even inside a json.RawMessage, which turned
// `make lint && echo ok > /tmp/x` into `make lint && echo ok > /tmp/x`.
func TestInstallKeepsTheBytesOfTheUsersCommands(t *testing.T) {
	env := testEnv(t)
	for _, id := range []string{ClaudeCode, Codex, GeminiCLI} {
		t.Run(id, func(t *testing.T) {
			tg := mustTarget(t, id)
			path := tg.Path(env)
			event := tg.Events[0].Name
			cmd := `make lint && echo ok > /tmp/x < /dev/null`
			writeFile(t, path, `{"hooks":{"`+event+`":[{"hooks":[{"type":"command","command":"`+cmd+`"}]}],"Other<&>":[]}}`)
			if _, err := tg.Install(env, "tuios"); err != nil {
				t.Fatal(err)
			}
			for _, step := range []string{"install", "uninstall"} {
				if step == "uninstall" {
					if _, err := tg.Uninstall(env); err != nil {
						t.Fatal(err)
					}
				}
				got := readFile(t, path)
				if !strings.Contains(got, `"command": "`+cmd+`"`) || !strings.Contains(got, `"Other<&>"`) {
					t.Fatalf("%s rewrote the user's text:\n%s", step, got)
				}
				if strings.Contains(got, `\u00`) {
					t.Fatalf("%s escaped characters the user wrote plainly:\n%s", step, got)
				}
			}
		})
	}
}

// TestInstallWritesThroughASymlink checks a settings file a dotfile manager
// links into place stays a link, and the change lands in the linked file.
func TestInstallWritesThroughASymlink(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	path := tg.Path(env)
	real := filepath.Join(t.TempDir(), "dotfiles", "claude-settings.json")
	writeFile(t, real, userClaudeSettings)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, step := range []string{"install", "uninstall"} {
		var err error
		if step == "install" {
			_, err = tg.Install(env, "tuios")
		} else {
			_, err = tg.Uninstall(env)
		}
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s replaced the symlink with a regular file", step)
		}
		managed := strings.Contains(readFile(t, real), "agent-hook")
		if managed != (step == "install") {
			t.Fatalf("after %s the linked file reads:\n%s", step, readFile(t, real))
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(real))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tuios-") {
			t.Fatalf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestInstallRefusesADanglingSymlink checks a link to a missing file is left
// alone rather than replaced by a regular file.
func TestInstallRefusesADanglingSymlink(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	path := tg.Path(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing.json"), path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := tg.Install(env, "tuios"); err == nil {
		t.Fatal("installed through a dangling symlink")
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the dangling symlink was replaced")
	}
}

// TestBackupKeepsTheFileFromBeforeTuios checks a second write that changes the
// file does not overwrite the backup of the original.
func TestBackupKeepsTheFileFromBeforeTuios(t *testing.T) {
	env := testEnv(t)
	tg := mustTarget(t, ClaudeCode)
	path := tg.Path(env)
	writeFile(t, path, userClaudeSettings)
	if _, err := tg.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.Uninstall(env); err != nil {
		t.Fatal(err)
	}
	if _, err := tg.Install(env, "/opt/tuios/bin/tuios"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path+BackupSuffix); got != userClaudeSettings {
		t.Fatalf("the backup is no longer the file from before tuios:\n%s", got)
	}
}
