package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// TestHerdrMethodsNameTheirCheck holds the herdr socket to the rule the JSON
// verbs have: every method it answers names its check, and the table names
// no method the socket does not answer. A method missing here is refused at
// run time, so this test is what tells its author.
func TestHerdrMethodsNameTheirCheck(t *testing.T) {
	answered := herdrAnswered()
	for _, m := range answered {
		if herdrChecks[m] == herdrUnclassified {
			t.Errorf("herdr method %s has no entry in herdrChecks", m)
		}
	}
	for m := range herdrChecks {
		if !slices.Contains(answered, m) {
			t.Errorf("herdrChecks names %s, which the herdr socket does not answer", m)
		}
	}
}

// herdrVerbsCalls are the params each herdrVerbs method is called with in
// TestHerdrVerbMethodsRefuseAPluginWithNoGrants. WS, TAB and PANE stand for
// the ids of a workspace the person made. A herdrVerbs method missing here
// fails the test.
var herdrVerbsCalls = map[string]string{
	"session.snapshot":          `{}`,
	"workspace.list":            `{}`,
	"workspace.get":             `{"workspace_id":"WS"}`,
	"workspace.create":          `{"cwd":"/","focus":false,"label":"x"}`,
	"workspace.rename":          `{"workspace_id":"WS","label":"x"}`,
	"workspace.close":           `{"workspace_id":"WS"}`,
	"workspace.focus":           `{"workspace_id":"WS"}`,
	"workspace.report_metadata": `{"workspace_id":"WS","source":"t","tokens":{"k":"v"}}`,
	"tab.list":                  `{}`,
	"tab.get":                   `{"tab_id":"TAB"}`,
	"tab.create":                `{"workspace_id":"WS","focus":false,"label":"x"}`,
	"tab.rename":                `{"tab_id":"TAB","label":"x"}`,
	"tab.focus":                 `{"tab_id":"TAB"}`,
	"tab.move":                  `{"tab_id":"TAB","insert_index":0}`,
	"tab.close":                 `{"tab_id":"TAB"}`,
	"pane.list":                 `{}`,
	"pane.get":                  `{"pane_id":"PANE"}`,
	"pane.current":              `{}`,
	"pane.layout":               `{"pane_id":"PANE"}`,
	"pane.read":                 `{"pane_id":"PANE","source":"visible"}`,
	"pane.send_text":            `{"pane_id":"PANE","text":"x"}`,
	"pane.send_keys":            `{"pane_id":"PANE","keys":["Enter"]}`,
	"pane.send_input":           `{"pane_id":"PANE","text":"x"}`,
	"pane.rename":               `{"pane_id":"PANE","label":"x"}`,
	"pane.focus":                `{"pane_id":"PANE"}`,
	"pane.split":                `{"target_pane_id":"PANE","direction":"right"}`,
	"pane.close":                `{"pane_id":"PANE"}`,
	"pane.wait_for_output":      `{"pane_id":"PANE","source":"visible","match":{"type":"substring","value":"x"},"timeout_ms":100}`,
	"pane.process_info":         `{"pane_id":"PANE"}`,
	"pane.neighbor":             `{"pane_id":"PANE","direction":"right"}`,
	"pane.edges":                `{"pane_id":"PANE"}`,
	"pane.focus_direction":      `{"pane_id":"PANE","direction":"right"}`,
	"pane.swap":                 `{"source_pane_id":"PANE","target_pane_id":"PANE"}`,
	"pane.zoom":                 `{"pane_id":"PANE"}`,
	"pane.resize":               `{"pane_id":"PANE","direction":"right"}`,
	"pane.move":                 `{"pane_id":"PANE","destination":{"type":"new_workspace"}}`,
	"agent.list":                `{}`,
	"agent.get":                 `{"target":"PANE"}`,
	"agent.read":                `{"target":"PANE","source":"visible"}`,
	"agent.send_keys":           `{"target":"PANE","keys":["Enter"]}`,
	"agent.focus":               `{"target":"PANE"}`,
	"agent.prompt":              `{"target":"PANE","text":"x"}`,
	"agent.wait":                `{"target":"PANE","timeout_ms":100}`,
	"agent.start":               `{"name":"x","kind":"claude","pane_id":"PANE"}`,
	"agent.rename":              `{"target":"PANE","name":"x"}`,
	"agent.explain":             `{"target":"PANE"}`,
	"worktree.list":             `{"workspace_id":"WS"}`,
	"worktree.create":           `{"workspace_id":"WS","branch":"x"}`,
	"worktree.open":             `{"workspace_id":"WS","path":"/"}`,
	"worktree.remove":           `{"workspace_id":"WS"}`,
	"events.wait":               `{"match_event":{"event":"pane_agent_status_changed","pane_id":"PANE","agent_status":"idle"},"timeout_ms":100}`,
	"events.subscribe":          `{}`,
	"client.window_title.set":   `{"title":"x"}`,
	"client.window_title.clear": `{}`,
	"plugin.pane.focus":         `{"pane_id":"PANE"}`,
	"plugin.pane.close":         `{"pane_id":"PANE"}`,
	"popup.close":               `{"pane_id":"PANE"}`,
}

// herdrListMethods answer a caller that may read nothing with an empty list
// rather than an error, as the matching tuios list verbs do, and the window
// title methods find no client the caller may reach and change nothing.
// They pass when the answer names nothing the person made.
var herdrListMethods = []string{"session.snapshot", "workspace.list", "tab.list", "pane.list", "agent.list", "client.window_title.set", "client.window_title.clear"}

// herdrHiddenMethods look their target up in the caller's filtered view
// before they act, so a caller that may read nothing is told the target is
// not there. That answer is the refusal: the target exists for the person.
var herdrHiddenMethods = map[string]string{
	"pane.current": "pane_not_found",
	"agent.rename": "agent_not_found",
	"popup.close":  "popup_not_found",
	"pane.swap":    "pane_not_found",
}

// TestHerdrVerbMethodsRefuseAPluginWithNoGrants holds the herdrVerbs label to
// what it says, by running it. Every method so labelled is called from a
// real process of a native plugin that holds no grants, the runner's own,
// against a workspace the person made. Each must be refused with forbidden,
// or for a list, answer with nothing the person made, and the person's
// workspace must be as it was. A handler that skips the grant check on any
// path the call takes answers instead.
func TestHerdrVerbMethodsRefuseAPluginWithNoGrants(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native plugins run only where the kernel names a caller")
	}
	// The person's shell starts on its own time, and its prompt raises the
	// pane's revision. A prompt that lands between the two snapshots below
	// reads as a change the calls made, so the shell prints a prompt the test
	// can wait for, and the first snapshot is taken once it shows. After it the
	// shell writes nothing until it reads input, so any later change to the
	// pane is a call that got through.
	const prompt = "tuios-test-ready>"
	t.Setenv("PS1", prompt+" ")
	d, sp := startTestDaemon(t)
	ws := herdrOK(t, "workspace.create", herdrDial(t, sp, "workspace.create", map[string]any{"cwd": t.TempDir(), "focus": false, "label": "Demo"}))
	ids := map[string]string{
		"WS":   ws["workspace"].(map[string]any)["workspace_id"].(string),
		"TAB":  ws["tab"].(map[string]any)["tab_id"].(string),
		"PANE": ws["root_pane"].(map[string]any)["pane_id"].(string),
	}

	herdrOK(t, "pane.wait_for_output", herdrDial(t, sp, "pane.wait_for_output", map[string]any{
		"pane_id": ids["PANE"], "source": "visible",
		"match": map[string]any{"type": "substring", "value": prompt}, "timeout_ms": 15_000,
	}))

	pidFile := filepath.Join(t.TempDir(), "pid")
	p := &herdrplugin.Plugin{PluginID: "t.none", Format: herdrplugin.FormatTuios, PluginRoot: t.TempDir(), Held: []string{}}
	if _, perr := d.plugins.runner.Start(herdrplugin.Job{Plugin: p, Command: []string{"sh", "-c", "echo $$ >" + pidFile + "; exec sleep 60"}}); perr != nil {
		t.Fatal(perr)
	}
	t.Cleanup(func() { d.plugins.runner.StopPlugin(p.PluginID) })
	pid := 0
	for deadline := time.Now().Add(5 * time.Second); pid == 0; {
		b, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		if pid == 0 && time.Now().After(deadline) {
			t.Fatal("the plugin process never wrote its pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The person's pane counts as a pane of this plugin, so plugin.pane.*
	// reach their grant check rather than stop at "not a plugin pane".
	if _, win, ierr := d.herdrFindPane(ids["PANE"]); ierr == nil {
		d.plugins.mu.Lock()
		d.plugins.panes[win.ID] = pluginPane{pluginID: p.PluginID, entrypoint: "x"}
		d.plugins.mu.Unlock()
	}
	snapshot := func() string {
		out, _ := json.Marshal(herdrOK(t, "snapshot", herdrDial(t, sp, "session.snapshot", nil)))
		return string(out)
	}
	before := snapshot()

	var verbs []string
	for m, c := range herdrChecks {
		if c == herdrVerbs {
			verbs = append(verbs, m)
		}
	}
	slices.Sort(verbs)
	for _, m := range verbs {
		tmpl, ok := herdrVerbsCalls[m]
		if !ok {
			t.Errorf("herdr method %s is labelled herdrVerbs and has no entry in herdrVerbsCalls", m)
			continue
		}
		for k, v := range ids {
			tmpl = strings.ReplaceAll(tmpl, k, v)
		}
		cs := &connState{clientID: "plugin-test", peerPID: pid, done: make(chan struct{})}
		var out any
		var herr *herdrError
		switch {
		case m == "events.subscribe":
			var sub *eventSub
			if sub, herr = d.herdrSubscribe(cs); sub != nil {
				d.events.unsubscribe(sub)
			}
		case strings.HasPrefix(m, "plugin.") || m == "popup.close":
			out, herr, _ = d.herdrPluginCall(cs, m, json.RawMessage(tmpl))
		default:
			out, herr, _ = d.herdrAPICall(cs, m, json.RawMessage(tmpl))
		}
		if herr != nil {
			if herr.code != "forbidden" && herr.code != herdrHiddenMethods[m] {
				t.Errorf("%s from a plugin with no grants: %s %q, want forbidden", m, herr.code, herr.msg)
			}
			continue
		}
		raw, _ := json.Marshal(out)
		leaked := slices.ContainsFunc([]string{ids["WS"], ids["TAB"], ids["PANE"]}, func(id string) bool { return strings.Contains(string(raw), id) })
		if !slices.Contains(herdrListMethods, m) || leaked {
			t.Errorf("%s from a plugin with no grants answered %s, want forbidden", m, raw)
		}
	}
	if after := snapshot(); after != before {
		t.Errorf("the calls changed the person's workspace:\nbefore %s\nafter  %s", before, after)
	}
}
