package integration

import (
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

// A tuios SKILL.md the person wrote or copied is never overwritten or
// removed. The stub tuios writes is replaced when it is out of date, and
// leaves the shared ~/.agents copy while another harness still uses it.
func TestSkillLeavesAFileItDidNotWrite(t *testing.T) {
	env := testEnv(t)
	claude := mustTarget(t, ClaudeCode)
	path := claude.SkillPath(env)
	writeFile(t, path, "---\nname: tuios\n---\nmy own copy\n")
	if _, err := claude.InstallSkill(env); err == nil || readFile(t, path) != "---\nname: tuios\n---\nmy own copy\n" {
		t.Fatalf("install overwrote a skill tuios did not write: %v", err)
	}
	if res, err := claude.UninstallSkill(env); err != nil || res.Changed {
		t.Fatalf("uninstall removed a skill tuios did not write: %+v %v", res, err)
	}
	if st := claude.SkillState(env); !st.Foreign || st.Installed {
		t.Fatalf("status of a skill tuios did not write = %+v, want foreign", st)
	}

	// The stub with a line the person added, a stub a newer tuios wrote, and
	// a link to a stub: each stays as it is.
	edited := string(SkillStub()) + "Always pass --json.\n"
	newer := strings.Replace(string(SkillStub()), "TUIOS_INTEGRATION_VERSION=1 ", "TUIOS_INTEGRATION_VERSION=9 ", 1)
	for name, text := range map[string]string{"edited": edited, "newer": newer} {
		writeFile(t, path, text)
		if _, err := claude.InstallSkill(env); readFile(t, path) != text {
			t.Fatalf("install replaced the %s stub: %v", name, err)
		}
		if _, err := claude.UninstallSkill(env); name == "edited" && readFile(t, path) != text {
			t.Fatalf("uninstall removed the edited stub: %v", err)
		}
	}

	codex, opencode := mustTarget(t, Codex), mustTarget(t, OpenCode)
	shared := codex.SkillPath(env)
	writeFile(t, shared, strings.Replace(string(SkillStub()), "TUIOS_INTEGRATION_VERSION=", "TUIOS_INTEGRATION_VERSION=0", 1))
	if st := codex.SkillState(env); !st.Installed || st.Current {
		t.Fatalf("status of an older stub = %+v, want installed and out of date", st)
	}
	if _, err := codex.InstallSkill(env); err != nil || readFile(t, shared) != string(SkillStub()) {
		t.Fatalf("install did not replace an older stub: %v", err)
	}
	if !strings.Contains(string(SkillStub()), "tuios --skill") {
		t.Fatal("the stub does not tell the agent to run tuios --skill")
	}
	writeFile(t, filepath.Join(opencode.ConfigDir(env), "x"), "")
	if _, err := opencode.Install(env, "tuios"); err != nil {
		t.Fatal(err)
	}
	if res, err := codex.UninstallSkill(env); err != nil || res.Changed {
		t.Fatalf("uninstall removed the stub opencode still uses: %+v %v", res, err)
	}
	if _, err := opencode.Uninstall(env); err != nil {
		t.Fatal(err)
	}
	if res, err := opencode.UninstallSkill(env); err != nil || !res.Changed {
		t.Fatalf("uninstall kept the stub nothing uses: %+v %v", res, err)
	}

	target := filepath.Join(t.TempDir(), "SKILL.md")
	linked := strings.Replace(string(SkillStub()), "TUIOS_INTEGRATION_VERSION=", "TUIOS_INTEGRATION_VERSION=0", 1)
	writeFile(t, target, linked)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	_, _ = claude.InstallSkill(env)
	_, _ = claude.UninstallSkill(env)
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 || readFile(t, target) != linked {
		t.Fatalf("install or uninstall replaced a linked SKILL.md: %v", err)
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
