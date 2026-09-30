package session

import (
	"bufio"
	"encoding/json"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

// herdrDial sends one request to the herdr socket and returns the reply.
func herdrDial(t *testing.T, sp, method string, params any) map[string]any {
	t.Helper()
	conn, err := net.Dial("unix", HerdrSocketPath(sp))
	if err != nil {
		t.Fatalf("dial herdr socket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	line, _ := json.Marshal(map[string]any{"id": "t1", "method": method, "params": params})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("%s: read reply: %v", method, err)
	}
	var out map[string]any
	if err := json.Unmarshal(reply, &out); err != nil {
		t.Fatalf("%s: reply %q: %v", method, reply, err)
	}
	return out
}

// herdrOK is a reply's result, failing the test on an error reply.
func herdrOK(t *testing.T, what string, reply map[string]any) map[string]any {
	t.Helper()
	if e, ok := reply["error"]; ok {
		t.Fatalf("%s: error %v", what, e)
	}
	res, ok := reply["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no result in %v", what, reply)
	}
	return res
}

// herdrCode is a reply's error code, "" for a success.
func herdrCode(reply map[string]any) string {
	e, ok := reply["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := e["code"].(string)
	return code
}

// herdrCallAs sends one request over the socket, as the pane the daemon's
// placer names, and returns the result or "code: message".
func herdrCallAs(t *testing.T, sp, method string, params any) (map[string]any, string) {
	t.Helper()
	reply := herdrDial(t, sp, method, params)
	if e, ok := reply["error"].(map[string]any); ok {
		return nil, e["code"].(string) + ": " + e["message"].(string)
	}
	return reply["result"].(map[string]any), ""
}

func TestHerdrIDs(t *testing.T) {
	sid, wid := "5f0c8a1e-7b2d-4c3e-9f10-aabbccddeeff", "0d1e2f3a-4b5c-6d7e-8f90-112233445566"
	if got := herdrWorkspaceID(sid); got != "w5f0c8a1e7b2d" {
		t.Errorf("workspace id %q", got)
	}
	if got := herdrTabID(sid, 3); got != "w5f0c8a1e7b2d:t3" {
		t.Errorf("tab id %q", got)
	}
	if got := herdrPaneID(sid, wid); got != "w5f0c8a1e7b2d:p0d1e2f3a4b5c" {
		t.Errorf("pane id %q", got)
	}
}

func TestHerdrFindsObjectsByEveryForm(t *testing.T) {
	d, _, a1, _, b1 := scopeFixture(t)
	sa := d.manager.GetSession("a")
	for _, id := range []string{herdrPaneID(sa.ID, a1), a1, a1[:8], strings.ReplaceAll(a1, "-", "")} {
		if _, w, err := d.herdrFindPane(id); err != nil || w.ID != a1 {
			t.Errorf("pane %q found %v, %v", id, w.ID, err)
		}
	}
	// A window found by its window part, whatever session part the id has.
	sb := d.manager.GetSession("b")
	if _, w, err := d.herdrFindPane(herdrPaneID(sa.ID, b1)); err != nil || w.ID != b1 {
		t.Errorf("pane with a stale session part: %v, %v", w.ID, err)
	}
	if s, err := d.herdrFindSession(herdrWorkspaceID(sb.ID)); err != nil || s != sb {
		t.Errorf("workspace: %v", err)
	}
	if s, n, err := d.herdrFindTab(herdrTabID(sa.ID, 1)); err != nil || s != sa || n != 1 {
		t.Errorf("tab: %v %d %v", s, n, err)
	}
	for _, bad := range []string{"", "p", "w:p", "wzz:p1234", herdrTabID(sa.ID, 99)} {
		if _, _, err := d.herdrFindPane(bad); err == nil || err.code != "pane_not_found" {
			t.Errorf("pane %q: %v", bad, err)
		}
	}
	for _, n := range []int{2, 99} {
		// 2 is an empty workspace, which is not a tab; 99 is past the last.
		if _, _, err := d.herdrFindTab(herdrTabID(sa.ID, n)); err == nil || err.code != "tab_not_found" {
			t.Errorf("tab %d: %v", n, err)
		}
	}
	if _, err := d.herdrFindSession("w000000000000"); err == nil || err.code != "workspace_not_found" {
		t.Errorf("unknown workspace: %v", err)
	}
}

// TestHerdrSnapshotShape reads session.snapshot over the socket, from outside
// every pane, and holds each record to the fields herdr's schema requires.
func TestHerdrSnapshotShape(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	sa := d.manager.GetSession("a")
	res := herdrOK(t, "snapshot", herdrDial(t, sp, "session.snapshot", map[string]any{}))
	if res["type"] != "session_snapshot" {
		t.Fatalf("type %v", res["type"])
	}
	snap := res["snapshot"].(map[string]any)
	if v, _ := snap["version"].(string); !strings.HasPrefix(v, herdrTargetVersion) || snap["protocol"] != float64(herdrTargetProtocol) {
		t.Errorf("version %v protocol %v", snap["version"], snap["protocol"])
	}
	required := map[string][]string{
		"workspaces": {"workspace_id", "number", "label", "focused", "pane_count", "tab_count", "active_tab_id", "agent_status"},
		"tabs":       {"tab_id", "workspace_id", "number", "label", "focused", "pane_count", "agent_status"},
		"panes":      {"pane_id", "terminal_id", "workspace_id", "tab_id", "focused", "agent_status", "revision"},
		"layouts":    {"workspace_id", "tab_id", "zoomed", "area", "focused_pane_id", "panes", "splits"},
	}
	for list, fields := range required {
		items, ok := snap[list].([]any)
		if !ok {
			t.Fatalf("%s is %T", list, snap[list])
		}
		for _, it := range items {
			m := it.(map[string]any)
			for _, f := range fields {
				if _, ok := m[f]; !ok {
					t.Errorf("%s record %v has no %s", list, m, f)
				}
			}
		}
	}
	if _, ok := snap["agents"].([]any); !ok {
		t.Errorf("agents is %T", snap["agents"])
	}
	if n := len(snap["workspaces"].([]any)); n != 2 {
		t.Errorf("%d workspaces, want 2", n)
	}
	var ids []string
	for _, p := range snap["panes"].([]any) {
		m := p.(map[string]any)
		ids = append(ids, m["pane_id"].(string))
		if m["agent_status"] != "unknown" {
			t.Errorf("a shell pane's status is %v", m["agent_status"])
		}
		if m["tab_id"] != herdrTabID(sa.ID, 1) && strings.HasPrefix(m["pane_id"].(string), herdrWorkspaceID(sa.ID)) {
			t.Errorf("pane %v is on tab %v", m["pane_id"], m["tab_id"])
		}
	}
	for _, w := range []string{a1, a2, b1} {
		if !slices.ContainsFunc(ids, func(id string) bool { return strings.HasSuffix(id, ":p"+herdrHex(w)) }) {
			t.Errorf("window %s is not in %v", w, ids)
		}
	}
	// An agent appears in its pane's status, in its tab's and workspace's
	// summary, and in agents.
	result(t, callP(dialVerb(t, sp), t, "set-agent-state",
		map[string]any{"session": "a", "window": a2, "state": "needs_input", "harness": "claude-code", "message": "Allow?"}))
	snap = herdrOK(t, "snapshot", herdrDial(t, sp, "session.snapshot", nil))["snapshot"].(map[string]any)
	found := false
	for _, p := range snap["panes"].([]any) {
		m := p.(map[string]any)
		if m["pane_id"] == herdrPaneID(sa.ID, a2) {
			found = true
			if m["agent"] != "claude" || m["agent_status"] != "blocked" {
				t.Errorf("agent pane %v", m)
			}
		}
	}
	if !found {
		t.Fatal("the agent pane is gone")
	}
	for _, w := range snap["workspaces"].([]any) {
		m := w.(map[string]any)
		if m["workspace_id"] == herdrWorkspaceID(sa.ID) && m["agent_status"] != "blocked" {
			t.Errorf("workspace summary %v", m["agent_status"])
		}
	}
	if agents := snap["agents"].([]any); len(agents) != 1 || agents[0].(map[string]any)["agent_status"] != "blocked" {
		t.Errorf("agents %v", agents)
	}
}

// TestHerdrReadAndType types into a pane through the herdr socket and reads it
// back, text and keys, the way Collie sends a reply.
func TestHerdrReadAndType(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess, err := d.manager.CreateSession("rw", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Command: []string{"cat"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pane := herdrPaneID(sess.ID, win.ID)
	herdrOK(t, "send_text", herdrDial(t, sp, "pane.send_text", map[string]any{"pane_id": pane, "text": "hello, herdr"}))
	herdrOK(t, "send_keys", herdrDial(t, sp, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"Enter", ",", "x", "Enter"}}))
	var text string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res := herdrOK(t, "read", herdrDial(t, sp, "pane.read", map[string]any{"pane_id": pane, "source": "visible", "lines": 40, "format": "text"}))
		read := res["read"].(map[string]any)
		text, _ = read["text"].(string)
		if strings.Count(text, "hello, herdr") >= 2 && strings.Count(text, ",x") >= 2 {
			if read["pane_id"] != pane || read["workspace_id"] != herdrWorkspaceID(sess.ID) || read["source"] != "visible" {
				t.Errorf("read record %v", read)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Count(text, "hello, herdr") < 2 || strings.Count(text, ",x") < 2 {
		t.Fatalf("cat did not echo the text and keys: %q", text)
	}
	recent := herdrOK(t, "read recent", herdrDial(t, sp, "pane.read", map[string]any{"pane_id": pane, "source": "recent", "lines": 1}))["read"].(map[string]any)
	if strings.Count(recent["text"].(string), "\n") > 1 {
		t.Errorf("a one-line read gave %q", recent["text"])
	}
	if code := herdrCode(herdrDial(t, sp, "pane.send_keys", map[string]any{"pane_id": pane, "keys": []string{"Enter", "cmd+k"}})); code != "invalid_key" {
		t.Errorf("cmd+k answered %q, want invalid_key", code)
	}
	if code := herdrCode(herdrDial(t, sp, "pane.read", map[string]any{"pane_id": pane, "source": "sideways"})); code != "invalid_request" {
		t.Errorf("a bad source answered %q", code)
	}
}

func TestHerdrKeyRuns(t *testing.T) {
	cases := []struct {
		keys []string
		want []herdrKeyRun
		bad  string
	}{
		{[]string{"ctrl+c"}, []herdrKeyRun{{keys: "ctrl+c"}}, ""},
		{[]string{"C-c"}, []herdrKeyRun{{keys: "ctrl+c"}}, ""},
		{[]string{"Down", "Enter", "1"}, []herdrKeyRun{{keys: "Down Enter"}, {keys: "1", literal: true}}, ""},
		{[]string{"2", ",", "space", "Enter"}, []herdrKeyRun{{keys: "2, ", literal: true}, {keys: "Enter"}}, ""},
		{[]string{"shift+tab", "alt+f", "Control+Shift+p"}, []herdrKeyRun{{keys: "shift+Tab alt+f ctrl+shift+p"}}, ""},
		{[]string{"return", "esc", "bs", "F12"}, []herdrKeyRun{{keys: "Enter Escape Backspace F12"}}, ""},
		{[]string{"minus", "+"}, []herdrKeyRun{{keys: "-+", literal: true}}, ""},
		{[]string{"A"}, []herdrKeyRun{{keys: "A", literal: true}}, ""},
		{[]string{"Enter", "cmd+k"}, nil, "cmd+k"},
		{[]string{"F13"}, nil, "F13"},
		{[]string{"ctrl++"}, nil, "ctrl++"},
		{[]string{"nope"}, nil, "nope"},
	}
	for _, c := range cases {
		got, bad := herdrKeyRuns(c.keys)
		if bad != c.bad || !slices.Equal(got, c.want) {
			t.Errorf("%q: got %+v bad %q, want %+v bad %q", c.keys, got, bad, c.want, c.bad)
		}
	}
}

func TestHerdrUnknownAndUnsupportedMethods(t *testing.T) {
	_, sp := startTestDaemon(t)
	cases := []struct {
		method string
		params any
		code   string
	}{
		{"plugin.list", map[string]any{}, "unsupported"},
		{"pane.zoom", map[string]any{}, "unsupported"},
		{"no.such_method", map[string]any{}, "invalid_request"},
		{"pane.get", map[string]any{}, "invalid_request"},
		{"pane.get", map[string]any{"pane_id": "w1:p1"}, "pane_not_found"},
		{"tab.get", map[string]any{"tab_id": "w1:t1"}, "tab_not_found"},
		{"workspace.get", map[string]any{"workspace_id": "w1"}, "workspace_not_found"},
		{"events.subscribe", map[string]any{"subscriptions": []any{map[string]any{"type": "pane.agent_status_changed"}}}, "invalid_request"},
		{"events.subscribe", map[string]any{"subscriptions": []any{map[string]any{"type": "tab.exploded"}}}, "invalid_request"},
	}
	for _, c := range cases {
		reply := herdrDial(t, sp, c.method, c.params)
		if got := herdrCode(reply); got != c.code {
			t.Errorf("%s %v answered %q, want %q (%v)", c.method, c.params, got, c.code, reply)
		}
		if reply["id"] != "t1" {
			t.Errorf("%s: id %v", c.method, reply["id"])
		}
	}
	pong := herdrOK(t, "ping", herdrDial(t, sp, "ping", nil))
	if pong["type"] != "pong" || pong["server"] != "tuios" {
		t.Errorf("pong %v", pong)
	}
}

// TestHerdrStructureMethods creates, renames, focuses and closes tabs,
// panes and workspaces through the herdr socket as the person.
func TestHerdrStructureMethods(t *testing.T) {
	d, sp := startTestDaemon(t)
	dir := t.TempDir()
	ws := herdrOK(t, "workspace.create", herdrDial(t, sp, "workspace.create", map[string]any{"cwd": dir, "focus": false, "label": "Demo"}))
	if ws["type"] != "workspace_created" {
		t.Fatalf("type %v", ws["type"])
	}
	w := ws["workspace"].(map[string]any)
	root := ws["root_pane"].(map[string]any)
	if w["label"] != "Demo" || root["workspace_id"] != w["workspace_id"] || ws["tab"].(map[string]any)["tab_id"] != root["tab_id"] {
		t.Fatalf("workspace.create %v", ws)
	}
	wsID := w["workspace_id"].(string)

	tab := herdrOK(t, "tab.create", herdrDial(t, sp, "tab.create", map[string]any{"workspace_id": wsID, "focus": false, "label": "logs"}))
	tb := tab["tab"].(map[string]any)
	if tab["type"] != "tab_created" || tb["label"] != "logs" || tb["number"] != float64(2) || tab["root_pane"] == nil {
		t.Fatalf("tab.create %v", tab)
	}
	tabID := tb["tab_id"].(string)
	renamed := herdrOK(t, "tab.rename", herdrDial(t, sp, "tab.rename", map[string]any{"tab_id": tabID, "label": "build"}))
	if renamed["tab"].(map[string]any)["label"] != "build" {
		t.Errorf("tab.rename %v", renamed)
	}
	paneID := tab["root_pane"].(map[string]any)["pane_id"].(string)
	p := herdrOK(t, "pane.rename", herdrDial(t, sp, "pane.rename", map[string]any{"pane_id": paneID, "label": "watcher"}))
	if p["type"] != "pane_info" || p["pane"].(map[string]any)["label"] != "watcher" {
		t.Errorf("pane.rename %v", p)
	}
	p = herdrOK(t, "pane.rename null", herdrDial(t, sp, "pane.rename", map[string]any{"pane_id": paneID, "label": nil}))
	if _, has := p["pane"].(map[string]any)["label"]; has {
		t.Errorf("pane.rename null kept %v", p["pane"].(map[string]any)["label"])
	}
	f := herdrOK(t, "pane.focus", herdrDial(t, sp, "pane.focus", map[string]any{"pane_id": paneID}))
	if f["pane"].(map[string]any)["tab_id"] != tabID {
		t.Errorf("pane.focus %v", f)
	}
	split := herdrOK(t, "pane.split", herdrDial(t, sp, "pane.split", map[string]any{"target_pane_id": paneID, "direction": "right"}))
	if split["pane"].(map[string]any)["tab_id"] != tabID {
		t.Errorf("pane.split put the pane on %v", split["pane"].(map[string]any)["tab_id"])
	}
	herdrOK(t, "tab.close", herdrDial(t, sp, "tab.close", map[string]any{"tab_id": tabID}))
	tabs := herdrOK(t, "tab.list", herdrDial(t, sp, "tab.list", map[string]any{"workspace_id": wsID}))["tabs"].([]any)
	for _, x := range tabs {
		if x.(map[string]any)["tab_id"] == tabID {
			t.Errorf("tab %s is still listed after tab.close", tabID)
		}
	}
	herdrOK(t, "workspace.rename", herdrDial(t, sp, "workspace.rename", map[string]any{"workspace_id": wsID, "label": "Renamed"}))
	herdrOK(t, "workspace.close", herdrDial(t, sp, "workspace.close", map[string]any{"workspace_id": wsID}))
	if _, err := d.herdrFindSession(wsID); err == nil {
		t.Error("the workspace is still there after workspace.close")
	}
}

// TestHerdrGrantsHoldAPane: a herdr client in a pane is held to the pane's
// grants exactly as a verb call is.
func TestHerdrGrantsHoldAPane(t *testing.T) {
	d, sp, a1, a2, b1 := scopeFixture(t)
	sa, sb := d.manager.GetSession("a"), d.manager.GetSession("b")
	setStrict(d) // read, write, fan
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })

	snap, e := herdrCallAs(t, sp, "session.snapshot", nil)
	if e != "" {
		t.Fatal(e)
	}
	spaces := snap["snapshot"].(map[string]any)["workspaces"].([]any)
	if len(spaces) != 1 || spaces[0].(map[string]any)["workspace_id"] != herdrWorkspaceID(sa.ID) {
		t.Errorf("a pane without admin reads %v, want only its own session", spaces)
	}
	if _, e := herdrCallAs(t, sp, "pane.read", map[string]any{"pane_id": herdrPaneID(sb.ID, b1), "source": "visible"}); !strings.HasPrefix(e, "forbidden") {
		t.Errorf("read of another session answered %q", e)
	}
	if _, e := herdrCallAs(t, sp, "pane.send_text", map[string]any{"pane_id": herdrPaneID(sb.ID, b1), "text": "x"}); !strings.HasPrefix(e, "forbidden") {
		t.Errorf("typing into another session answered %q", e)
	}
	if _, e := herdrCallAs(t, sp, "pane.send_text", map[string]any{"pane_id": herdrPaneID(sa.ID, a2), "text": "x"}); e != "" {
		t.Errorf("typing into its own session: %s", e)
	}
	for _, m := range []struct {
		method string
		params map[string]any
	}{
		{"pane.close", map[string]any{"pane_id": herdrPaneID(sa.ID, a2)}},
		{"pane.rename", map[string]any{"pane_id": herdrPaneID(sa.ID, a2), "label": "x"}},
		{"pane.focus", map[string]any{"pane_id": herdrPaneID(sa.ID, a2)}},
		{"tab.create", map[string]any{"workspace_id": herdrWorkspaceID(sa.ID)}},
		{"workspace.create", map[string]any{}},
		{"workspace.close", map[string]any{"workspace_id": herdrWorkspaceID(sb.ID)}},
	} {
		if _, e := herdrCallAs(t, sp, m.method, m.params); !strings.HasPrefix(e, "forbidden") {
			t.Errorf("%s without admin answered %q", m.method, e)
		}
	}
	if len(sa.GetState().Windows) != 2 || d.manager.GetSession("b") == nil {
		t.Fatal("a refused call changed something")
	}

	// A pane waiting on a prompt answers what is typed into it: that needs
	// respond, which admin does not give.
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, callP(dialVerb(t, sp), t, "set-agent-state", map[string]any{"session": "a", "window": a2, "state": "needs_input", "harness": "claude-code"}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a1 })
	setStrict(d, "admin")
	for _, m := range []string{"pane.send_text", "pane.send_keys", "pane.send_input"} {
		params := map[string]any{"pane_id": herdrPaneID(sa.ID, a2), "text": "1", "keys": []string{"1", "Enter"}}
		if _, e := herdrCallAs(t, sp, m, params); !strings.HasPrefix(e, "forbidden") || !strings.Contains(e, "respond") {
			t.Errorf("%s into a prompt without respond answered %q", m, e)
		}
	}
	setStrict(d, "admin", "respond")
	if _, e := herdrCallAs(t, sp, "pane.send_keys", map[string]any{"pane_id": herdrPaneID(sa.ID, a2), "keys": []string{"1"}}); e != "" {
		t.Errorf("with respond: %s", e)
	}

	// The person, outside every pane, reads and types everywhere.
	setStrict(d)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	if n := len(herdrOK(t, "snapshot", herdrDial(t, sp, "session.snapshot", nil))["snapshot"].(map[string]any)["workspaces"].([]any)); n != 2 {
		t.Errorf("the person reads %d workspaces", n)
	}
	herdrOK(t, "person types into a prompt", herdrDial(t, sp, "pane.send_text", map[string]any{"pane_id": herdrPaneID(sa.ID, a2), "text": "1"}))
}

// TestHerdrReportsStillOnlyForThemselves: the report methods keep their rule
// under the full API, by either pane id form.
func TestHerdrReportsStillOnlyForThemselves(t *testing.T) {
	f := newHerdrFixture(t)
	f.caller = f.a
	if _, e := f.call(t, "pane.report_agent", map[string]any{"pane_id": herdrPaneID(f.sess.ID, f.a), "source": "crush", "agent": "crush", "state": "working", "seq": 1}); e != "" {
		t.Fatalf("a report by herdr's id form: %s", e)
	}
	f.wantState(t, f.a, AgentStateWorking, "report by herdr id")
	if _, e := f.call(t, "pane.report_agent", map[string]any{"pane_id": herdrPaneID(f.sess.ID, f.b), "source": "crush", "agent": "crush", "state": "working", "seq": 2}); !strings.HasPrefix(e, "forbidden") {
		t.Fatalf("a report for another pane by herdr's id form answered %q", e)
	}
}

// TestHerdrEventsTranslate: tuios's events become herdr's, with the kinds
// and fields herdr's stream carries.
func TestHerdrEventsTranslate(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "ev")
	tr := d.newHerdrTranslator()
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{Workspace: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.translate(streamEvent{Type: EventWindowCreated, Session: "ev", Window: win.ID})
	if len(got) != 2 || got[0].Event != "tab_created" || got[1].Event != "pane_created" {
		t.Fatalf("window-created on an empty workspace gave %+v", got)
	}
	if got[1].Data.Type != "pane_created" || got[1].Data.Pane == nil || got[1].Data.Pane.PaneID != herdrPaneID(sess.ID, win.ID) {
		t.Errorf("pane_created data %v", got[1].Data)
	}
	result(t, callP(dialVerb(t, sp), t, "set-agent-state", map[string]any{"session": "ev", "window": win.ID, "state": "needs_input", "harness": "codex"}))
	got = tr.translate(streamEvent{Type: EventAgentState, Session: "ev", Window: win.ID})
	if len(got) != 2 || got[0].Event != "pane_agent_detected" || got[1].Event != "pane.agent_status_changed" {
		t.Fatalf("agent-state gave %+v", got)
	}
	if got[1].Data.AgentStatus != "blocked" || got[1].Data.Agent != "codex" || got[1].window != win.ID {
		t.Errorf("status event %v", got[1].Data)
	}
	if got[1].Data.Type != "" {
		t.Error("a pane's own event carries type, which herdr's does not")
	}
	subs := []herdrSubscription{{Type: "pane.agent_status_changed", window: "other"}}
	if herdrWanted(subs, got[1]) {
		t.Error("a status event reached a subscription for another pane")
	}
	result(t, callP(dialVerb(t, sp), t, "close-window", map[string]any{"session": "ev", "window": win.ID}))
	got = tr.translate(streamEvent{Type: EventWindowClosed, Session: "ev", Window: win.ID})
	if len(got) != 2 || got[0].Event != "pane_closed" || got[1].Event != "tab_closed" {
		t.Fatalf("window-closed gave %+v", got)
	}
}

// TestHerdrEventStream subscribes over the socket and sees an agent's status
// change arrive as herdr's line.
func TestHerdrEventStream(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "st")
	w := sess.GetState().Windows[0].ID
	pane := herdrPaneID(sess.ID, w)
	conn, err := net.Dial("unix", HerdrSocketPath(sp))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	req, _ := json.Marshal(map[string]any{"id": "es1", "method": "events.subscribe", "params": map[string]any{"subscriptions": []any{
		map[string]any{"type": "pane.created"}, map[string]any{"type": "pane.agent_detected"},
		map[string]any{"type": "pane.agent_status_changed", "pane_id": pane},
	}}})
	_, _ = conn.Write(append(req, '\n'))
	r := bufio.NewReader(conn)
	ack, _ := r.ReadBytes('\n')
	if !strings.Contains(string(ack), `"subscription_started"`) || !strings.Contains(string(ack), `"es1"`) {
		t.Fatalf("ack %s", ack)
	}
	result(t, callP(dialVerb(t, sp), t, "set-agent-state", map[string]any{"session": "st", "window": w, "state": "working", "harness": "claude-code"}))
	seen := map[string]map[string]any{}
	for len(seen) < 2 {
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatalf("stream ended after %v: %v", seen, err)
		}
		var ev struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("line %s: %v", line, err)
		}
		seen[ev.Event] = ev.Data
	}
	if d := seen["pane_agent_detected"]; d == nil || d["pane_id"] != pane || d["agent"] != "claude" || d["type"] != "pane_agent_detected" {
		t.Errorf("pane_agent_detected %v", d)
	}
	if d := seen["pane.agent_status_changed"]; d == nil || d["agent_status"] != "working" {
		t.Errorf("pane.agent_status_changed %v", d)
	}
}

// TestHerdrEventsWaitMatchesAStatus: events.wait answers a pane's status
// change, as herdr's does, and refuses any other match with herdr's code.
func TestHerdrEventsWaitMatchesAStatus(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "wt")
	w := sess.GetState().Windows[0].ID
	pane := herdrPaneID(sess.ID, w)
	done := make(chan map[string]any, 1)
	go func() {
		done <- herdrDial(t, sp, "events.wait", map[string]any{"match_event": map[string]any{
			"event": "pane_agent_status_changed", "pane_id": pane, "agent_status": "working",
		}, "timeout_ms": 10000})
	}()
	time.Sleep(200 * time.Millisecond)
	result(t, callP(dialVerb(t, sp), t, "set-agent-state", map[string]any{"session": "wt", "window": w, "state": "working", "harness": "codex"}))
	res := herdrOK(t, "events.wait", <-done)
	ev := res["event"].(map[string]any)
	data := ev["data"].(map[string]any)
	if res["type"] != "wait_matched" || ev["event"] != "pane_agent_status_changed" || data["pane_id"] != pane || data["agent_status"] != "working" {
		t.Errorf("events.wait answered %v", res)
	}
	reply := herdrDial(t, sp, "events.wait", map[string]any{"match_event": map[string]any{"event": "tab_created"}})
	if code := herdrCode(reply); code != "unsupported_event_wait_match" {
		t.Errorf("a tab_created wait answered %q", code)
	}
}
