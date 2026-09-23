package session

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"
)

// This file answers for the socket what the reachability table answers for the
// keyboard: does the verb do anything?
//
// Every other test here calls a verb it is about and checks what that verb did.
// A verb nobody wrote a test for is dispatched by nothing and asserted by
// nothing, so its handler can be replaced with one that returns an empty result
// and the package stays green. Eight verbs were in that state.
//
// So this runs every example in the registry against a real daemon and checks
// the one thing a dead handler cannot fake: that the call produced a result
// with fields in it, and that the fields the verb's own schema names are among
// them. It does not check the values. Values belong to the tests that own them.

// exampleOutcome is what one registry example does against the fixture below.
// Success needs no entry: a verb whose example works is the normal case, and
// the assertions do the rest. Anything else is written down here, so a verb
// that starts failing, or stops failing, is a test failure and not a silence.
type exampleOutcome struct {
	// errCode is the error the example answers with in this fixture, "" when
	// the example is expected to succeed.
	errCode string
	// blocks says the example is a wait that does not answer at all here. A
	// handler that stopped waiting would answer at once, which is the fault
	// this catches.
	blocks bool
	// why says what about the fixture, not the verb, produces this.
	why string
	// slow marks an example that does real work before it can answer, and so
	// is not covered by a budget written for a verb that reads state and
	// returns. See slowBudget.
	slow bool
}

// exampleOutcomes records every example the fixture cannot make succeed. The
// fixture is a daemon with no attached client, no configured hosts and no agent
// panes, so a verb that needs one of those answers with an error, and that
// error is the honest expectation rather than a skip.
var exampleOutcomes = map[string]exampleOutcome{
	// A client-owned surface with no client attached.
	"list-dock-components#0": {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"list-dock-components#1": {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"refresh-dock#0":         {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"refresh-dock#1":         {errCode: ErrVerbNeedsClient, why: "the dock is drawn by a client"},
	"popup#0":                {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"popup#1":                {errCode: ErrVerbNeedsClient, why: "a popup is a client window"},
	"resolve-pane#0":         {errCode: ErrVerbWindowNotFound, why: "the example's pids are not the fixture's shells"},
	// The example's nonce is a placeholder, and no client is attached to
	// issue a real one. The allowed path is proved in verb_attention_test.go.
	"dismiss-attention#0": {errCode: ErrVerbNotHuman, why: "only a client attached right now may dismiss, and none is"},
	"run-command#0":       {errCode: ErrVerbNeedsClient, why: "ToggleZoom is a client command"},
	"set-layout#0":        {errCode: ErrVerbNeedsClient, why: "tiling is the client's arithmetic"},
	"split-window#0":      {errCode: ErrVerbNeedsClient, why: "a split is the client's arithmetic"},

	// The fixture has no [hosts] table, so the one host name an example can
	// use is not configured. The connection itself is proved with two daemons
	// in host_connection_test.go.
	"open-host-connection#0": {errCode: ErrVerbUnknownHost, why: "no hosts are configured in the fixture"},

	// Rendering and encoding a picture is real work, unlike every other verb
	// here, and a budget written for a state read is not a budget for it.
	"screenshot#0": {slow: true, why: "a screenshot renders and encodes a picture"},

	// resize-pane addresses a pane open-pane returned, and the example carries
	// a literal id rather than one from this run. The pair is proved end to
	// end against two daemons in remote_pane_test.go; what this run proves is
	// that the verb is reachable and says which pane it could not find.
	"resize-pane#0": {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"pane-cwd#0":    {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},
	"pane-agent#0":  {errCode: ErrVerbUnknownPane, why: "the example's pane id is a literal, not one this daemon handed out"},

	// An example that names a pane by the environment variable an agent would
	// have expanded. The literal is not a window id here.
	"ask-agent#0":           {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"read-agent-messages#0": {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"send-agent-message#0":  {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"wait-for#2":            {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},
	"wait-for#3":            {errCode: ErrVerbWindowNotFound, why: "$TUIOS_PANE_ID is unexpanded"},

	// Paths and ids the example invents for the reader.
	"new-session#1":        {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"new-window#1":         {errCode: ErrVerbInvalidParams, why: "/src/api does not exist here"},
	"send-agent-message#2": {errCode: ErrVerbInvalidParams, why: "/tmp/flame.png does not exist here"},
	"send-agent-message#3": {errCode: ErrVerbInvalidParams, why: "message 12 does not exist here"},
	"stash-put#0":          {errCode: ErrVerbInvalidParams, why: "/tmp/flame.png does not exist here"},
	"stash-get#0":          {errCode: ErrVerbInvalidParams, why: "the example path is a placeholder, not a file this session stashed"},

	// /src/api is not a repository here, and no agent is installed in the
	// fixture. The verbs are proved against throwaway repositories in
	// verb_worktree_test.go.
	"new-worktree#0":    {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"new-worktree#1":    {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},
	"remove-worktree#0": {errCode: ErrVerbSessionNotFound, why: "api-feat-retry does not exist here"},
	"remove-worktree#1": {errCode: ErrVerbSessionNotFound, why: "api-feat-retry does not exist here"},
	"fan#0":             {errCode: ErrVerbGitFailed, why: "/src/api is not a git repository here"},

	// No hosts are configured in the fixture.
	"list-host-agents#1":   {errCode: ErrVerbUnknownHost, why: "no hosts are configured here"},
	"list-host-sessions#1": {errCode: ErrVerbUnknownHost, why: "no hosts are configured here"},

	// Unsubscribing needs a subscription, and each example gets a new connection.
	"unsubscribe#0": {errCode: ErrVerbInvalidRequest, why: "the connection has no subscription"},

	// The two waits nothing satisfies. They are the positive control for the
	// verb that is supposed to block: a handler that returned early would answer.
	"wait-for#0": {blocks: true, why: "no pane prints \"done\""},
	"wait-for#1": {blocks: true, why: "no pane reports an agent state"},
	"wait-for#4": {blocks: true, why: "no pane in any session reports an agent state"},
}

// blockBudget is how long a call gets to answer. Every example that answers at
// all answers in single-digit milliseconds, so this is generous by two orders
// of magnitude and still keeps the blocking pair cheap.
const blockBudget = 750 * time.Millisecond

// slowBudget is what an example marked slow gets instead.
//
// The budget above is two orders of magnitude over what a verb that reads
// state and answers needs, which is every verb but one. Taking a screenshot
// renders and encodes a picture, so it is the one example whose honest cost is
// in the same order as the budget, and on a loaded runner it went over: twice
// in a week the build went red on a timeout that said nothing about the verb.
//
// Raising blockBudget for everyone would have been the wrong fix. It is also
// how long the blocking pair is waited on before they count as blocking, so
// every raise is paid by the two tests that are meant to time out.
const slowBudget = 10 * time.Second

// TestEveryVerbExampleReachesItsHandler runs every example in the registry
// against a real daemon.
//
// The failure it exists for is a verb that answers without doing anything. A
// handler replaced with one that returns an empty result and no error fails
// here for every verb at once: an empty result carries no field, a verb that
// documents what it returns names none of them, and a verb that is meant to
// block answers immediately.
func TestEveryVerbExampleReachesItsHandler(t *testing.T) {
	// The daemon writes screenshots and stashes under the user's directories.
	// Point all of them at the test's own temp dir so a run leaves nothing
	// behind and reads nothing the developer happens to have.
	for _, env := range []string{"HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(env, t.TempDir())
	}
	d, socketPath := startTestDaemon(t)

	names := make([]string, 0, len(verbRegistry))
	for name := range verbRegistry {
		names = append(names, name)
	}
	sort.Strings(names)

	seen := map[string]bool{}
	for _, name := range names {
		entry := verbRegistry[name]
		if len(entry.examples) == 0 {
			t.Errorf("verb %q documents no example, so nothing here can call it", name)
			continue
		}
		for i, example := range entry.examples {
			key := exampleKey(name, i)
			seen[key] = true
			t.Run(key, func(t *testing.T) {
				// A fresh session per example. The examples close windows, move
				// them between workspaces and kill the session, so one shared
				// fixture would make each example depend on the ones before it.
				freshWorkSession(t, d)

				skipIfExampleNeedsAMissingProgram(t, example)

				want := exampleOutcomes[key]
				budget := blockBudget
				if want.slow {
					budget = slowBudget
				}
				resp, err := callOnce(t, socketPath, example, budget)
				if want.blocks {
					if err == nil {
						t.Fatalf("%s answered %v, but this example has nothing to wait for: "+
							"a wait that returns at once is a wait that is not waiting", key, resp)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s did not answer within %v: %v", key, budget, err)
				}

				if e, ok := resp["error"].(map[string]any); ok && e != nil {
					code, _ := e["code"].(string)
					if want.errCode == "" {
						t.Fatalf("%s failed with %q (%v). If the fixture cannot make this "+
							"example work, add it to exampleOutcomes and say why", key, code, e["message"])
					}
					if code != want.errCode {
						t.Fatalf("%s failed with %q, expected %q (%s)", key, code, want.errCode, want.why)
					}
					return
				}
				if want.errCode != "" {
					t.Fatalf("%s succeeded, but exampleOutcomes expects %q (%s): "+
						"drop the row if the verb now works here", key, want.errCode, want.why)
				}

				res, ok := resp["result"].(map[string]any)
				if !ok || len(res) == 0 {
					// This is the line a no-op handler dies on, for every verb.
					t.Fatalf("%s answered with an empty result: the handler ran and "+
						"produced nothing (%v)", key, resp)
				}
				assertDocumentedFields(t, key, entry, res)
			})
		}
	}

	for key, want := range exampleOutcomes {
		if !seen[key] {
			t.Errorf("exampleOutcomes names %s (%s), which the registry no longer has", key, want.why)
		}
	}
}

// assertDocumentedFields checks the result against the verb's own returns
// schema. Many fields are conditional, so the rule is that a verb which
// documents what it returns must return at least one of those fields: the
// schema and the handler have to be connected, and an empty or unrelated result
// means they are not.
func assertDocumentedFields(t *testing.T, key string, entry verbEntry, res map[string]any) {
	t.Helper()
	if len(entry.returns) == 0 {
		return
	}
	for _, field := range entry.returns {
		if _, ok := res[field.Name]; ok {
			return
		}
	}
	documented := make([]string, 0, len(entry.returns))
	for _, field := range entry.returns {
		documented = append(documented, field.Name)
	}
	got := make([]string, 0, len(res))
	for field := range res {
		got = append(got, field)
	}
	sort.Strings(got)
	t.Errorf("%s returned %v and none of the fields its schema names (%v)", key, got, documented)
}

// verbsWithNoDocumentedResult is every verb whose registry entry names no
// return fields. For these the check above has nothing to compare against, so
// the empty-result rule is all that holds them up.
//
// The list is asserted in both directions. A verb that documents its result
// leaves it; a new verb that does not document one has to be added on purpose.
var verbsWithNoDocumentedResult = map[string]bool{
	"capture-pane": true, "close-window": true, "explain-agent-detect": true,
	"explain-agent-screen": true, "get-agent-state": true, "hello": true,
	"kill-session": true, "list-sessions": true, "list-verbs": true,
	"list-windows": true, "resize": true, "send-keys": true, "send-text": true,
	"session-info": true, "set-session-accent": true,
	"set-session-name": true, "set-workspace-name": true,
	"set-workspace-order": true, "unsubscribe": true,
	"wait-for": true,
}

// TestVerbResultSchemasAreDeclaredOrListed keeps the gap above from widening.
// A result schema is what lets the test check that a handler filled anything
// in, so a verb without one is a verb the check cannot hold to much.
func TestVerbResultSchemasAreDeclaredOrListed(t *testing.T) {
	for name, entry := range verbRegistry {
		if len(entry.returns) == 0 && !verbsWithNoDocumentedResult[name] {
			t.Errorf("verb %q names no return fields: document them in its registry "+
				"entry, or add it to verbsWithNoDocumentedResult", name)
		}
		if len(entry.returns) > 0 && verbsWithNoDocumentedResult[name] {
			t.Errorf("verb %q documents its result now: drop it from verbsWithNoDocumentedResult", name)
		}
	}
	for name := range verbsWithNoDocumentedResult {
		if _, ok := verbRegistry[name]; !ok {
			t.Errorf("verbsWithNoDocumentedResult names %q, which the registry does not have", name)
		}
	}
}

func exampleKey(verb string, i int) string {
	return verb + "#" + strconv.Itoa(i)
}

// freshWorkSession rebuilds the session the examples name, with the two windows
// they name in it.
func freshWorkSession(t *testing.T, d *Daemon) {
	t.Helper()
	if d.manager.GetSession("work") != nil {
		_ = d.manager.DeleteSession("work")
	}
	sess, err := d.manager.CreateSession("work", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("create the session the examples name: %v", err)
	}
	for _, name := range []string{"build", "review"} {
		if _, err := sess.AddDaemonWindow(name, nil); err != nil {
			t.Fatalf("add window %q: %v", name, err)
		}
	}
}

// callOnce sends one request on its own connection and reads one response. A
// connection per call because hello, subscribe and unsubscribe are about the
// connection, and sharing one would make each example depend on the last.
func callOnce(t *testing.T, socketPath, line string, budget time.Duration) (map[string]any, error) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(budget))
	raw, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("the daemon answered something that is not JSON: %q (%v)", raw, err)
	}
	return resp, nil
}

// skipIfExampleNeedsAMissingProgram skips an example that launches a program by
// absolute path this machine does not have.
//
// The examples are documentation, so they name a path a reader recognises
// (/usr/bin/htop). The fixture really runs them, which turns "htop is not
// installed here" into a failure that reads like a broken handler. A missing
// program is a fact about the machine, and the handler is reached by every
// other example of the same verb. exampleOutcomes is the wrong home for this:
// a row there says the call always fails, and on a box that has the program it
// does not.
func skipIfExampleNeedsAMissingProgram(t *testing.T, example string) {
	t.Helper()
	var call struct {
		Params struct {
			Command []string `json:"command"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(example), &call); err != nil || len(call.Params.Command) == 0 {
		return
	}
	prog := call.Params.Command[0]
	if !filepath.IsAbs(prog) {
		return
	}
	if _, err := os.Stat(prog); err != nil {
		t.Skipf("the example runs %s, which this machine does not have", prog)
	}
}
