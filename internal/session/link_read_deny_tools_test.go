package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestALinkMayNotReadToolCredentialStores holds the deny lists to the stores
// where agent and developer tools keep their logins and keys, and to the
// config folders of the agent harnesses tuios installs hooks into.
//
// The ways this can fail, written down first:
//   - a tool's login, token or key file is readable over a link with files;
//   - a key or token a link may not read can still be replaced by a link
//     write, so the tool runs on a key the link chose;
//   - a harness config that names commands the tool runs (hooks, plugins,
//     extensions, MCP servers) can be written by a link to get code run;
//   - the entries name a whole tool folder where they should name its
//     credential file, so a tool's ordinary files or an unrelated file are
//     refused too.
func TestALinkMayNotReadToolCredentialStores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetDenyKeysCache(t)
	realHome := realPath(home)

	// What a link checks for each path below.
	const (
		// readRefused: linkReadDenied holds for the path and for a file under it.
		readRefused = 1 << iota
		// readServed: linkReadDenied does not hold for the path.
		readServed
		// writeRefused: linkDenied holds for the path.
		writeRefused
		// writeRefusedBelow: linkDenied holds for the path and for a file under it.
		writeRefusedBelow
		// writeAllowed: linkDenied does not hold for the path.
		writeAllowed
	)

	d, sp := startTestDaemon(t)
	d.SetLinkPolicies(map[string]config.HostConfig{"laptop": {Allow: []string{"list", "files"}}})
	link := dialLink(t, sp)
	result(t, linkPeer(t, link, "laptop"))
	id := 0
	read := func(t *testing.T, rel string) map[string]any {
		id++
		raw, _ := json.Marshal(map[string]any{"path": filepath.Join(home, filepath.FromSlash(rel))})
		return link.call(t, fmt.Sprintf(`{"id":%d,"verb":"file-read","params":%s}`, id, raw))
	}

	cred := readRefused | writeRefusedBelow
	for _, c := range []struct {
		rel   string
		check int
		// link is a file read over the link socket: refused when the path
		// is readRefused, served otherwise.
		link string
	}{
		// Keys and tokens a link may neither read nor replace.
		{".config/sops/age", cred, ""},
		{"Library/Application Support/sops/age", cred, ""},
		{"AppData/Roaming/sops/age", cred, ""},
		{".local/share/atuin", cred, ".local/share/atuin/key"},
		{".config/atuin", cred, ""},
		{".ollama/id_ed25519", cred, ""},
		{".gemini/.env", cred, ""},
		{".qwen/.env", cred, ""},
		{".hermes/.env", cred, ".hermes/.env"},
		{"AppData/Local/hermes/.env", cred, ""},
		{".claude.json", cred, ""},

		// Stores a link may not read.
		{".gemini/oauth_creds.json", readRefused, ".gemini/oauth_creds.json"},
		{".gemini/mcp-oauth-tokens.json", readRefused, ""},
		{".local/share/opencode/auth.json", readRefused, ""},
		{".local/share/opencode/mcp-auth.json", readRefused, ""},
		{".qwen/oauth_creds.json", readRefused, ""},
		{".local/share/kilo/auth.json", readRefused, ""},
		{".local/share/kilo/mcp-auth.json", readRefused, ""},
		{".pi/agent/auth.json", readRefused, ""},
		{".omp/agent/agent.db", readRefused, ""},
		{".omp/agent/agent.db-wal", readRefused, ""},
		{".local/share/amp/secrets.json", readRefused, ""},
		{".copilot/config.json", readRefused, ""},
		{".oci", readRefused, ".oci/config"},
		{".config/doctl", readRefused, ""},
		{"Library/Application Support/doctl", readRefused, ""},
		{"AppData/Roaming/doctl", readRefused, ""},
		{".config/ngrok", readRefused, ""},
		{"Library/Application Support/ngrok", readRefused, ""},
		{"AppData/Local/ngrok", readRefused, ""},
		{".ngrok2", readRefused, ""},
		{".config/Cursor", readRefused, ""},
		{".config/cursor/x", readRefused, ""},
		{"Library/Application Support/Cursor", readRefused, ""},
		{"AppData/Roaming/Cursor", readRefused, ""},
		{".local/share/zsh/history", readRefused, ""},
		{".local/state/zsh/history", readRefused, ""},

		// Configs that name commands a tool runs, which a link may not write.
		{".cursor/mcp.json", readRefused | writeRefused, ".cursor/mcp.json"},
		{".cursor/hooks.json", writeRefused, ""},
		{".gemini/settings.json", writeRefused, ""},
		{".config/opencode/opencode.json", writeRefused | readServed, ""},
		{".config/amp/plugins/a.ts", writeRefused, ""},
		{".copilot/hooks/a.json", writeRefused, ""},
		{".config/crush/crush.json", writeRefused, ""},
		{".config/devin/config.json", writeRefused, ""},
		{"AppData/Roaming/devin/config.json", writeRefused, ""},
		{".factory/settings.json", writeRefused, ""},
		{".grok/hooks/a.json", writeRefused, ""},
		{".hermes/config.yaml", writeRefused | readServed, ""},
		{"AppData/Local/hermes/config.yaml", writeRefused, ""},
		{".config/kilo/plugin/a.js", writeRefused, ""},
		{".kimi-code/config.toml", writeRefused, ""},
		{".pi/agent/extensions/a.ts", writeRefused, ""},
		{".omp/agent/extensions/a.ts", writeRefused, ""},
		{".qoder/settings.json", writeRefused, ""},
		{".qwen/settings.json", writeRefused, ""},

		// Ordinary files beside them stay readable.
		{".gemini/GEMINI.md", readServed, ".gemini/GEMINI.md"},
		{".cursor/settings.json", readServed, ""},
		{".local/share/opencode/storage/x", readServed, ""},
		{".copilot/session-state/x", readServed, ""},
		{".pi/agent/sessions/x", readServed, ""},
		{"notes.txt", readServed | writeAllowed, "notes.txt"},
	} {
		t.Run(c.rel, func(t *testing.T) {
			p := filepath.Join(realHome, filepath.FromSlash(c.rel))
			below := filepath.Join(p, "x")
			if c.check&readRefused != 0 && (!linkReadDenied(p, realHome, linkPrivate{}) || !linkReadDenied(below, realHome, linkPrivate{})) {
				t.Errorf("ASSERTION: a link may read ~/%s or a file under it", c.rel)
			}
			if c.check&readServed != 0 && linkReadDenied(p, realHome, linkPrivate{}) {
				t.Errorf("a link may not read the ordinary file ~/%s", c.rel)
			}
			if c.check&(writeRefused|writeRefusedBelow) != 0 && !linkDenied(p, realHome, linkPrivate{}) {
				t.Errorf("ASSERTION: a link may write ~/%s", c.rel)
			}
			if c.check&writeRefusedBelow != 0 && !linkDenied(below, realHome, linkPrivate{}) {
				t.Errorf("ASSERTION: a link may write a file under ~/%s", c.rel)
			}
			if c.check&writeAllowed != 0 && linkDenied(p, realHome, linkPrivate{}) {
				t.Errorf("a link may not write the ordinary file ~/%s", c.rel)
			}
			if c.link == "" {
				return
			}
			f := filepath.Join(home, filepath.FromSlash(c.link))
			if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, []byte("plain words\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if c.check&readRefused != 0 {
				mustRefuse(t, read(t, c.link), ErrVerbForbidden, "a link read ~/"+c.link)
			} else {
				result(t, read(t, c.link))
			}
		})
	}
}
