package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The ui grant: a shell plugin sets a dock cell from an action and exits, the
// daemon keeps the text, a click runs an action, a notice shows under the
// plugin's name, and a TML panel opens that the plugin reads back. The grant
// reaches nothing else, a form a hook opened outside every pane included. A
// plugin without it is refused, and a person's shell is refused.
//
// The negative controls are in NEGATIVE_CONTROLS.md, "The ui grant".

const uiManifest = `api = 1
id = "e2e.ui"
name = "UI probe"
version = "1.0.0"

[permissions]
grants = ["ui"]

[[cell]]
id = "status"

[[process]]
id = "set"
run = "action"
title = "Set"
command = ["sh", "set.sh"]

[[process]]
id = "clicked"
run = "action"
title = "Clicked"
command = ["sh", "-c", "echo yes > \"$TUIOS_PLUGIN_STATE_DIR/clicked\""]

[[process]]
id = "panel"
run = "action"
title = "Panel"
command = ["sh", "panel.sh"]

[[process]]
id = "probe"
run = "action"
title = "Probe"
command = ["sh", "probe.sh"]
`

const uiSet = `"$TUIOS_BIN" plugin-cell set status "$(printf 'CI \033[32mok\033[2J')" --action clicked
"$TUIOS_BIN" plugin-notify "The build passed"
`

const uiPanel = `cd "$TUIOS_PLUGIN_STATE_DIR"
cat > panel.tml <<'T'
<tml version="1" title="Probe panel"><text>panel from a plugin</text></tml>
T
"$TUIOS_BIN" tml show panel.tml -s "$TUIOS_PLUGIN_SESSION" > panel.out 2> panel.err
echo $? > panel.code
"$TUIOS_BIN" tml get "$(awk '/^tml-/ {print $1; exit}' panel.out)" > own.out 2> own.err
echo $? > own.code
`

// uiProbe tries what the ui grant must not reach.
const uiProbe = `D="$TUIOS_PLUGIN_STATE_DIR"
run() { n=$1; shift; "$@" >"$D/$n.out" 2>&1; echo $? >"$D/$n.code"; }
run type "$TUIOS_BIN" send-text -s "$TUIOS_PLUGIN_SESSION" "echo typed"
run read "$TUIOS_BIN" capture-pane -s "$TUIOS_PLUGIN_SESSION"
run list "$TUIOS_BIN" list-sessions
run other "$TUIOS_BIN" plugin-cell set nosuch text
run hook "$TUIOS_BIN" tml get "$(cat "$D/hook.id")"
echo done > "$D/probe.done"
`

func TestPluginUIGrant(t *testing.T) {
	const id = "e2e.ui"
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"tuios-plugin.toml": uiManifest, "set.sh": uiSet, "panel.sh": uiPanel, "probe.sh": uiProbe})
	base := t.TempDir()
	// A hook runs outside every pane, so tuios cannot place it. Armed once,
	// it opens a form as a pane and leaves the surface id in hookID.
	armed := filepath.Join(base, "armed")
	hookID := filepath.Join(base, "hook.id")
	hookOut := filepath.Join(base, "hook.out")
	hookErr := filepath.Join(base, "hook.err")
	hookTML := filepath.Join(base, "hook.tml")
	hook := filepath.Join(base, "hook.sh")
	if err := os.WriteFile(hookTML, []byte(`<tml version="1" title="Hook form"><text>form from a hook</text></tml>`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n[ -e " + armed + " ] || exit 0\nrm -f " + armed + "\n" +
		tuiosBin + " tml show " + hookTML + " --pane -s \"$TUIOS_SESSION_ID\" > " + hookOut + " 2> " + hookErr + "\n" +
		"awk '/^tml-/ {print $1; exit}' " + hookOut + " > " + hookID + ".tmp\nmv " + hookID + ".tmp " + hookID + "\n"
	if err := os.WriteFile(hook, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[hooks]\nafter-new-window = \""+hook+"\"\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "plugins", "install", dir, "--yes")
	if err != nil || !strings.Contains(out, "ui     Shows cells, panels and messages") || !strings.Contains(out, "cells    status") {
		t.Fatalf("install: %v\n%s", err, out)
	}
	art := artifactDir(t)
	state := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", id)
	run := func(action string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "plugins", "run", id, action, "--wait"); err != nil {
			t.Fatalf("plugins run %s: %v\n%s", action, err, out)
		}
	}

	// A person's shell has no plugin.
	if out, err := tuiosCLI(t, base, "plugin-cell", "set", "status", "x"); err == nil || !strings.Contains(out, "for a plugin's process") {
		t.Errorf("plugin-cell from a shell: %v\n%s", err, out)
	}

	// The hook's form, with a value the person typed into it.
	if err := os.WriteFile(armed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "work", "trigger", "--no-focus"); err != nil {
		t.Fatalf("open the trigger window: %v\n%s", err, out)
	}
	hookOpened := false
	t.Cleanup(func() {
		if !hookOpened {
			e, _ := os.ReadFile(hookErr)
			t.Logf("the hook's stderr:\n%s", e)
		}
	})
	surface := strings.TrimSpace(waitFile(t, hookID, func(s string) bool { return strings.HasPrefix(s, "tml-") }, "the hook's form"))
	hookOpened = true
	if out, err := tuiosCLI(t, base, "tml", "patch", surface, `[{"op":"add","path":"/user","value":"typed by the person"}]`); err != nil {
		t.Fatalf("type into the hook's form: %v\n%s", err, out)
	}
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "hook.id"), []byte(surface+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// What the grant does not reach.
	run("probe")
	for name, want := range map[string]string{"type": "needs the", "read": "needs the", "list": "needs the", "other": "no [[cell]] with the id", "hook": "refused for plugin " + id} {
		code, _ := os.ReadFile(filepath.Join(state, name+".code"))
		msg, _ := os.ReadFile(filepath.Join(state, name+".out"))
		if strings.TrimSpace(string(code)) == "0" || !strings.Contains(string(msg), want) {
			t.Errorf("%s with the ui grant: exit %s, want a refusal with %q\n%s", name, code, want, msg)
		}
	}
	if out, err := tuiosCLI(t, base, "tml", "close", surface); err != nil {
		t.Fatalf("close the hook's form: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "close-window", "-s", "work", "trigger"); err != nil {
		t.Fatalf("close the trigger window: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	run("set")
	// The notice sits where the right-hand cells do while it shows.
	if err := term.WaitForText("UI probe: The build passed", uiTimeout); err != nil {
		t.Fatalf("the notice did not show under the plugin's name: %v\n%s", err, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "notice")
	if err := term.WaitForText("CI ok", uiTimeout); err != nil {
		t.Fatalf("the cell did not draw after the action exited: %v\n%s", err, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "cell")

	// A click on the cell runs the action it names.
	x, y, ok := dockCellFind(term.Screen(), "CI ok")
	if !ok {
		t.Fatalf("the cell is not on the dock:\n%s", term.Snapshot())
	}
	mouseClick(t, term, x+1, y, tuitest.MouseLeft, 0)
	waitFile(t, filepath.Join(state, "clicked"), func(s string) bool { return strings.Contains(s, "yes") }, "the action of the cell")

	// A panel, with the ui grant alone.
	go func() { _, _ = tuiosCLI(t, base, "plugins", "run", id, "panel", "--wait") }()
	if err := term.WaitForText("panel from a plugin", uiTimeout); err != nil {
		b, _ := os.ReadFile(filepath.Join(state, "panel.out"))
		e, _ := os.ReadFile(filepath.Join(state, "panel.err"))
		t.Fatalf("the panel did not open: %v\n%s%s\n%s", err, b, e, term.Snapshot())
	}
	// The plugin reads its own panel.
	code := strings.TrimSpace(waitFile(t, filepath.Join(state, "own.code"), func(s string) bool { return strings.TrimSpace(s) != "" }, "the plugin's tml get of its panel"))
	if own, _ := os.ReadFile(filepath.Join(state, "own.out")); code != "0" || !strings.Contains(string(own), "panel from a plugin") {
		e, _ := os.ReadFile(filepath.Join(state, "own.err"))
		t.Errorf("the plugin's tml get of its own panel: exit %s, want 0 and the panel\n%s%s", code, own, e)
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "panel")
	if err := term.SendKeys("\x1b"); err != nil {
		t.Fatal(err)
	}

	// A client that attaches later gets the cell, at 16 colours too.
	late := startIn(t, base, startOpts{args: []string{"attach", "work"}, env: []string{"TERM=xterm", "COLORTERM="}})
	if err := late.WaitForText("CI ok", bootTimeout); err != nil {
		t.Fatalf("a late client has no cell: %v\n%s", err, late.Snapshot())
	}
	savePNG(t, late.Screen(), hostPalette(t, ""), art, "cell-16-colours")

	// The cell goes when the plugin stops.
	if out, err := tuiosCLI(t, base, "plugins", "disable", id); err != nil {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for strings.Contains(late.Snapshot(), "CI ok") {
		if time.Now().After(deadline) {
			t.Fatalf("the cell of a stopped plugin stays:\n%s", late.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Without the grant.
	plain := t.TempDir()
	writeFiles(t, plain, map[string]string{
		"tuios-plugin.toml": strings.NewReplacer(`"e2e.ui"`, `"e2e.noui"`, `grants = ["ui"]`, `grants = ["read"]`).Replace(uiManifest),
		"set.sh":            "\"$TUIOS_BIN\" plugin-cell set status x > \"$TUIOS_PLUGIN_STATE_DIR/cell.out\" 2>&1\n\"$TUIOS_BIN\" plugin-notify x > \"$TUIOS_PLUGIN_STATE_DIR/notify.out\" 2>&1\nexit 0\n",
		"panel.sh":          "", "probe.sh": "",
	})
	if out, err := tuiosCLI(t, base, "plugins", "install", plain, "--yes"); err != nil {
		t.Fatalf("install of the plain plugin: %v\n%s", err, out)
	}
	if out, _ := tuiosCLI(t, base, "plugins", "info", "e2e.noui"); !strings.Contains(out, "[[cell]] needs the ui grant") {
		t.Errorf("info does not warn about the grant:\n%s", out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "run", "e2e.noui", "set", "--wait"); err != nil {
		t.Fatalf("plugins run: %v\n%s", err, out)
	}
	noui := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", "e2e.noui")
	for _, f := range []string{"cell.out", "notify.out"} {
		if b, _ := os.ReadFile(filepath.Join(noui, f)); !strings.Contains(string(b), "needs the ui grant") {
			t.Errorf("%s without the grant: %s", f, b)
		}
	}
}
