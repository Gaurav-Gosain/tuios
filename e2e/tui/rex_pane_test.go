package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// rexScratch runs a Rex server of the test's own, on a socket in a temporary
// folder, with its own state, config and home, so nothing of the person's
// Rex is touched. It returns the server's unix:// address and a function
// that runs the rex CLI against it.
func rexScratch(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	rex, err := exec.LookPath("rex")
	if err != nil {
		t.Skip("rex is not on PATH")
	}
	dir, err := os.MkdirTemp("", "rx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "rex.sock")
	cfg := filepath.Join(dir, "init.lua")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HOME="+filepath.Join(dir, "home"), "XDG_RUNTIME_DIR="+filepath.Join(dir, "run"),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"), "XDG_STATE_HOME="+filepath.Join(dir, "state"), "XDG_DATA_HOME="+filepath.Join(dir, "data"))
	for _, d := range []string{"home", "run", "cfg", "state", "data"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
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
	addr := "unix://" + sock
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(rex, append([]string{"--autostart=false", "-S", addr}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("rex %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	return addr, run
}

// rexTuiosBlocks lists the blocks of the scratch server's tuios session.
func rexTuiosBlocks(t *testing.T, rex func(args ...string) string) []string {
	t.Helper()
	const lua = `local sid
for _, s in ipairs(rex.call("session.list").sessions or {}) do
  if s.label == "tuios" then sid = s.session_id end
end
if not sid then return {} end
rex.call("session.attach", { session_id = sid })
local ids = {}
for _, b in ipairs(rex.call("session.list_blocks", { session_id = sid }).blocks or {}) do
  ids[#ids + 1] = b.block_id
end
return ids`
	out := strings.TrimSpace(rex("do", "-e", lua, "--json"))
	var ids []string
	if strings.HasPrefix(out, "[") {
		_ = json.Unmarshal([]byte(out), &ids)
	}
	return ids
}

// TestRexPaneDetachAndReattach: tuios new-window --rex opens a pane whose
// program is a shell of a Rex server. rex_detach closes the pane and leaves
// the shell running on the server. --rex-block opens it again with its
// earlier output in the scrollback, and closing that pane ends the shell.
//
// How it could pass wrongly, written down first:
//   - The pane could run a local shell: the shell must be a block of the
//     server's tuios session while the pane is open.
//   - Detach could end the shell: the block must still be listed after the
//     pane closed.
//   - Reattach could open a new shell, or show only output typed after it:
//     the earlier output must be in the new pane, and the server must hold
//     one block, the same one.
//   - Closing the pane could leave the shell running: the block list must
//     be empty after close-window.
//
// NEGATIVE CONTROL: with the seed (rexSeed) cut from rex-attach, the
// reattached pane does not hold the earlier output and the test fails.
func TestRexPaneDetachAndReattach(t *testing.T) {
	requireReal(t, "it runs a Rex server and a bridge process in a pane")
	addr, rex := rexScratch(t)
	base := t.TempDir()
	term := startIn(t, base, startOpts{args: []string{"new", "work"}})
	waitBoot(t, term)

	if out, err := tuiosCLI(t, base, "new-window", "rx", "-s", "work", "--rex", addr); err != nil {
		t.Fatalf("new-window --rex: %v\n%s", err, out)
	}
	capture := func(w string) string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", w, "--scrollback")
		return out
	}
	var blocks []string
	waitTextOf(t, "a block in the scratch server's tuios session", uiTimeout*3, func() string {
		blocks = rexTuiosBlocks(t, rex)
		return strings.Join(blocks, ",")
	}, func(s string) bool { return s != "" })
	if len(blocks) != 1 {
		t.Fatalf("ASSERTION: the tuios Rex session holds %d blocks, want 1: %v", len(blocks), blocks)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", "rx", "echo REX-'4'2\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitTextOf(t, "the Rex shell's answer in the pane", uiTimeout*3, func() string { return capture("rx") },
		func(s string) bool { return strings.Contains(s, "REX-42") })
	saveFrame(t, term, "rex-pane")

	// rex_detach acts only on a pane whose program is tuios rex-attach, as
	// the daemon's poll of the pane's foreground reports it.
	waitTextOf(t, "the Rex pane's program in the window list", uiTimeout*3, func() string {
		out, _ := tuiosCLI(t, base, "list-windows", "-s", "work", "--json")
		return out
	}, func(s string) bool {
		return strings.Contains(s, `"foreground_cmd": "tuios"`) || strings.Contains(s, `"foreground_cmd":"tuios"`)
	})
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "work", "rx"); err != nil {
		t.Fatalf("focus rx: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "run-command", "-s", "work", "Action", "rex_detach"); err != nil {
		t.Fatalf("run rex_detach: %v\n%s", err, out)
	}
	waitTextOf(t, "the Rex pane closed after detach", uiTimeout*3, func() string {
		out, _ := tuiosCLI(t, base, "list-windows", "-s", "work")
		return out
	}, func(s string) bool { return !strings.Contains(s, "rx") })
	if after := rexTuiosBlocks(t, rex); len(after) != 1 || after[0] != blocks[0] {
		t.Fatalf("ASSERTION: detach did not leave the shell running: blocks %v, want %v", after, blocks)
	}

	if out, err := tuiosCLI(t, base, "new-window", "rx2", "-s", "work", "--rex", addr, "--rex-block", blocks[0]); err != nil {
		t.Fatalf("new-window --rex-block: %v\n%s", err, out)
	}
	seeded := waitTextOf(t, "the earlier output in the reattached pane", uiTimeout*3, func() string { return capture("rx2") },
		func(s string) bool { return strings.Contains(s, "REX-42") })
	saveText(t, "rex-reattached", seeded)
	if out, err := tuiosCLI(t, base, "send-text", "-s", "work", "-w", "rx2", "echo AGAIN-'4'\n"); err != nil {
		t.Fatalf("send-text rx2: %v\n%s", err, out)
	}
	waitTextOf(t, "a new answer in the reattached pane", uiTimeout*3, func() string { return capture("rx2") },
		func(s string) bool { return strings.Contains(s, "AGAIN-4") })
	if after := rexTuiosBlocks(t, rex); len(after) != 1 || after[0] != blocks[0] {
		t.Errorf("ASSERTION: reattach did not open the same shell: blocks %v, want %v", after, blocks)
	}
	saveFrame(t, term, "rex-reattached")

	if out, err := tuiosCLI(t, base, "close-window", "-s", "work", "rx2"); err != nil {
		t.Fatalf("close-window rx2: %v\n%s", err, out)
	}
	waitTextOf(t, "the Rex shell ended after its pane closed", uiTimeout*3, func() string {
		return strings.Join(rexTuiosBlocks(t, rex), ",")
	}, func(s string) bool { return s == "" })
	alive(t, term, "after the Rex pane round trip")
}
