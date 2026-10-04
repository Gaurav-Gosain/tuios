package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// herdr's methods that tuios answered with unsupported until plugins needed
// them: pane.resize (herdr-splits.nvim's resize keys), server.reload_config
// (terminal-browser and the config-writing plugins), pane.move,
// agent.rename and agent.explain. Each runs through "$HERDR_BIN_PATH" in a
// real pane, as the plugins run it.

// herdrWidths is the width of each pane of the tab pane is in.
func herdrWidths(t *testing.T, base, pane string) map[string]float64 {
	t.Helper()
	l := herdrCall(t, base, "pane.layout", map[string]any{"pane_id": pane})["layout"].(map[string]any)
	ws := map[string]float64{}
	for _, p := range l["panes"].([]any) {
		m := p.(map[string]any)
		ws[m["pane_id"].(string)] = m["rect"].(map[string]any)["width"].(float64)
	}
	return ws
}

// TestHerdrFrontResize runs herdr-splits.nvim's resize keys: from the right
// pane of two, move the border between them left (the pane grows) and then
// right (it shrinks back), as `pane resize --direction` does. The layout the
// daemon reports changes, and so does the screen. A step off the edge of a
// pane with no border on that side moves the other border. A read-only pane
// may not resize.
//
// Negative control: with the resize_window case taken out of the client's
// routed commands (internal/app/update.go), pane resize fails with
// pane_resize_failed and the widths do not change.
func TestHerdrFrontResize(t *testing.T) {
	term, base := herdrFrontClient(t)
	ids := crushPanes(t, base, "nav")
	navID := herdrPaneByLabel(t, base, "nav")["pane_id"].(string)
	before := herdrWidths(t, base, navID)
	if len(before) != 2 {
		t.Fatalf("the tab holds %d panes, want 2: %v", len(before), before)
	}
	otherID := ""
	for id := range before {
		if id != navID {
			otherID = id
		}
	}

	steps := runHerdrSteps(t, base, "nav", "resize", `
step grow "$H" pane resize --direction left --amount 0.1 --pane "$HERDR_PANE_ID"
`)
	steps["grow"].ok(t, "pane resize --direction left")
	grow := steps["grow"].json(t, "pane resize")
	if dig(grow, "result", "type") != "pane_resize" || dig(grow, "result", "resize", "changed") != true || dig(grow, "result", "resize", "pane_id") != navID {
		t.Fatalf("pane resize --direction left did not change the layout:\n%s", steps["grow"].out)
	}
	grown := herdrWidths(t, base, navID)
	if grown[navID] <= before[navID] || grown[otherID] >= before[otherID] {
		t.Fatalf("after moving the border left the widths are %v, were %v; want nav wider", grown, before)
	}

	// nav is at the right edge, so "right" moves its left border right:
	// nav shrinks again.
	more := runHerdrSteps(t, base, "nav", "resize2", `
step shrink "$H" pane resize --direction right --amount 0.1 --pane "$HERDR_PANE_ID"
`)
	more["shrink"].ok(t, "pane resize --direction right")
	shrunk := herdrWidths(t, base, navID)
	if shrunk[navID] >= grown[navID] {
		t.Fatalf("after moving the border right the widths are %v, were %v; want nav narrower", shrunk, grown)
	}
	saveFrame(t, term, "herdr-front-resize")

	// A pane with the read grant alone may not move a border.
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", crushSession, "-w", ids["nav"], "--grants", "read"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	held := runHerdrSteps(t, base, "nav", "resize3", `
step held "$H" pane resize --direction left --amount 0.2 --pane "$HERDR_PANE_ID"
`)
	var resp map[string]any
	_ = json.Unmarshal([]byte(held["held"].err), &resp)
	if held["held"].code != 1 || dig(resp, "error", "code") != "forbidden" {
		t.Fatalf("pane resize from a read-only pane: exit %d, stderr %q; want herdr's forbidden error", held["held"].code, held["held"].err)
	}
	if after := herdrWidths(t, base, navID); after[navID] != shrunk[navID] {
		t.Fatalf("a refused resize changed the widths: %v, were %v", after, shrunk)
	}
	alive(t, term, "after the resize sequence")
}

// TestHerdrFrontReloadMoveRename runs the calls of the plugins that write
// herdr's config and reload it, move a pane to its own tab, and name an
// agent: server reload-config succeeds and says nothing was reloaded, pane
// move --new-tab puts the pane on a new tab of its workspace, agent rename
// names the agent and agent get finds it by the new name, and agent explain
// reports what the detector saw. A workspace token reported with a seq
// shows on the workspace, and an older seq is dropped. The client takes the
// terminal title a tool sets. status server says the server runs, in
// herdr's text and JSON.
//
// Negative controls: with server.reload_config back in herdrUnsupported, the
// reload step exits 1. With the move-window call taken out of herdrPaneMove,
// the pane stays on its tab.
func TestHerdrFrontReloadMoveRename(t *testing.T) {
	term, base := herdrFrontClient(t)
	crushPanes(t, base, "mover", "agentpane")
	mover := herdrPaneByLabel(t, base, "mover")
	moverID := mover["pane_id"].(string)
	agentID := herdrPaneByLabel(t, base, "agentpane")["pane_id"].(string)
	// The agent pane reports a claude agent, as an agent with herdr support
	// does.
	typeIn(t, base, "agentpane", `"$HERDR_BIN_PATH" pane report-agent "$HERDR_PANE_ID" --source stub --agent claude --state idle --seq 1; echo REPORTED`)
	waitJoined(t, base, "agentpane", "REPORTED")

	steps := runHerdrSteps(t, base, "mover", "reload", `
step reload "$H" server reload-config
step rename "$H" agent rename `+agentID+` reviewer
step get "$H" agent get reviewer
step badname "$H" agent rename `+agentID+` Bad-Name
step explain "$H" agent explain reviewer
step move "$H" pane move "$HERDR_PANE_ID" --new-tab --label moved
NEW_TOPIC=topic=fixing-tests
OLD_TOPIC=topic=older
step meta "$H" workspace report-metadata "$HERDR_WORKSPACE_ID" --source auto-title --token "$NEW_TOPIC" --seq 2
step stale "$H" workspace report-metadata "$HERDR_WORKSPACE_ID" --source auto-title --token "$OLD_TOPIC" --seq 1
step wsget "$H" workspace get "$HERDR_WORKSPACE_ID"
step title "$H" terminal title set "tuios e2e title"
step status "$H" status server
step statusjson "$H" status server --json
`)
	steps["reload"].ok(t, "server reload-config")
	if r := steps["reload"].json(t, "server reload-config"); dig(r, "result", "type") != "config_reload" || dig(r, "result", "status") != "applied" {
		t.Fatalf("server reload-config: %s", steps["reload"].out)
	}
	steps["rename"].ok(t, "agent rename")
	if r := steps["rename"].json(t, "agent rename"); dig(r, "result", "agent", "name") != "reviewer" {
		t.Fatalf("agent rename: %s", steps["rename"].out)
	}
	steps["get"].ok(t, "agent get reviewer")
	if g := steps["get"].json(t, "agent get"); dig(g, "result", "agent", "pane_id") != agentID {
		t.Fatalf("agent get reviewer found %v, want %s", dig(g, "result", "agent", "pane_id"), agentID)
	}
	if b := steps["badname"]; b.code != 1 || !strings.Contains(b.err, `"code":"invalid_agent_name"`) {
		t.Errorf("agent rename to Bad-Name: exit %d, stderr %q; want invalid_agent_name", b.code, b.err)
	}
	steps["explain"].ok(t, "agent explain")
	if e := steps["explain"].json(t, "agent explain"); dig(e, "result", "type") != "agent_explain" || dig(e, "result", "explain") == nil {
		t.Fatalf("agent explain: %s", steps["explain"].out)
	}
	steps["move"].ok(t, "pane move --new-tab")
	mv := steps["move"].json(t, "pane move")
	newTab, _ := dig(mv, "result", "move_result", "pane", "tab_id").(string)
	if dig(mv, "result", "move_result", "changed") != true || newTab == "" || newTab == mover["tab_id"] {
		t.Fatalf("pane move --new-tab did not move the pane to a new tab:\n%s", steps["move"].out)
	}
	if label := dig(mv, "result", "move_result", "created_tab", "label"); label != "moved" {
		t.Errorf("the new tab is labelled %v, want moved", label)
	}
	if dig(mv, "result", "move_result", "previous_pane_id") != moverID {
		t.Errorf("previous_pane_id is %v, want %s", dig(mv, "result", "move_result", "previous_pane_id"), moverID)
	}
	if got := herdrPaneByLabel(t, base, "mover")["tab_id"]; got != newTab {
		t.Fatalf("the daemon puts mover on tab %v, want %s", got, newTab)
	}
	// The metadata shows on the workspace, and a report with an older seq
	// changes nothing.
	steps["meta"].ok(t, "workspace report-metadata")
	steps["stale"].ok(t, "workspace report-metadata, older seq")
	steps["wsget"].ok(t, "workspace get")
	if got := dig(steps["wsget"].json(t, "workspace get"), "result", "workspace", "tokens", "topic"); got != "fixing-tests" {
		t.Fatalf("the workspace's topic token is %v, want fixing-tests:\n%s", got, steps["wsget"].out)
	}
	steps["title"].ok(t, "terminal title set")
	if ti := steps["title"].json(t, "terminal title set"); dig(ti, "result", "type") != "client_window_title" || dig(ti, "result", "changed") != true || dig(ti, "result", "reason") != "set" {
		t.Fatalf("terminal title set: %s", steps["title"].out)
	}
	// Tools check that a server answers with status server, by its text or
	// its JSON.
	steps["status"].ok(t, "status server")
	if !strings.HasPrefix(steps["status"].out, "status: running\n") {
		t.Fatalf("status server: %q", steps["status"].out)
	}
	steps["statusjson"].ok(t, "status server --json")
	if st := steps["statusjson"].json(t, "status server --json"); st["running"] != true || st["protocol"] != float64(22) {
		t.Fatalf("status server --json: %s", steps["statusjson"].out)
	}
	saveFrame(t, term, "herdr-front-reload-move-rename")
	alive(t, term, "after the reload, move and rename sequence")
}
