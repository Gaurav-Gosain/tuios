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
