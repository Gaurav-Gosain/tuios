package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mcpHome makes the harness's configuration directory, which install needs.
func mcpHome(t *testing.T, id string) (Env, *Target) {
	t.Helper()
	env := testEnv(t)
	tg := mustTarget(t, id)
	if err := os.MkdirAll(tg.ConfigDir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	return env, tg
}

func TestMCPInstallClaudeKeepsTheUsersServersAndRoundTrips(t *testing.T) {
	env, tg := mcpHome(t, ClaudeCode)
	path := filepath.Join(env.Home, ".claude.json")
	writeFile(t, path, `{"numStartups": 12, "mcpServers": {"mine": {"type": "stdio", "command": "mine-server"}}, "projects": {}}`)

	res, err := tg.InstallMCP(env, "tuios", false)
	if err != nil || !res.Changed || res.Path != path {
		t.Fatalf("InstallMCP = %+v, %v", res, err)
	}
	var doc struct {
		NumStartups int                        `json:"numStartups"`
		Servers     map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.NumStartups != 12 || doc.Servers["mine"] == nil {
		t.Errorf("the user's keys did not survive: %s", readFile(t, path))
	}
	var entry struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	_ = json.Unmarshal(doc.Servers["tuios"], &entry)
	if entry.Type != "stdio" || entry.Command != "tuios" || !slices.Equal(entry.Args, []string{"mcp", "--integration", "1"}) {
		t.Errorf("entry = %+v", entry)
	}
	if _, err := os.Stat(path + BackupSuffix); err != nil {
		t.Errorf("no backup of the file as it was: %v", err)
	}

	st := tg.MCPState(env, "tuios")
	if !st.Installed || !st.Current || st.Write || st.Version != MCPVersion {
		t.Errorf("status = %+v", st)
	}
	again, err := tg.InstallMCP(env, "tuios", false)
	if err != nil || again.Changed {
		t.Errorf("a second install changed the file: %+v, %v", again, err)
	}

	// --write is a different entry, and status says so.
	if _, err := tg.InstallMCP(env, "tuios", true); err != nil {
		t.Fatal(err)
	}
	if st := tg.MCPState(env, "tuios"); !st.Write || !st.Current {
		t.Errorf("status after --write = %+v", st)
	}
	if st := tg.MCPState(env, "/opt/tuios"); st.Current {
		t.Error("an entry running another binary reads as current")
	}

	if _, err := tg.UninstallMCP(env); err != nil {
		t.Fatal(err)
	}
	after := readFile(t, path)
	if strings.Contains(after, `"tuios"`) || !strings.Contains(after, "mine-server") {
		t.Errorf("uninstall left %s", after)
	}
}

func TestMCPInstallLeavesAServerTheUserNamedTuios(t *testing.T) {
	env, tg := mcpHome(t, GeminiCLI)
	path := filepath.Join(tg.ConfigDir(env), "settings.json")
	own := `{"mcpServers": {"tuios": {"command": "my-tuios-wrapper"}}}`
	writeFile(t, path, own)
	if _, err := tg.InstallMCP(env, "tuios", false); err == nil || !strings.Contains(err.Error(), "not written by tuios") {
		t.Errorf("install over the user's own server = %v", err)
	}
	if readFile(t, path) != own {
		t.Error("the refused install changed the file")
	}
	if st := tg.MCPState(env, "tuios"); st.Installed || !st.Foreign {
		t.Errorf("status = %+v, want foreign and not installed", st)
	}
	if res, err := tg.UninstallMCP(env); err != nil || res.Changed || readFile(t, path) != own {
		t.Errorf("uninstall touched the user's server: %+v, %v", res, err)
	}
}

func TestMCPInstallGeminiAndOpenCodeShapes(t *testing.T) {
	env, gem := mcpHome(t, GeminiCLI)
	if _, err := gem.InstallMCP(env, "tuios", true); err != nil {
		t.Fatal(err)
	}
	g := readFile(t, filepath.Join(gem.ConfigDir(env), "settings.json"))
	if !strings.Contains(g, `"mcpServers"`) || !strings.Contains(g, `"--write"`) || strings.Contains(g, `"type"`) {
		t.Errorf("gemini settings = %s", g)
	}

	env, oc := mcpHome(t, OpenCode)
	path := filepath.Join(oc.ConfigDir(env), "opencode.json")
	writeFile(t, path, `{"$schema": "https://opencode.ai/config.json", "model": "x"}`)
	if _, err := oc.InstallMCP(env, "tuios", false); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Model string `json:"model"`
		MCP   map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	e := doc.MCP["tuios"]
	if doc.Model != "x" || e.Type != "local" || !e.Enabled || !slices.Equal(e.Command, []string{"tuios", "mcp", "--integration", "1"}) {
		t.Errorf("opencode config = %s", readFile(t, path))
	}
	if st := oc.MCPState(env, "tuios"); !st.Current {
		t.Errorf("opencode status = %+v", st)
	}
	if _, err := oc.UninstallMCP(env); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, path), `"mcp"`) {
		t.Errorf("uninstall left an empty mcp object: %s", readFile(t, path))
	}
}

func TestMCPInstallCodexKeepsTheUsersTOML(t *testing.T) {
	env, tg := mcpHome(t, Codex)
	path := filepath.Join(tg.ConfigDir(env), "config.toml")
	user := "model = \"gpt-5\"\n\n[mcp_servers.docs]\ncommand = \"docs-mcp\"\n"
	writeFile(t, path, user)
	if _, err := tg.InstallMCP(env, "/opt/my tuios", true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, user) {
		t.Errorf("the user's config was not kept byte for byte:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.tuios]\ncommand = \"/opt/my tuios\"\nargs = [\"mcp\", \"--write\", \"--integration\", \"1\"]\n") {
		t.Errorf("block = \n%s", got)
	}
	if st := tg.MCPState(env, "/opt/my tuios"); !st.Current || !st.Write {
		t.Errorf("status = %+v", st)
	}
	if _, err := tg.UninstallMCP(env); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != user {
		t.Errorf("uninstall left:\n%q\nwant\n%q", got, user)
	}

	// A table the user wrote under the same name is left alone.
	own := user + "\n[mcp_servers.tuios]\ncommand = \"mine\"\n"
	writeFile(t, path, own)
	if _, err := tg.InstallMCP(env, "tuios", false); err == nil {
		t.Error("install over the user's own [mcp_servers.tuios] did not fail")
	}
	if st := tg.MCPState(env, "tuios"); st.Installed || !st.Foreign {
		t.Errorf("status = %+v, want foreign", st)
	}
}

func TestMCPInstallNeedsTheHarness(t *testing.T) {
	env := testEnv(t)
	if _, err := mustTarget(t, Codex).InstallMCP(env, "tuios", false); err == nil {
		t.Error("install for a harness that never ran here did not fail")
	}
}
