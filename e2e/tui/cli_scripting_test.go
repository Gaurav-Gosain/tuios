package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests hold the CLI to what a script and an agent read from it: the
// list commands print JSON with --json, set-config says when the daemon only
// recorded a value, and a command given a name that does not exist refuses it
// and names the command that lists the real ones.
//
// Negative controls (NEGATIVE_CONTROLS.md):
//   - the findLayoutTemplate check cut from `layout delete`: the delete of a
//     missing layout exits 0.
//   - the AvailableThemes check cut from --preview-theme: an unknown theme
//     prints the default palette and exits 0.
//   - the "Not applied" print cut from runSetConfig: stderr is empty while the
//     value was only recorded.
//   - the --json flag of each list command: before the change each run fails
//     with "unknown flag: --json".

// splitCLI runs a tuios subcommand under base and returns stdout and stderr
// apart, which tuiosCLI does not: JSON goes to stdout and notes to stderr.
func splitCLI(t *testing.T, base string, args ...string) (string, string, error) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func TestCLIListsPrintJSON(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	// One saved layout, so layout list --json has a row to print.
	dir, _, err := splitCLI(t, base, "layout", "dir")
	if err != nil {
		t.Fatalf("layout dir: %v", err)
	}
	dir = strings.TrimSpace(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := `{"name":"pair","version":2,"auto_tiling":true,"created_at":"2026-01-02T03:04:05Z","windows":[{},{}]}`
	if err := os.WriteFile(filepath.Join(dir, "pair.json"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}

	decode := func(what, out string, v any) {
		t.Helper()
		if err := json.Unmarshal([]byte(out), v); err != nil {
			t.Fatalf("ASSERTION: %s did not print JSON: %v\n%s", what, err, out)
		}
	}

	out, errOut, err := splitCLI(t, base, "layout", "list", "--json")
	if err != nil {
		t.Fatalf("layout list --json: %v\n%s%s", err, out, errOut)
	}
	var layouts []struct {
		Name    string `json:"name"`
		Windows int    `json:"windows"`
		Tiled   bool   `json:"tiled"`
	}
	decode("layout list --json", out, &layouts)
	if len(layouts) != 1 || layouts[0].Name != "pair" || layouts[0].Windows != 2 || !layouts[0].Tiled {
		t.Errorf("ASSERTION: layout list --json = %+v, want one tiled layout pair with 2 windows", layouts)
	}

	out, errOut, err = splitCLI(t, base, "tape", "list", "--json")
	if err != nil {
		t.Fatalf("tape list --json: %v\n%s%s", err, out, errOut)
	}
	var tapes struct {
		Dir   string            `json:"dir"`
		Tapes []json.RawMessage `json:"tapes"`
	}
	decode("tape list --json", out, &tapes)
	if tapes.Dir == "" || tapes.Tapes == nil {
		t.Errorf("ASSERTION: tape list --json = %s, want a dir and a tapes list", out)
	}

	if out, err := tuiosCLI(t, base, "new", "cli", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	out, errOut, err = splitCLI(t, base, "resurrect", "--json")
	if err != nil {
		t.Fatalf("resurrect --json: %v\n%s%s", err, out, errOut)
	}
	var saved []json.RawMessage
	decode("resurrect --json", out, &saved)

	out, errOut, err = splitCLI(t, base, "send-text", "-s", "cli", "--json", "echo SENT")
	if err != nil {
		t.Fatalf("send-text --json: %v\n%s%s", err, out, errOut)
	}
	var sent struct {
		Type string `json:"type"`
	}
	decode("send-text --json", out, &sent)
	if sent.Type != "ok" {
		t.Errorf("ASSERTION: send-text --json = %s, want type ok", out)
	}

	// No client is attached, so the daemon only records the value. The JSON
	// says so, and the plain run keeps its stdout line and says so on stderr.
	out, errOut, err = splitCLI(t, base, "set-config", "appearance.border_style", "rounded", "-s", "cli", "--json")
	if err != nil {
		t.Fatalf("set-config --json: %v\n%s%s", err, out, errOut)
	}
	var set struct {
		Applied *bool  `json:"applied"`
		Reason  string `json:"reason"`
		Key     string `json:"key"`
	}
	decode("set-config --json", out, &set)
	if set.Applied == nil || *set.Applied || set.Reason == "" || set.Key != "appearance.border_style" {
		t.Errorf("ASSERTION: set-config --json with no client = %s, want applied false with a reason", out)
	}

	out, errOut, err = splitCLI(t, base, "set-config", "appearance.border_style", "rounded", "-s", "cli")
	if err != nil {
		t.Fatalf("set-config: %v\n%s%s", err, out, errOut)
	}
	if strings.TrimSpace(out) != "Set appearance.border_style = rounded" {
		t.Errorf("ASSERTION: set-config stdout = %q, want the line it printed before", out)
	}
	if !strings.HasPrefix(errOut, "Not applied: ") || !strings.Contains(errOut, "client") {
		t.Errorf("ASSERTION: set-config with no client wrote %q on stderr, want a Not applied note that names the client", errOut)
	}
}

func TestCLIRefusesNamesThatDoNotExist(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		args []string
		next string
	}{
		{[]string{"layout", "delete", "nope"}, "tuios layout list"},
		{[]string{"layout", "export", "nope"}, "tuios layout list"},
		{[]string{"--preview-theme", "nope"}, "tuios list-themes"},
		{[]string{"tape", "show", "nope"}, "tuios tape list"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, "_"), func(t *testing.T) {
			out, err := tuiosCLI(t, base, c.args...)
			if err == nil {
				t.Fatalf("ASSERTION: tuios %s exited 0, want a refusal:\n%s", strings.Join(c.args, " "), out)
			}
			if !strings.Contains(out, `"nope"`) || !strings.Contains(out, c.next) {
				t.Errorf("ASSERTION: tuios %s printed %q, want the name and %q", strings.Join(c.args, " "), out, c.next)
			}
		})
	}
}
