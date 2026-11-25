package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rexServerWithEnv runs a Rex server of the test's own, on a socket in a
// temporary folder, with its own state and config. Its blocks get env, so
// the tuios they run reaches the test's daemon. It returns a function that
// runs the rex CLI against it.
func rexServerWithEnv(t *testing.T, env []string) func(args ...string) string {
	t.Helper()
	rex := needTool(t, "rex")
	dir, err := os.MkdirTemp("", "rx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock, cfg := filepath.Join(dir, "rex.sock"), filepath.Join(dir, "init.lua")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(rex, "server", "run", "--listen", "unix://"+sock, "--state-dir", filepath.Join(dir, "state"),
		"--restore=false", "--config", cfg)
	srv.Env = env
	log, _ := os.Create(filepath.Join(dir, "server.log"))
	srv.Stdout, srv.Stderr = log, log
	if err := srv.Start(); err != nil {
		t.Fatalf("start a scratch Rex server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Process.Signal(os.Interrupt)
		_ = srv.Wait()
		b, _ := os.ReadFile(filepath.Join(dir, "server.log"))
		saveText(t, "rex-server-log", string(b))
	})
	waitTextOf(t, "the scratch Rex server's socket", uiTimeout*3, func() string {
		if _, err := os.Stat(sock); err != nil {
			return ""
		}
		return "up"
	}, func(s string) bool { return s == "up" })
	return func(args ...string) string {
		t.Helper()
		cmd := exec.Command(rex, append([]string{"--autostart=false", "-S", "unix://" + sock}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rex %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
}

// TestRexShowsTuiosPanes: tuios_open_pane shows one tuios pane in a new
// split of a Rex session, and tuios_mirror_session opens a Rex window with
// one split for each pane of a tuios session. Both run tuios pane-view in
// real Rex blocks, against the test's own tuios daemon.
//
// How it could pass wrongly, written down first:
//   - A Rex block could print the marker itself: the markers are printed in
//     the tuios panes before Rex runs, and the Rex blocks run only tuios
//     pane-view.
//   - The mirror could show one pane twice: it must make one block for each
//     pane, and each block must show its own pane's marker.
//
// NEGATIVE CONTROL: with mirror_session passing the first window to every
// block, the mirror shows PANE-42 twice and the test fails.
func TestRexShowsTuiosPanes(t *testing.T) {
	requireReal(t, "it runs tuios pane-view in the blocks of a Rex server")
	base := t.TempDir()
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("new work: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "logs", "-s", "work", "--no-focus"); err != nil {
		t.Fatalf("new-window logs: %v\n%s", err, out)
	}
	var list struct {
		Windows []struct {
			ID   string `json:"window_id"`
			Name string `json:"display_name"`
		} `json:"windows"`
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", "work", "--json")
	if err != nil || json.Unmarshal([]byte(out), &list) != nil || len(list.Windows) != 2 {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	first, second := list.Windows[0].ID, list.Windows[1].ID
	for id, cmd := range map[string]string{first: "echo PANE-$((6*7))\n", second: "echo LOGS-$((3+4))\n"} {
		if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", id, cmd); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}
	for id, want := range map[string]string{first: "PANE-42", second: "LOGS-7"} {
		waitTextOf(t, want+" in its tuios pane", uiTimeout*3, func() string {
			out, _ := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", id)
			return out
		}, func(s string) bool { return strings.Contains(s, want) })
	}

	rex := rexServerWithEnv(t, cliEnv(base))
	var created struct {
		ID string `json:"session_id"`
	}
	if out := rex("new", "--json"); json.Unmarshal([]byte(out), &created) != nil || created.ID == "" {
		t.Fatalf("rex new gave no session: %s", out)
	}
	luaDir, err := filepath.Abs(filepath.Join("..", "..", "integrations", "rex"))
	if err != nil {
		t.Fatal(err)
	}
	loader := filepath.Join(t.TempDir(), "load.lua")
	script := "package.path = " + jsonString(luaDir+"/?.lua;") + " .. package.path\n" +
		"require(\"tuios\").setup{ bin = " + jsonString(tuiosBin) + " }\n"
	if err := os.WriteFile(loader, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	act := func(name string, args map[string]string) []string {
		t.Helper()
		raw, _ := json.Marshal(args)
		out := rex("do", "-s", created.ID, loader, "--action", name, "--args", string(raw), "--json")
		saveText(t, name, out)
		var res struct {
			Blocks []string `json:"block_ids"`
		}
		if json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res) != nil {
			t.Fatalf("%s gave no JSON: %s", name, out)
		}
		return res.Blocks
	}
	shows := func(block string) string {
		var got string
		waitTextOf(t, "a tuios pane in Rex block "+block, uiTimeout*3, func() string {
			got = rex("capture", "-b", block, "--trim")
			return got
		}, func(s string) bool { return strings.Contains(s, "PANE-42") || strings.Contains(s, "LOGS-7") })
		return got
	}
	marker := func(s string) string {
		if strings.Contains(s, "PANE-42") {
			return "PANE-42"
		}
		return "LOGS-7"
	}

	opened := act("tuios_open_pane", map[string]string{"session": "work", "window": second})
	if len(opened) != 1 {
		t.Fatalf("ASSERTION: tuios_open_pane made %d blocks, want 1", len(opened))
	}
	split := shows(opened[0])
	saveText(t, "rex-open-pane", split)
	if marker(split) != "LOGS-7" {
		t.Errorf("ASSERTION: the split does not show the pane it was given:\n%s", split)
	}

	mirrored := act("tuios_mirror_session", map[string]string{"session": "work"})
	if len(mirrored) != 2 {
		t.Fatalf("ASSERTION: the mirror made %d blocks for the 2 panes of work: %v", len(mirrored), mirrored)
	}
	var seen []string
	for i, b := range mirrored {
		s := shows(b)
		saveText(t, "rex-mirror-"+string(rune('a'+i)), s)
		seen = append(seen, marker(s))
	}
	slices.Sort(seen)
	if strings.Join(seen, " ") != "LOGS-7 PANE-42" {
		t.Errorf("ASSERTION: the mirror's blocks show %v, want each pane once", seen)
	}
}

// jsonString is s as a JSON string, which Lua reads the same way for these
// paths.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
