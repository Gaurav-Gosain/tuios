package session

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// quoteJSON renders a string as a JSON literal, for building a request line that
// carries a filesystem path.
func quoteJSON(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

// The arrangement verbs are driven here the way a caller drives them: over a
// socket, against a daemon that is really running, asserting on the response
// and then on the state the daemon holds. A handler test would pass with a verb
// that was never registered, and registration is half of what these verbs are.

// windowNamed returns the row list-windows reports for a window, by display name.
func windowNamed(t *testing.T, c *verbConn, session, name string) map[string]any {
	t.Helper()
	res := result(t, c.call(t, `{"verb":"list-windows","params":{"session":"`+session+`"}}`))
	for _, w := range res["windows"].([]any) {
		row := w.(map[string]any)
		if row["display_name"] == name {
			return row
		}
	}
	t.Fatalf("no window named %q in %v", name, res["windows"])
	return nil
}

// TestNewWindowPlacesWhereItIsTold pins the placement parameters. Creating a
// window and moving it afterwards leaves it on the wrong workspace for as long
// as the two calls take, which an attached client draws.
func TestNewWindowPlacesWhereItIsTold(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "place")
	c := dialVerb(t, sp)

	dir := t.TempDir()
	res := result(t, c.call(t, `{"verb":"new-window","params":{"session":"place","name":"tests","workspace":3,"cwd":`+quoteJSON(dir)+`,"focus":false}}`))

	if res["workspace"] != float64(3) {
		t.Errorf("workspace = %v, want 3", res["workspace"])
	}
	if res["focused"] != false {
		t.Errorf("focused = %v, want false", res["focused"])
	}
	id, _ := res["window_id"].(string)
	if id == "" {
		t.Fatal("no window_id returned")
	}

	// The daemon's own state has to agree, because that is what every read verb
	// answers from.
	state := sess.GetState()
	idx, err := findWindowStateIndex(state.Windows, id)
	if err != nil {
		t.Fatalf("created window is not in daemon state: %v", err)
	}
	if got := state.Windows[idx].Workspace; got != 3 {
		t.Errorf("daemon state workspace = %d, want 3", got)
	}
	// focus:false has to leave the focus alone. Reporting it and moving it anyway
	// is the failure this guards.
	if state.FocusedWindowID == id {
		t.Error("focus:false still took the focus")
	}
}

// TestNewWindowRefusesAnUnusableCwd pins the refusal. The PTY falls back to the
// daemon's own directory when a cwd cannot be entered, so a caller that mistyped
// a path would get a working shell in the wrong place and a success envelope.
func TestNewWindowRefusesAnUnusableCwd(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "cwd")
	c := dialVerb(t, sp)

	missing := filepath.Join(t.TempDir(), "nowhere")
	resp := c.call(t, `{"verb":"new-window","params":{"session":"cwd","cwd":`+quoteJSON(missing)+`}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestNewWindowRefusesAWorkspaceOutOfRange checks the bound is reported with the
// range, so a caller knows what would have worked.
func TestNewWindowRefusesAWorkspaceOutOfRange(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "bounds")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"verb":"new-window","params":{"session":"bounds","workspace":99}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	hint := resp["error"].(map[string]any)["hint"].(map[string]any)
	if hint["param"] != "workspace" {
		t.Errorf("hint.param = %v, want workspace", hint["param"])
	}
}

// TestFocusWindowByEveryName drives the three ways focus-window names a pane and
// checks each reports where the focus landed. A relative or directional move
// cannot say in advance which pane it picks, so reporting it is the contract.
func TestFocusWindowByEveryName(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "focus")
	c := dialVerb(t, sp)

	first := result(t, c.call(t, `{"verb":"new-window","params":{"session":"focus","name":"one"}}`))["window_id"].(string)
	second := result(t, c.call(t, `{"verb":"new-window","params":{"session":"focus","name":"two"}}`))["window_id"].(string)

	res := result(t, c.call(t, `{"verb":"focus-window","params":{"session":"focus","window":"one"}}`))
	if res["focused_window_id"] != first {
		t.Errorf("focused = %v, want %v", res["focused_window_id"], first)
	}
	if res["window"] == nil {
		t.Error("focus-window reported no window row")
	}

	res = result(t, c.call(t, `{"verb":"focus-window","params":{"session":"focus","relative":"next"}}`))
	if res["focused_window_id"] == first {
		t.Error("relative:next did not move the focus")
	}
	_ = second

	// Exactly one selector: three ways to name one pane, so two of them together
	// is a caller that has not decided.
	resp := c.call(t, `{"verb":"focus-window","params":{"session":"focus","window":"one","relative":"next"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("two selectors gave %q, want %q", code, ErrVerbInvalidParams)
	}
	resp = c.call(t, `{"verb":"focus-window","params":{"session":"focus"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("no selector gave %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestFocusDirectionNeedsAClient pins the seam. A direction is a question about
// the viewport, and the daemon has none, so it says so rather than guessing.
func TestFocusDirectionNeedsAClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "dir")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"verb":"focus-window","params":{"session":"dir","direction":"left"}}`)
	if code := errCode(t, resp); code != ErrVerbNeedsClient {
		t.Fatalf("code = %q, want %q", code, ErrVerbNeedsClient)
	}
}

// TestMoveWindowMovesTheOneItWasGiven is the point of the verb. The tape command
// behind it only ever moved the focused window, so moving a named one meant
// focusing it first, which is a visible change nobody asked for.
func TestMoveWindowMovesTheOneItWasGiven(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "move")
	c := dialVerb(t, sp)

	target := result(t, c.call(t, `{"verb":"new-window","params":{"session":"move","name":"movable"}}`))["window_id"].(string)
	other := result(t, c.call(t, `{"verb":"new-window","params":{"session":"move","name":"stays"}}`))["window_id"].(string)
	focusedBefore := sess.GetState().FocusedWindowID
	if focusedBefore != other {
		t.Fatalf("expected the last created window to hold the focus, got %q", focusedBefore)
	}

	res := result(t, c.call(t, `{"verb":"move-window","params":{"session":"move","window":"movable","workspace":4}}`))
	if res["window_id"] != target {
		t.Errorf("window_id = %v, want %v", res["window_id"], target)
	}
	if res["workspace"] != float64(4) {
		t.Errorf("workspace = %v, want 4", res["workspace"])
	}
	if res["from_workspace"] != float64(1) {
		t.Errorf("from_workspace = %v, want 1", res["from_workspace"])
	}

	state := sess.GetState()
	idx, _ := findWindowStateIndex(state.Windows, target)
	if state.Windows[idx].Workspace != 4 {
		t.Errorf("daemon state workspace = %d, want 4", state.Windows[idx].Workspace)
	}
	// Without follow, the view stays where it was and so does the focus.
	if state.CurrentWorkspace != 1 {
		t.Errorf("current workspace = %d, want 1 without follow", state.CurrentWorkspace)
	}
	if state.FocusedWindowID != focusedBefore {
		t.Errorf("moving a named window changed the focus to %q", state.FocusedWindowID)
	}

	res = result(t, c.call(t, `{"verb":"move-window","params":{"session":"move","window":"stays","workspace":5,"follow":true}}`))
	if res["current_workspace"] != float64(5) {
		t.Errorf("follow did not switch the view: %v", res["current_workspace"])
	}
}

// TestSelectWorkspaceAndListWorkspaces drives the pair a caller uses to decide
// where to put something.
func TestSelectWorkspaceAndListWorkspaces(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "spaces")
	c := dialVerb(t, sp)

	c.call(t, `{"verb":"new-window","params":{"session":"spaces","name":"two","workspace":2}}`)
	c.call(t, `{"verb":"set-workspace-name","params":{"session":"spaces","workspace":2,"name":"review"}}`)

	res := result(t, c.call(t, `{"verb":"list-workspaces","params":{"session":"spaces"}}`))
	spaces := res["workspaces"].([]any)
	if len(spaces) == 0 {
		t.Fatal("list-workspaces reported none")
	}
	ws2 := spaces[1].(map[string]any)
	if ws2["name"] != "review" {
		t.Errorf("workspace 2 name = %v, want review", ws2["name"])
	}
	if ws2["window_count"] != float64(1) {
		t.Errorf("workspace 2 holds %v windows, want 1", ws2["window_count"])
	}

	res = result(t, c.call(t, `{"verb":"select-workspace","params":{"session":"spaces","workspace":2}}`))
	if res["current_workspace"] != float64(2) {
		t.Errorf("current_workspace = %v, want 2", res["current_workspace"])
	}
	if res["window_count"] != float64(1) {
		t.Errorf("window_count = %v, want 1", res["window_count"])
	}

	resp := c.call(t, `{"verb":"select-workspace","params":{"session":"spaces","workspace":99}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("out of range gave %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestSetWindowRenamesAndMinimizes checks the one verb does both, and reports
// the window as it ends up rather than echoing the request.
func TestSetWindowRenamesAndMinimizes(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "setwin")
	c := dialVerb(t, sp)

	c.call(t, `{"verb":"new-window","params":{"session":"setwin","name":"before"}}`)

	res := result(t, c.call(t, `{"verb":"set-window","params":{"session":"setwin","window":"before","name":"after","minimized":true}}`))
	if res["display_name"] != "after" {
		t.Errorf("display_name = %v, want after", res["display_name"])
	}
	if res["minimized"] != true {
		t.Errorf("minimized = %v, want true", res["minimized"])
	}
	row := windowNamed(t, c, "setwin", "after")
	if row["minimized"] != true {
		t.Error("list-windows disagrees about minimized")
	}

	// Clearing the name falls back to the shell's title, which is why the result
	// reports the window rather than the string that was sent.
	res = result(t, c.call(t, `{"verb":"set-window","params":{"session":"setwin","window":"after","name":""}}`))
	if res["display_name"] == "after" {
		t.Error("clearing the name left the custom name in place")
	}

	resp := c.call(t, `{"verb":"set-window","params":{"session":"setwin","window":"after"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("a set-window with nothing to set gave %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestSplitAndLayoutNeedAClient pins the other half of the seam. Both compute a
// geometry, so both refuse rather than record something no renderer will honour.
func TestSplitAndLayoutNeedAClient(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "geo")
	c := dialVerb(t, sp)

	resp := c.call(t, `{"verb":"split-window","params":{"session":"geo","direction":"vertical"}}`)
	if code := errCode(t, resp); code != ErrVerbNeedsClient {
		t.Errorf("split-window gave %q, want %q", code, ErrVerbNeedsClient)
	}
	resp = c.call(t, `{"verb":"set-layout","params":{"session":"geo","tiling":true}}`)
	if code := errCode(t, resp); code != ErrVerbNeedsClient {
		t.Errorf("set-layout gave %q, want %q", code, ErrVerbNeedsClient)
	}

	// A bad direction is caught before the client is consulted, so the caller is
	// told about its own mistake rather than about the missing renderer.
	resp = c.call(t, `{"verb":"split-window","params":{"session":"geo","direction":"sideways"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("a bad direction gave %q, want %q", code, ErrVerbInvalidParams)
	}
	resp = c.call(t, `{"verb":"set-layout","params":{"session":"geo"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("a set-layout with nothing to set gave %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestRunCommandReachesTheTapeVocabulary checks the escape hatch runs a
// daemon-owned command with nobody attached and says which side ran it.
func TestRunCommandReachesTheTapeVocabulary(t *testing.T) {
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "tape")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"verb":"run-command","params":{"session":"tape","command":"SwitchWorkspace","args":["3"]}}`))
	if res["routed"] != false {
		t.Errorf("routed = %v, want false with no client attached", res["routed"])
	}
	if got := sess.GetState().CurrentWorkspace; got != 3 {
		t.Errorf("current workspace = %d, want 3", got)
	}

	resp := c.call(t, `{"verb":"run-command","params":{"session":"tape"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("a missing command gave %q, want %q", code, ErrVerbInvalidParams)
	}
}
