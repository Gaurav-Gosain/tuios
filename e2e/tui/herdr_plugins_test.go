package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// herdr plugins hosted by tuios. Each test runs a real daemon with a client
// attached, and a stand-in plugin from testdata/herdrplugins that writes
// what it saw into its HERDR_PLUGIN_STATE_DIR. The test reads those files,
// and keeps them, with the frames, under TUIOS_E2E_FRAMES when it is set.

// pluginFixture is the absolute folder of a stand-in plugin.
func pluginFixture(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "herdrplugins", name))
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir
}

// pluginsConfig is a [plugins] table with the named fixtures in dirs and the
// given ids enabled.
func pluginsConfig(t *testing.T, enabled []string, fixtures ...string) string {
	t.Helper()
	quote := func(list []string) string {
		out := make([]string, len(list))
		for i, s := range list {
			out[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(out, ", ") + "]"
	}
	dirs := make([]string, len(fixtures))
	for i, f := range fixtures {
		dirs[i] = pluginFixture(t, f)
	}
	return "[plugins]\nenabled = " + quote(enabled) + "\ndirs = " + quote(dirs) + "\n"
}

// pluginClient is crushClient with a [plugins] table in config.toml.
func pluginClient(t *testing.T, plugins string) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n\n"+plugins)
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", crushSession, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", crushSession}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	t.Cleanup(func() {
		if t.Failed() {
			saveFrame(t, term, t.Name()+"-failed")
			out, _ := tuiosCLI(t, base, "plugins", "log")
			t.Logf("plugin log at the failure:\n%s", out)
		}
	})
	return term, base
}

// pluginState is the state folder the daemon gives plugin id.
func pluginState(base, id string) string {
	return filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "plugins", id)
}

// readPluginFile reads a file a stand-in wrote, "" when it is not there.
func readPluginFile(base, id, name string) string {
	b, _ := os.ReadFile(filepath.Join(pluginState(base, id), name))
	return string(b)
}

// waitPluginFile waits until a file a stand-in writes holds want.
func waitPluginFile(t *testing.T, base, id, name, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * uiTimeout)
	for {
		got := readPluginFile(base, id, name)
		if strings.Contains(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s of %s never held %q. It holds:\n%s", name, id, want, got)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// savePluginArtifact keeps a file a stand-in wrote beside the frames.
func savePluginArtifact(t *testing.T, base, id, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	dst := filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "-")+"-"+id+"-"+name)
	if err := os.WriteFile(dst, []byte(readPluginFile(base, id, name)), 0o644); err != nil {
		t.Logf("could not save %s: %v", dst, err)
	}
}

// envValue reads KEY=VALUE from a file a stand-in wrote.
func envValue(text, key string) string {
	for _, l := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	return ""
}

// TestHerdrPluginTrustAndHooks lists three plugins, one of them a manifest
// herdr refuses, with none enabled. While e2e.hooks is off its startup
// command and its pane.created hook never run, and a pane cannot turn it on,
// neither through tuios plugins nor through herdr's CLI. The person enables
// it: startup runs once, and a new pane runs the hook with herdr's event
// JSON. The person disables it: the startup process is killed and the next
// pane runs no hook.
//
// Negative controls (NEGATIVE_CONTROLS.md, "herdr plugins"): with the
// Runnable check cut from runStartups, the startup log appears while the
// plugin is off; with the herdrPluginTrust call cut from
// herdrPluginSetEnabled, the pane's enable succeeds; with d.plugins.start()
// cut from Daemon.Start, no hook runs after the enable; with writePlugins
// applying the table read back from config.toml, the person's enable also
// enables the plugin the test wrote into the file; with TUIOS_SOCKET cut
// from the plugin variables, the process the startup command leaves behind
// enables a plugin.
func TestHerdrPluginTrustAndHooks(t *testing.T) {
	_, base := pluginClient(t, pluginsConfig(t, nil, "hooks", "actions", "broken"))
	const id = "e2e.hooks"

	out, err := tuiosCLI(t, base, "plugins", "list", "--json")
	if err != nil {
		t.Fatalf("plugins list: %v\n%s", err, out)
	}
	var entries []struct {
		ID      string `json:"plugin_id"`
		Enabled bool   `json:"enabled"`
		Err     *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("plugins list --json: %v\n%s", err, out)
	}
	seen := map[string]string{}
	for _, e := range entries {
		state := "off"
		switch {
		case e.Err != nil:
			state = e.Err.Code
		case e.Enabled:
			state = "on"
		}
		seen[e.ID] = state
	}
	if seen[id] != "off" || seen["e2e.actions"] != "off" || seen["broken"] != "plugin_requires_newer_herdr" {
		t.Fatalf("plugins list: %v, want e2e.hooks and e2e.actions off and broken refused\n%s", seen, out)
	}

	// Off: nothing runs, at start or on a new pane.
	crushPanes(t, base, "w1")
	time.Sleep(2 * time.Second)
	if got := readPluginFile(base, id, "startup.log") + readPluginFile(base, id, "events.log"); got != "" {
		t.Fatalf("a disabled plugin ran:\n%s", got)
	}

	// A pane cannot enable it. The tuios command is run with the pane's
	// variables taken away, so the daemon's own check is what refuses it.
	steps := runHerdrSteps(t, base, "w1", "trust", fmt.Sprintf(
		"step cli env -u TUIOS_PANE_ID -u TUIOS_WINDOW_ID %s plugins enable %s\nstep herdr \"$H\" plugin enable %s\nstep link \"$H\" plugin link %s\n",
		tuiosBin, id, id, pluginFixture(t, "panes")))
	for _, n := range []string{"cli", "herdr", "link"} {
		s := steps[n]
		if s.code == 0 || !strings.Contains(s.out+s.err, "forbidden") {
			t.Fatalf("%s from a pane was not refused: exit %d\n%s%s", n, s.code, s.out, s.err)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml"))
	if strings.Contains(string(cfg), `enabled = ["`) || strings.Contains(string(cfg), "panes") {
		t.Fatalf("a refused change reached config.toml:\n%s", cfg)
	}
	time.Sleep(time.Second)
	if got := readPluginFile(base, id, "startup.log"); got != "" {
		t.Fatalf("the plugin ran after a refused enable:\n%s", got)
	}

	// A pane can still write config.toml itself. Its entry waits, and the
	// person's enable of another plugin does not apply it.
	cfgPath := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	if err := os.WriteFile(cfgPath, []byte(strings.Replace(string(cfg), "enabled = []", `enabled = ["e2e.actions"]`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)

	// The person enables it: startup runs once, and the hook runs on a new
	// pane, with herdr's event name, pane id and event JSON.
	if out, err := tuiosCLI(t, base, "plugins", "enable", id); err != nil {
		t.Fatalf("plugins enable: %v\n%s", err, out)
	}
	waitPluginFile(t, base, id, "startup.log", "startup startup e2e.hooks")
	if out, err := tuiosCLI(t, base, "plugins", "run", "e2e.actions", "hello", "--wait"); err == nil || !strings.Contains(out, "plugin_disabled") {
		t.Fatalf("the person's enable of %s also enabled e2e.actions, which a pane wrote into config.toml: %v\n%s", id, err, out)
	}
	// The startup command left a process behind, outside every pane, that
	// tries to enable e2e.actions. It is the plugin's, not the person's.
	orphan := waitPluginFile(t, base, id, "orphan.out", "code=")
	if !strings.Contains(orphan, "forbidden") || strings.Contains(orphan, "code=0") {
		t.Fatalf("a process the startup command left behind enabled a plugin:\n%s", orphan)
	}
	savePluginArtifact(t, base, id, "orphan.out")
	crushPanes(t, base, "w2")
	w2 := herdrPaneByLabel(t, base, "w2")["pane_id"].(string)
	events := waitPluginFile(t, base, id, "events.log", "pane.created "+w2+" ")
	line := ""
	for _, l := range strings.Split(events, "\n") {
		if strings.Contains(l, w2) {
			line = l
		}
	}
	var envelope struct {
		Event string `json:"event"`
		Data  struct {
			Type string `json:"type"`
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"data"`
	}
	js := ""
	if parts := strings.SplitN(line, " ", 3); len(parts) == 3 {
		js = parts[2]
	}
	if err := json.Unmarshal([]byte(js), &envelope); err != nil || envelope.Event != "pane_created" || envelope.Data.Type != "pane_created" || envelope.Data.Pane.PaneID != w2 {
		t.Fatalf("HERDR_PLUGIN_EVENT_JSON is not herdr's envelope for %s (%v):\n%s", w2, err, line)
	}
	if n := strings.Count(readPluginFile(base, id, "startup.log"), "startup"); n != 2 {
		t.Fatalf("startup ran %d times, want once:\n%s", n/2, readPluginFile(base, id, "startup.log"))
	}
	savePluginArtifact(t, base, id, "startup.log")
	savePluginArtifact(t, base, id, "events.log")

	// The person disables it: the startup process stops, and the next pane
	// runs no hook.
	pid, _ := strconv.Atoi(strings.TrimSpace(readPluginFile(base, id, "startup.pid")))
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		t.Fatalf("the startup process %d is not running while the plugin is on", pid)
	}
	if out, err := tuiosCLI(t, base, "plugins", "disable", id); err != nil {
		t.Fatalf("plugins disable: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the startup process %d still runs after disable", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	crushPanes(t, base, "w3")
	w3 := herdrPaneByLabel(t, base, "w3")["pane_id"].(string)
	time.Sleep(2 * time.Second)
	if strings.Contains(readPluginFile(base, id, "events.log"), w3) {
		t.Fatalf("a disabled plugin's hook ran for %s", w3)
	}
}

// TestHerdrPluginActions runs an action from tuios plugins run and from a
// pane through herdr's CLI. The action runs in the plugin's folder with
// herdr's variables, no terminal, the focused pane in its context, and a
// herdr command line that reaches tuios. plugin.log.list keeps both runs
// and their output.
//
// Negative controls (NEGATIVE_CONTROLS.md, "herdr plugins"): with the
// herdrPluginCall branch cut from herdrAPICall, both runs fail as
// unsupported; with cmd.Dir cut from Runner.Start, the cwd check fails;
// with the grant check cut from herdrPluginLogList, the read-only pane
// reads the log.
func TestHerdrPluginActions(t *testing.T) {
	_, base := pluginClient(t, pluginsConfig(t, []string{"e2e.actions"}, "actions"))
	const id = "e2e.actions"
	root := pluginFixture(t, "actions")
	crushPanes(t, base, "w1")

	out, err := tuiosCLI(t, base, "plugins", "run", id, "hello", "--wait")
	if err != nil || !strings.Contains(out, "hello from e2e.actions") {
		t.Fatalf("plugins run --wait: %v\n%s", err, out)
	}
	run := waitPluginFile(t, base, id, "run-0.env", "herdr=")
	for key, want := range map[string]string{
		"arg": "arg one", "cwd": root, "id": id, "root": root, "action": "hello",
		"stdin": "none", "herdr": "ok", "socket": herdrSocket(base),
	} {
		if got := envValue(run, key); got != want {
			t.Errorf("the action saw %s=%q, want %q", key, got, want)
		}
	}
	var ctx map[string]any
	if err := json.Unmarshal([]byte(envValue(run, "context")), &ctx); err != nil || ctx["invocation_source"] != "cli" || ctx["focused_pane_id"] == nil || ctx["focused_pane_id"] != envValue(run, "pane") {
		t.Errorf("HERDR_PLUGIN_CONTEXT_JSON %q (%v) lacks the cli source or the focused pane %q", envValue(run, "context"), err, envValue(run, "pane"))
	}
	if !strings.HasPrefix(envValue(run, "config"), filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")) {
		t.Errorf("HERDR_PLUGIN_CONFIG_DIR %q is not tuios's", envValue(run, "config"))
	}

	// From a pane, through herdr's CLI, as a herdr tool runs it.
	steps := runHerdrSteps(t, base, "w1", "actions", "step invoke \"$H\" plugin action invoke hello --plugin e2e.actions\nstep list \"$H\" plugin action list\n")
	steps["invoke"].ok(t, "plugin action invoke")
	inv := steps["invoke"].json(t, "plugin action invoke")
	if dig(inv, "result", "type") != "plugin_action_invoked" || dig(inv, "result", "log", "status") != "running" || dig(inv, "result", "action", "action_id") != "hello" {
		t.Fatalf("plugin action invoke answered %v", inv)
	}
	if acts, _ := dig(steps["list"].json(t, "plugin action list"), "result", "actions").([]any); len(acts) != 1 {
		t.Fatalf("plugin action list: %s", steps["list"].out)
	}
	run1 := waitPluginFile(t, base, id, "run-1.env", "herdr=")
	if envValue(run1, "cwd") != root || envValue(run1, "herdr") != "ok" {
		t.Fatalf("the action invoked from a pane saw:\n%s", run1)
	}
	savePluginArtifact(t, base, id, "run-0.env")
	savePluginArtifact(t, base, id, "run-1.env")

	deadline := time.Now().Add(uiTimeout)
	for {
		logs, _ := herdrCall(t, base, "plugin.log.list", map[string]any{"plugin_id": id})["logs"].([]any)
		done := 0
		for _, l := range logs {
			m := l.(map[string]any)
			if m["status"] == "succeeded" && strings.Contains(fmt.Sprint(m["stdout"]), "hello from e2e.actions") {
				done++
			}
		}
		if done == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin.log.list holds %d finished runs, want 2: %v", done, logs)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A read-only pane may neither run an action nor read the log, which
	// holds what the commands printed.
	crushPanes(t, base, "held")
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", crushSession, "-w", windowID(t, base, crushSession, "held"), "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	held := runHerdrSteps(t, base, "held", "held", "step invoke \"$H\" plugin action invoke hello --plugin e2e.actions\nstep log \"$H\" plugin log list --plugin e2e.actions\n")
	for _, name := range []string{"invoke", "log"} {
		st := held[name]
		if st.code != 1 || !strings.Contains(st.err, "forbidden") || strings.Contains(st.out, "hello from e2e.actions") {
			t.Errorf("plugin %s from a read-only pane: exit %d, stdout %q, stderr %q; want herdr's forbidden error", name, st.code, st.out, st.err)
		}
	}
}

// TestHerdrPluginPanes opens a plugin's popup from outside every pane, and
// its split from a pane through herdr's CLI. Each is an ordinary tuios pane
// that runs the entry's command in the plugin's folder with herdr's plugin
// variables, and the marker it prints is on screen. popup.close and
// plugin.pane.close close them.
//
// Negative controls (NEGATIVE_CONTROLS.md, "herdr plugins"): with Env cut
// from the NewWindowOptions in herdrPluginPaneOpen, the pane env files lack
// the entrypoint; with Popup forced false, the popup opens as a tile and
// the window count check fails.
func TestHerdrPluginPanes(t *testing.T) {
	term, base := pluginClient(t, pluginsConfig(t, []string{"e2e.panes"}, "panes"))
	const id = "e2e.panes"
	root := pluginFixture(t, "panes")
	if out, err := tuiosCLI(t, base, "run-command", "-s", crushSession, "EnableTiling"); err != nil {
		t.Fatalf("EnableTiling: %v\n%s", err, out)
	}

	// The popup, from outside every pane.
	if res := herdrCall(t, base, "plugin.pane.open", map[string]any{"plugin_id": id, "entrypoint": "pop"}); res["type"] != "ok" {
		t.Fatalf("plugin.pane.open pop answered %v", res)
	}
	if err := term.WaitForText("PLUGIN-PANE-READY pop", uiTimeout); err != nil {
		t.Fatalf("the popup's marker never showed: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "herdr-plugin-popup")
	pop := waitPluginFile(t, base, id, "pane-pop.env", "tuios_pane=")
	if envValue(pop, "entry") != "pop" || envValue(pop, "id") != id || envValue(pop, "cwd") != root || envValue(pop, "tuios_pane") == "" || envValue(pop, "pane") == "" {
		t.Fatalf("the popup pane saw:\n%s", pop)
	}
	// The manifest asks for 12 rows, which no tile of this screen has: the
	// pane is the popup, at the size the entry names.
	wins, _ := tuiosCLI(t, base, "list-windows", "--json", "-s", crushSession)
	var listed struct {
		Windows []struct {
			Name   string `json:"display_name"`
			Height int    `json:"height"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(wins), &listed)
	popupRows := 0
	for _, w := range listed.Windows {
		if w.Name == "E2E popup" {
			popupRows = w.Height
		}
	}
	if popupRows != 12 {
		t.Fatalf("the plugin popup is %d rows, want the 12 its entry names:\n%s", popupRows, wins)
	}
	if res := herdrCall(t, base, "popup.close", map[string]any{}); res["type"] != "ok" {
		t.Fatalf("popup.close answered %v", res)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !screenHas(s, "PLUGIN-PANE-READY pop") }, uiTimeout); err != nil {
		t.Fatalf("popup.close left the popup on screen\n%s", term.Snapshot())
	}

	// The split, from a pane, through herdr's CLI.
	crushPanes(t, base, "w1")
	steps := runHerdrSteps(t, base, "w1", "panes", "step open \"$H\" plugin pane open --plugin e2e.panes --entrypoint side\n")
	steps["open"].ok(t, "plugin pane open")
	opened := steps["open"].json(t, "plugin pane open")
	paneID, _ := dig(opened, "result", "plugin_pane", "pane", "pane_id").(string)
	if dig(opened, "result", "type") != "plugin_pane_opened" || dig(opened, "result", "plugin_pane", "entrypoint") != "side" || paneID == "" {
		t.Fatalf("plugin pane open answered %v", opened)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "PLUGIN-PANE-READY side") && countWindows(s) == 3
	}, uiTimeout); err != nil {
		t.Fatalf("the split never showed beside the shell: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "herdr-plugin-split")
	side := waitPluginFile(t, base, id, "pane-side.env", "tuios_pane=")
	if envValue(side, "entry") != "side" || envValue(side, "pane") != paneID || envValue(side, "cwd") != root {
		t.Fatalf("the split pane saw (opened as %s):\n%s", paneID, side)
	}
	savePluginArtifact(t, base, id, "pane-pop.env")
	savePluginArtifact(t, base, id, "pane-side.env")
	if res := herdrCall(t, base, "plugin.pane.close", map[string]any{"pane_id": paneID}); res["type"] != "plugin_pane_closed" {
		t.Fatalf("plugin.pane.close answered %v", res)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, uiTimeout); err != nil {
		t.Fatalf("plugin.pane.close left the pane\n%s", term.Snapshot())
	}
}

// TestHerdrPluginPalette runs an enabled plugin's action and opens its popup
// from the command palette, as the person does. A plugin that is not enabled
// has no row: its action is not offered, and nothing of it runs.
//
// Negative controls (NEGATIVE_CONTROLS.md, "herdr plugins"): with the
// pluginPaletteItems call cut from rebuildPaletteItems, the palette never
// lists "Say hello"; with the Runnable check cut from pluginPaletteItems,
// the palette offers the hidden plugin's action.
func TestHerdrPluginPalette(t *testing.T) {
	term, base := pluginClient(t, pluginsConfig(t, []string{"e2e.actions", "e2e.panes"}, "actions", "panes", "hidden"))
	const id = "e2e.actions"

	openPaletteQuery := func(query string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
			t.Fatalf("open palette: %v", err)
		}
		waitPaletteOpen(t, term, "for "+query)
		if err := term.SendKeys(query); err != nil {
			t.Fatalf("type palette query: %v", err)
		}
	}

	// The hidden plugin is off: its action is not in the palette.
	openPaletteQuery("Never offered")
	time.Sleep(500 * time.Millisecond)
	if screenHas(term.Screen(), "E2E hidden") {
		t.Fatalf("the palette offers an action of a plugin that is off\n%s", term.Snapshot())
	}
	closePalette(t, term, "after the hidden query")

	// The enabled action runs from the palette, with the palette as its
	// source.
	openPaletteQuery("Say hello")
	if err := term.WaitForText("E2E actions: Say hello", uiTimeout); err != nil {
		t.Fatalf("the palette never listed the plugin action: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "herdr-plugin-palette")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate the plugin action: %v", err)
	}
	waitPaletteClosed(t, term, "after the plugin action")
	run := waitPluginFile(t, base, id, "run-0.env", "herdr=")
	var ctx map[string]any
	if err := json.Unmarshal([]byte(envValue(run, "context")), &ctx); err != nil || ctx["invocation_source"] != "palette" {
		t.Fatalf("the palette's action saw context %q (%v), want invocation_source palette", envValue(run, "context"), err)
	}
	if envValue(run, "herdr") != "ok" {
		t.Fatalf("the palette's action could not reach herdr's CLI:\n%s", run)
	}
	savePluginArtifact(t, base, id, "run-0.env")

	// The enabled pane opens from the palette as a popup.
	openPaletteQuery("E2E popup")
	if err := term.WaitForText("E2E panes: E2E popup", uiTimeout); err != nil {
		t.Fatalf("the palette never listed the plugin pane: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate the plugin pane: %v", err)
	}
	if err := term.WaitForText("PLUGIN-PANE-READY pop", uiTimeout); err != nil {
		t.Fatalf("the palette's popup never showed its marker: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "herdr-plugin-palette-popup")
	if _, err := os.Stat(filepath.Join(pluginState(base, "e2e.hidden"), "ran")); err == nil {
		t.Fatalf("an action of a plugin that is off ran")
	}
}
