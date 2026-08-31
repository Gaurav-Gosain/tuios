package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
)

// list-hooks is the answer to "why does my hook not fire?". A dock component
// already reports its exit code, when it last ran and its last error, which is
// why the same question about the dock is answerable. These pin that a hook now
// reports the same three facts, under the same three names, over a real socket.

// startRealHookDaemon starts a daemon whose hooks are real shell commands, so
// the reported exit codes and errors are the ones a shell produced.
func startRealHookDaemon(t *testing.T, table map[string]any) (*Daemon, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Cleanup(useResurrectionDir(t.TempDir()))

	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true, Hooks: table})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)
	sp, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	return d, sp
}

// hookRow returns the single row list-hooks reports for a command, failing if
// there is not exactly one.
func hookRow(t *testing.T, res map[string]any, command string) map[string]any {
	t.Helper()
	rows, _ := res["hooks"].([]any)
	var found []map[string]any
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if ok && m["command"] == command {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("list-hooks reported %d rows for %q, want 1: %v", len(found), command, rows)
	}
	return found[0]
}

// TestListHooksReportsTheTableAsLoaded answers the first half of the question.
// A hook the user believes they wrote and that is not in this list was never
// loaded, which is a different problem from one that ran and failed.
func TestListHooksReportsTheTableAsLoaded(t *testing.T) {
	d, sp := startRealHookDaemon(t, map[string]any{
		"after-new-window": "true",
		"after-attach":     "true",
	})
	makeSessionWithWindow(t, d, "hooked")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-hooks"}`))
	if res["type"] != "hook_list" {
		t.Errorf("type = %v, want hook_list", res["type"])
	}

	rows, _ := res["hooks"].([]any)
	events := map[string]string{}
	for _, r := range rows {
		m := r.(map[string]any)
		events[m["event"].(string)] = m["side"].(string)
	}
	if events["after-new-window"] != "session" {
		t.Errorf("after-new-window is reported on side %q, want session", events["after-new-window"])
	}
	// after-attach describes one client's connection, so the daemon holds no
	// row for it at all.
	if _, ok := events["after-attach"]; ok {
		t.Errorf("the daemon reported a row for after-attach, which only a client runs")
	}

	// The valid event names are reported, so a caller can check a spelling. A
	// misspelled event is dropped when the config loads and never runs.
	names, _ := res["events"].([]any)
	if len(names) != len(hooks.AllEvents()) {
		t.Errorf("events lists %d names, want %d", len(names), len(hooks.AllEvents()))
	}

	// Nothing is attached, so the client half of the table is missing and the
	// result says so rather than implying the client has no hooks.
	if res["client_attached"] != false {
		t.Errorf("client_attached = %v with nothing attached, want false", res["client_attached"])
	}
}

// TestListHooksReportsWhatAFailingHookDid is the whole change in one call. The
// hook ran, it exited non-zero, and the reason is on the wire.
func TestListHooksReportsWhatAFailingHookDid(t *testing.T) {
	d, sp := startRealHookDaemon(t, map[string]any{
		"after-new-window": "echo 'no such display' >&2; exit 4",
	})
	c := dialVerb(t, sp)
	makeSessionWithWindow(t, d, "hooked")

	// Wait for the firing the first window raised.
	var row map[string]any
	waitUntilHookTest(t, "the hook to run and be recorded", func() bool {
		res := result(t, c.call(t, `{"id":1,"verb":"list-hooks","params":{"session":"hooked"}}`))
		rows, _ := res["hooks"].([]any)
		if len(rows) == 0 {
			return false
		}
		row = rows[0].(map[string]any)
		return row["runs"] == float64(1)
	})

	if row["last_exit"] != float64(4) {
		t.Errorf("last_exit = %v, want 4", row["last_exit"])
	}
	lastError, _ := row["last_error"].(string)
	if !strings.Contains(lastError, "no such display") {
		t.Errorf("last_error = %q, want the command's stderr", lastError)
	}
	if lastRun, _ := row["last_run"].(string); lastRun == "" {
		t.Error("last_run is empty, so nothing says when the hook ran")
	}
	if _, ok := row["last_ms"]; !ok {
		t.Error("the row does not say how long the hook took")
	}
}

// TestListHooksReportsAHookThatNeverRan is the other answer the user needs.
// Zero runs and no error means the command is fine and the event never
// happened, which points at the event name rather than at the command.
func TestListHooksReportsAHookThatNeverRan(t *testing.T) {
	d, sp := startRealHookDaemon(t, map[string]any{
		"after-workspace-switch": "true",
	})
	c := dialVerb(t, sp)
	makeSessionWithWindow(t, d, "quiet")

	res := result(t, c.call(t, `{"id":1,"verb":"list-hooks","params":{"session":"quiet"}}`))
	row := hookRow(t, res, "true")
	if row["runs"] != float64(0) {
		t.Errorf("runs = %v, want 0", row["runs"])
	}
	if row["last_run"] != "" {
		t.Errorf("last_run = %v, want empty for a hook that never ran", row["last_run"])
	}
	if row["last_error"] != "" {
		t.Errorf("last_error = %v, want empty for a hook that never ran", row["last_error"])
	}
}

// TestListHooksFiltersByEvent keeps the call usable on a full table.
func TestListHooksFiltersByEvent(t *testing.T) {
	d, sp := startRealHookDaemon(t, map[string]any{
		"after-new-window":   "echo one",
		"after-close-window": "echo two",
	})
	c := dialVerb(t, sp)
	makeSessionWithWindow(t, d, "filtered")

	res := result(t, c.call(t, `{"id":1,"verb":"list-hooks","params":{"session":"filtered","event":"after-close-window"}}`))
	if res["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", res["total"])
	}
	rows := res["hooks"].([]any)
	if rows[0].(map[string]any)["event"] != "after-close-window" {
		t.Errorf("the filter returned %v", rows[0])
	}
}

// TestListHooksRefusesAnUnknownEvent turns the most common mistake into a
// message that names it. An event outside the set is dropped when the config
// loads, so a hook written on one silently never runs.
func TestListHooksRefusesAnUnknownEvent(t *testing.T) {
	d, sp := startRealHookDaemon(t, map[string]any{"after-new-window": "true"})
	c := dialVerb(t, sp)
	makeSessionWithWindow(t, d, "typo")

	resp := c.call(t, `{"id":1,"verb":"list-hooks","params":{"session":"typo","event":"after-new-windows"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	hint, ok := resp["error"].(map[string]any)["hint"].(map[string]any)
	if !ok {
		t.Fatal("the refusal carried no hint")
	}
	if hint["did_you_mean"] != "after-new-window" {
		t.Errorf("did_you_mean = %v, want after-new-window", hint["did_you_mean"])
	}
	available, _ := hint["available"].([]any)
	if len(available) != len(hooks.AllEvents()) {
		t.Errorf("the hint listed %d events, want all %d", len(available), len(hooks.AllEvents()))
	}
}

// TestListHooksOnADaemonWithNoHooksIsEmptyRatherThanAnError keeps the verb
// usable as the first thing a plugin author calls.
func TestListHooksOnADaemonWithNoHooksIsEmptyRatherThanAnError(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "bare")
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"list-hooks","params":{"session":"bare"}}`))
	if res["total"] != float64(0) {
		t.Errorf("total = %v, want 0", res["total"])
	}
	names, _ := res["events"].([]any)
	if len(names) == 0 {
		t.Error("a daemon with no hooks still has to say which events exist")
	}
}

// newHookTableForTest builds a one-command hook table.
func newHookTableForTest() *hooks.Manager {
	m := hooks.NewManager()
	m.Register(hooks.AfterNewWindow, "true")
	return m
}
