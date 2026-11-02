package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// What a person meets on the way to a first plugin, and what docs/PLUGINS.md
// promises a plugin author: each refusal says what to do, a manifest for a
// newer tuios never loads silently, the TUIOS_* variables hold what the page
// says, and an unlinked plugin's pane goes with it.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "The plugin flow".

// flowManifest is a tuios plugin with an action and a pane. Each writes its
// environment to a file in its state folder, and the action also calls tuios
// through TUIOS_BIN with its PATH cut.
const flowManifest = `api = 1
id = "e2e.flow"
name = "Flow probe"
version = "1.0.0"

[permissions]
grants = ["read"]

[[process]]
id = "dump"
run = "action"
title = "Dump"
command = ["sh", "dump.sh"]

[[process]]
id = "panel"
run = "pane"
title = "Panel"
placement = "tab"
command = ["sh", "pane.sh"]
`

const flowDump = `env | grep '^TUIOS_' > "$TUIOS_PLUGIN_STATE_DIR/action.env"
PATH= "$TUIOS_BIN" list-sessions > "$TUIOS_PLUGIN_STATE_DIR/action.sessions" 2>&1
`

const flowPane = `/usr/bin/env | /bin/grep '^TUIOS_' > "$TUIOS_PLUGIN_STATE_DIR/pane.env"
exec sleep 600
`

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// probeEnv reads the file of KEY=VALUE lines a probe wrote.
func probeEnv(data string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(data) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestPluginsFlowSaysWhatToDo(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	art := artifactDir(t)

	// With no daemon, approve names the command that starts one, and it does
	// not start one itself.
	linkedFirst := t.TempDir()
	writeFiles(t, linkedFirst, map[string]string{"tuios-plugin.toml": "api = 1\nid = \"e2e.first\"\nname = \"First\"\nversion = \"1.0.0\"\n"})
	if out, err := tuiosCLI(t, base, "plugins", "link", linkedFirst, "--enable"); err != nil {
		t.Fatalf("plugins link with no daemon: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "approve", "e2e.first", "--yes"); err == nil || !strings.Contains(out, "tuios start-server") {
		t.Errorf("approve with no daemon: %v, want the command that starts one\n%s", err, out)
	}

	if out, err := tuiosCLI(t, base, "new", "flow", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}

	// A waiting plugin keeps its name, and the reason has a column of its own.
	out, err := tuiosCLI(t, base, "plugins", "list")
	if err != nil {
		t.Fatalf("plugins list: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(art, "list.txt"), []byte(out), 0o644)
	if !strings.Contains(strings.SplitN(out, "\n", 2)[0], "REASON") {
		t.Errorf("plugins list has no REASON column:\n%s", out)
	}
	row := ""
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "e2e.first") {
			row = line
		}
	}
	if !strings.Contains(row, "waiting for approval") || !strings.Contains(row, " First ") {
		t.Errorf("the row of a waiting plugin lost its state or its name: %q", row)
	}
	if out, err := tuiosCLI(t, base, "plugins", "unlink", "e2e.first"); err != nil {
		t.Fatalf("plugins unlink: %v\n%s", err, out)
	}

	// A bare name, which no catalog can find yet.
	if out, err := tuiosCLI(t, base, "plugins", "install", "pomodoro", "--yes"); err == nil || !strings.Contains(out, "no plugin catalog") || !strings.Contains(out, "owner/repo") {
		t.Errorf("install of a bare name: %v, want what to give instead\n%s", err, out)
	}

	// A repository whose plugin is in a folder: the error names the folder
	// and the flag, never the fetch folder, and that flag then works.
	work := testutil.GitRepo(t)
	if err := os.MkdirAll(filepath.Join(work, "plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(work, "plugin"), map[string]string{"tuios-plugin.toml": flowManifest, "dump.sh": flowDump, "pane.sh": flowPane})
	testutil.Git(t, work, "add", "-A")
	testutil.Git(t, work, "commit", "-q", "-m", "a plugin in a folder")
	bare := filepath.Join(t.TempDir(), "flow.git")
	testutil.Git(t, work, "clone", "-q", "--bare", work, bare)
	out, err = tuiosCLI(t, base, "plugins", "install", "file://"+bare, "--yes")
	_ = os.WriteFile(filepath.Join(art, "no-manifest.txt"), []byte(out), 0o644)
	if err == nil || !strings.Contains(out, "--subdir plugin") || !strings.Contains(out, "Nothing was installed") {
		t.Errorf("install of a repository with its manifest in a folder: %v, want the folder and the flag\n%s", err, out)
	}
	if strings.Contains(out, "%fetch-") || strings.Contains(out, "plugin_manifest") {
		t.Errorf("the error shows a temp folder or a code:\n%s", out)
	}
	out, err = tuiosCLI(t, base, "plugins", "install", "file://"+bare, "--subdir", "plugin", "--yes")
	_ = os.WriteFile(filepath.Join(art, "install.txt"), []byte(out), 0o644)
	if err != nil {
		t.Fatalf("install with --subdir: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Grants are not a sandbox") {
		t.Errorf("the install plan does not say that grants are not a sandbox:\n%s", out)
	}

	// The variables docs/PLUGINS.md promises, in an action and in a pane.
	const id = "e2e.flow"
	state := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", id)
	if out, err := tuiosCLI(t, base, "plugins", "run", id, "dump", "--wait"); err != nil {
		t.Fatalf("plugins run: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(state, "action.env"))
	if err != nil {
		t.Fatalf("the action wrote no environment: %v", err)
	}
	env := probeEnv(string(raw))
	var win struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	wout, _ := tuiosCLI(t, base, "list-windows", "--json", "-s", "flow")
	if err := json.Unmarshal([]byte(wout), &win); err != nil || len(win.Windows) == 0 {
		t.Fatalf("list-windows: %v\n%s", err, wout)
	}
	root := env["TUIOS_PLUGIN_ROOT"]
	if root == "" || !strings.HasSuffix(root, "/plugin") {
		t.Errorf("TUIOS_PLUGIN_ROOT is %q, want the plugin's folder in the repository", root)
	}
	if _, err := os.Stat(filepath.Join(root, "dump.sh")); err != nil {
		t.Errorf("TUIOS_PLUGIN_ROOT has no dump.sh: %v", err)
	}
	for k, want := range map[string]string{
		"TUIOS_BIN":               tuiosBin,
		"TUIOS_PLUGIN_ID":         id,
		"TUIOS_PLUGIN_ENTRY":      "dump",
		"TUIOS_PLUGIN_STATE_DIR":  state,
		"TUIOS_PLUGIN_CONFIG_DIR": filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "plugin-config", id),
		"TUIOS_PLUGIN_SESSION":    "flow",
		"TUIOS_PLUGIN_PANE":       win.Windows[0].ID,
	} {
		if env[k] != want {
			t.Errorf("%s is %q, want %q", k, env[k], want)
		}
	}
	// A plugin's process is in no pane: the names that say so stay unset.
	for _, k := range []string{"TUIOS_PANE_ID", "TUIOS_WINDOW_ID", "TUIOS_SESSION"} {
		if v, ok := env[k]; ok {
			t.Errorf("%s is set to %q in a plugin's process, which runs in no pane", k, v)
		}
	}
	if env["TUIOS_PLUGIN_CONTEXT_JSON"] == "" {
		t.Error("TUIOS_PLUGIN_CONTEXT_JSON is empty")
	}
	if env["TUIOS_PLUGIN_CWD"] == "" {
		t.Error("TUIOS_PLUGIN_CWD is empty")
	}
	if b, _ := os.ReadFile(filepath.Join(state, "action.sessions")); !strings.Contains(string(b), "flow") {
		t.Errorf("TUIOS_BIN with no PATH did not reach the daemon: %s", b)
	}
	if out, err := tuiosCLI(t, base, "plugins", "open", id, "panel"); err != nil {
		t.Fatalf("plugins open: %v\n%s", err, out)
	}
	pane := probeEnv(waitFile(t, filepath.Join(state, "pane.env"), func(s string) bool { return strings.Contains(s, "TUIOS_PLUGIN_ENTRY") }, "the pane's environment"))
	if pane["TUIOS_PLUGIN_ENTRY"] != "panel" || pane["TUIOS_BIN"] != tuiosBin || pane["TUIOS_PLUGIN_SESSION"] != "flow" {
		t.Errorf("the pane's environment is wrong: %v", pane)
	}
	if _, ok := pane["TUIOS_PLUGIN_TOKEN"]; ok {
		t.Error("a plugin's pane got the plugin's token")
	}

	// uninstall takes --yes, as install does.
	if out, err := tuiosCLI(t, base, "plugins", "uninstall", id, "--yes"); err != nil {
		t.Fatalf("plugins uninstall --yes: %v\n%s", err, out)
	}

	// The compatibility promise: a manifest this tuios does not fully know
	// is refused with what to do. It is never loaded with a part ignored.
	head := "id = \"e2e.compat\"\nname = \"Compat\"\nversion = \"1.0.0\"\n"
	for _, c := range []struct {
		why, manifest string
		want          []string
	}{
		{"a newer api", "api = 2\n" + head, []string{"api 2", "tuios update"}},
		{"a newer key", "api = 1\n" + head + "new_thing = true\n", []string{"unknown key new_thing", "tuios update"}},
		{"a newer grant", "api = 1\n" + head + "[permissions]\ngrants = [\"telepathy\"]\n", []string{`unknown grant "telepathy"`, "tuios update"}},
		{"a newer run", "api = 1\n" + head + "[[process]]\nrun = \"lunar\"\ncommand = [\"true\"]\n", []string{`unknown run "lunar"`, "tuios update"}},
		{"a newer event", "api = 1\n" + head + "[[process]]\nrun = \"event\"\non = [\"pane.teleported\"]\ncommand = [\"true\"]\n", []string{`unknown event "pane.teleported"`, "tuios update"}},
		{"no api", head, []string{"api"}},
		{"both names", "api = 1\nschema = 1\n" + head, []string{"Remove schema"}},
	} {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{"tuios-plugin.toml": c.manifest})
		out, err := tuiosCLI(t, base, "plugins", "install", dir, "--yes")
		if err == nil {
			t.Errorf("%s loaded:\n%s", c.why, out)
		}
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s: the refusal lacks %q:\n%s", c.why, want, out)
			}
		}
		if strings.Contains(out, "plugin_manifest") {
			t.Errorf("%s: the refusal shows a code:\n%s", c.why, out)
		}
	}
	// The old name still loads, and info warns.
	oldName := t.TempDir()
	writeFiles(t, oldName, map[string]string{"tuios-plugin.toml": "schema = 1\n" + head})
	if out, err := tuiosCLI(t, base, "plugins", "link", oldName); err != nil {
		t.Fatalf("link of a schema = 1 manifest: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "info", "e2e.compat"); err != nil || !strings.Contains(out, "Use api = 1") {
		t.Errorf("info of a schema = 1 manifest: %v, want a warning\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "unlink", "e2e.compat"); err != nil {
		t.Fatalf("unlink: %v\n%s", err, out)
	}

	// unlink closes the plugin's panes, so they do not come back with the
	// saved session after a restart. A herdr plugin needs no approval.
	linked := t.TempDir()
	writeFiles(t, linked, map[string]string{
		"herdr-plugin.toml": "id = \"e2e.unlinked\"\nname = \"Unlinked\"\nversion = \"1.0.0\"\nmin_herdr_version = \"0.9.3\"\n\n[[panes]]\nid = \"hold\"\ntitle = \"HoldPane\"\nplacement = \"tab\"\ncommand = [\"sh\", \"-c\", \"exec sleep 600\"]\n",
	})
	if out, err := tuiosCLI(t, base, "plugins", "link", linked, "--enable"); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "open", "e2e.unlinked", "hold"); err != nil {
		t.Fatalf("open: %v\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "list-windows", "-s", "flow"); !strings.Contains(out, "HoldPane") {
		t.Fatalf("the plugin pane did not open:\n%s", out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "unlink", "e2e.unlinked"); err != nil {
		t.Fatalf("unlink: %v\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "list-windows", "-s", "flow"); strings.Contains(out, "HoldPane") {
		t.Errorf("the pane of an unlinked plugin stays open:\n%s", out)
	}
	killDaemonNow(t, base)
	if out, err := tuiosCLI(t, base, "start-server"); err != nil {
		t.Fatalf("start-server: %v\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "list-windows", "-s", "flow"); strings.Contains(out, "HoldPane") {
		t.Errorf("the pane of an unlinked plugin came back after a restart:\n%s", out)
	}
}

// fenced is the first fenced block of language lang in text.
func fenced(t *testing.T, text, lang string) string {
	t.Helper()
	_, rest, ok := strings.Cut(text, "```"+lang+"\n")
	if !ok {
		t.Fatalf("no %s block", lang)
	}
	body, _, ok := strings.Cut(rest, "```")
	if !ok {
		t.Fatalf("the %s block does not end", lang)
	}
	return body
}

// TestPluginsWalkthrough does what "Write a plugin in ten minutes" in
// docs/PLUGINS.md says, with the files as the page prints them, so the page
// cannot drift from what works. The frame of the popup is the artifact.
func TestPluginsWalkthrough(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "PLUGINS.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(page), "## Write a plugin in ten minutes")
	if !ok {
		t.Fatal("docs/PLUGINS.md has no walkthrough")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"tuios-plugin.toml": fenced(t, section, "toml")})
	script := fenced(t, section, "sh")
	if err := os.WriteFile(filepath.Join(dir, "show.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	cwd := workDirIn(t, base)
	testutil.Git(t, cwd, "init", "-q")
	testutil.Git(t, cwd, "-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "commit", "-q", "--allow-empty", "-m", "the commit the popup shows")
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", dir, "--yes"); err != nil {
		t.Fatalf("plugins install: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if out, err := tuiosCLI(t, base, "run-command", "-s", "work", "CommandPalette"); err != nil {
		t.Fatalf("run-command CommandPalette: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "work", "--raw", "Last commits"); err != nil {
		t.Fatalf("send-keys: %v\n%s", err, out)
	}
	if err := term.WaitForText("Commits: Last commits", uiTimeout); err != nil {
		t.Fatalf("the palette has no row for the plugin: %v\n%s", err, term.Snapshot())
	}
	art := artifactDir(t)
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "walkthrough-palette")
	if err := term.SendKeys("\r"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("the commit the popup shows", uiTimeout); err != nil {
		t.Fatalf("the popup does not show the commit of the focused pane's folder: %v\n%s", err, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "walkthrough-popup")
	if err := term.SendKeys("q"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(uiTimeout)
	for strings.Contains(term.Snapshot(), "the commit the popup shows") {
		if time.Now().After(deadline) {
			t.Fatalf("q did not close the popup:\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "plugins", "uninstall", "me.commits", "--yes"); err != nil {
		t.Fatalf("plugins uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "show.sh")); err != nil {
		t.Errorf("uninstall did not keep the folder: %v", err)
	}
}
