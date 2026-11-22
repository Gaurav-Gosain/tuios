package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mailSenderScript is an agent pane held to its own session: it drops its own
// grants to read,write,mail,task, as a sandbox wrapper does before it execs
// the agent, then tries to type into an agent of another session, lists the
// agents of every session, and mails the one in the other session. Each step
// prints its exit status, so the capture says what the daemon allowed.
const mailSenderScript = `T="$1"; S="$2"; W="$3"; OUT="$4"
"$T" set-pane-grants --grants read,write,mail,task >/dev/null; echo "GRANTS=$?"
"$T" send-text -s "$S" -w "$W" 'typed-marker-9c1' >/dev/null 2>&1; echo "SENDTEXT=$?"
"$T" capture-pane -s "$S" -w "$W" >/dev/null 2>&1; echo "CAPTURE=$?"
"$T" list-agents --select "session:$S" --json >"$OUT" 2>&1; echo "LIST=$?"
"$T" send-agent-message -s "$S" -w "$W" --subject 'subject-marker-5e2' 'body-marker-0b7 the app needs a queue'; echo "MAIL=$?"
echo SENDER-DONE
`

// TestMailGrantReachesAnotherSessionByMailOnly is the setup the grant is for:
// one session per repository, and an agent in one that is dropped to its own
// session (an OS sandbox around it) and still has to tell the agent of another
// session something. A real pane drops its own grants, then works against a
// real daemon.
//
// How this could pass wrongly, written down first:
//   - The grant could open more than mail. The sender also tries send-text
//     and capture-pane into the other session, and both must be refused.
//   - The listing could hand over what the pane may not read. The row of the
//     other session's agent must carry limited and no cwd.
//   - The mail could arrive under another name. Its from must be the
//     sender's own pane, as the daemon placed it.
//   - The wake could carry the mail into the agent's prompt, or not come at
//     all for a sender that cannot type there. The agent at rest must get
//     the one fixed line and no marker text.
//   - The sender could lose sight of its mail once it left its session. Its
//     receipts must list the mail to the other session.
//   - The grant could override a restriction the connection chose. tuios mcp
//     at its default scope (own) must still be refused the other session,
//     and with --scope all must list it cut and mail it.
//
// The artifact is mail-grant.log in the test's artifact directory: what the
// sender printed, the listing it got, what the agent was typed, the mail as
// the person reads it, the sender's receipts and both MCP sessions.
//
// Negative controls (NEGATIVE_CONTROLS.md): with the mail case cut from
// checkGrants, MAIL is non-zero. With mailWakeRefusal not consulted in
// queueOriginRefusal, the agent is never woken.
func TestMailGrantReachesAnotherSessionByMailOnly(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const app, infra = "e2e-mail-app", "e2e-mail-infra"
	for _, s := range []string{app, infra} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	agent := startMailAgent(t, base, infra, "infra-agent")
	if out, err := tuiosCLI(t, base, "new-window", "sender", "-s", app, "--no-focus"); err != nil {
		t.Fatalf("new-window sender: %v\n%s", err, out)
	}
	sender := windowID(t, base, app, "sender")

	var log strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "mail-grant.log")
		if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
			t.Errorf("save the artifact: %v", err)
		}
		t.Logf("artifact: %s", path)
	}()

	script := filepath.Join(base, "mail-sender.sh")
	if err := os.WriteFile(script, []byte(mailSenderScript), 0o755); err != nil {
		t.Fatal(err)
	}
	listing := filepath.Join(base, "listing.json")
	run := fmt.Sprintf("sh '%s' '%s' '%s' '%s' '%s'\n", script, tuiosBin, infra, agent, listing)
	if out, err := tuiosCLI(t, base, "send-text", "-s", app, "-w", sender, run); err != nil {
		t.Fatalf("start the sender: %v\n%s", err, out)
	}
	waitCapture(t, base, app, sender, "SENDER-DONE")
	sent := time.Now()
	shown := capture(t, base, app, sender)
	fmt.Fprintf(&log, "== the sender pane (%s) printed:\n%s\n", sender[:8], shown)
	for _, want := range []string{"GRANTS=0", "LIST=0", "MAIL=0"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("ASSERTION: the sender did not print %s:\n%s", want, shown)
		}
	}
	for _, refused := range []string{"SENDTEXT=0", "CAPTURE=0"} {
		if strings.Contains(shown, refused) {
			t.Fatalf("ASSERTION: the mail grant let the sender do more than mail (%s):\n%s", refused, shown)
		}
	}

	list, err := os.ReadFile(listing)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&log, "== list-agents --select session:%s from the sender:\n%s\n", infra, list)
	if !strings.Contains(string(list), agent) || !strings.Contains(string(list), `"limited": true`) || strings.Contains(string(list), `"cwd"`) {
		t.Fatalf("ASSERTION: the sender's listing of the other session is not cut to what addressing needs:\n%s", list)
	}

	// The agent at rest gets the fixed line, and nothing the sender wrote.
	waitUntil(t, "the agent in the other session is woken for its mail", time.Until(sent.Add(mailWakeWait+uiTimeout)), func() (bool, string) {
		n, out := gotLines(t, base, infra, agent)
		if strings.Contains(out, "marker") {
			fmt.Fprintf(&log, "== the agent was typed:\n%s\n", out)
			t.Fatalf("ASSERTION: text the sender chose was typed into the agent:\n%s", out)
		}
		if n == 1 && strings.Contains(out, "GOT[You have 1 unread tuios message. Read it with: tuios read-agent-messages -s "+infra+" -w "+agent[:8]+" --unread]") {
			fmt.Fprintf(&log, "== the agent was typed, %v after the mail:\n%s\n", time.Since(sent).Round(time.Millisecond), out)
			return true, out
		}
		return false, out
	})

	mail, err := tuiosCLI(t, base, "read-agent-messages", "-s", infra, "-w", agent, "--json")
	fmt.Fprintf(&log, "== the mail as the person reads it:\n%s\n", mail)
	if err != nil || !strings.Contains(mail, `"from": "`+sender+`"`) || !strings.Contains(mail, "body-marker-0b7") {
		t.Fatalf("ASSERTION: the mail is not in the agent's inbox from the sender's pane: %v\n%s", err, mail)
	}

	// The same pane, after the wake: it sees the receipt of the mail it sent
	// to the other session, and over MCP the connection's own restriction
	// still wins, so the default scope keeps it in its own session and
	// --scope all lets the mail grant through.
	sentOut := filepath.Join(base, "sent.json")
	mcpOwn := filepath.Join(base, "mcp-own.jsonl")
	mcpAll := filepath.Join(base, "mcp-all.jsonl")
	follow := filepath.Join(base, "mail-follow.sh")
	if err := os.WriteFile(follow, []byte(mailFollowScript), 0o755); err != nil {
		t.Fatal(err)
	}
	run = fmt.Sprintf("sh '%s' '%s' '%s' '%s' '%s' '%s' '%s'\n", follow, tuiosBin, infra, agent, sentOut, mcpOwn, mcpAll)
	if out, err := tuiosCLI(t, base, "send-text", "-s", app, "-w", sender, run); err != nil {
		t.Fatalf("start the follow-up: %v\n%s", err, out)
	}
	waitCapture(t, base, app, sender, "FOLLOW-DONE")
	shown = capture(t, base, app, sender)
	fmt.Fprintf(&log, "== the sender pane, after the follow-up:\n%s\n", shown)
	for _, want := range []string{"SENT=0", "MCPOWN=0", "MCPALL=0"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("ASSERTION: the follow-up did not print %s:\n%s", want, shown)
		}
	}

	receipts, err := os.ReadFile(sentOut)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&log, "== read-agent-messages --sent from the sender:\n%s\n", receipts)
	if !strings.Contains(string(receipts), `"session": "`+infra+`"`) || !strings.Contains(string(receipts), agent) {
		t.Fatalf("ASSERTION: the sender has no receipt for its mail to the other session:\n%s", receipts)
	}

	own := mcpResults(t, mcpOwn)
	fmt.Fprintf(&log, "== tuios mcp (default scope own) from the sender:\n%v\n", own)
	// Scope own narrows the selector to the pane's own sessions, so the
	// listing answers with no row of the other session rather than refusing.
	if r := own[2]; r.isErr || len(agentRows(t, r)) != 0 {
		t.Errorf("ASSERTION: under scope own the mail grant widened list_agents to the other session: %+v", r)
	}
	if r := own[3]; !r.isErr || !strings.Contains(r.text, "forbidden") {
		t.Errorf("ASSERTION: under scope own the mail grant widened send_agent_message to the other session: %+v", r)
	}
	all := mcpResults(t, mcpAll)
	fmt.Fprintf(&log, "== tuios mcp --scope all from the sender:\n%v\n", all)
	if r := all[2]; r.isErr {
		t.Errorf("ASSERTION: under scope all list_agents of the other session failed: %+v", r)
	} else if rows := agentRows(t, r); len(rows) != 1 || rows[0]["window_id"] != agent || rows[0]["limited"] != true || rows[0]["cwd"] != nil {
		t.Errorf("ASSERTION: under scope all list_agents of the other session is not the cut listing: %+v", r)
	}
	if r := all[3]; r.isErr {
		t.Errorf("ASSERTION: under scope all send_agent_message to the other session failed: %+v", r)
	}
	mail, err = tuiosCLI(t, base, "read-agent-messages", "-s", infra, "-w", agent, "--json")
	fmt.Fprintf(&log, "== the agent's inbox after the MCP send:\n%s\n", mail)
	if err != nil || !strings.Contains(mail, "mcp-marker-4d1") || strings.Count(mail, `"from": "`+sender+`"`) < 1 {
		t.Fatalf("ASSERTION: the MCP mail is not in the agent's inbox from the sender's pane: %v\n%s", err, mail)
	}
}

// mailFollowScript runs in the sender pane after the first mail was read: the
// pane's receipts, then tuios mcp twice with TUIOS_PANE_TOKEN taken out, so
// only the kernel places it, first at the default scope and then with
// --scope all. Each MCP session lists the other session's agents by selector
// and mails the agent there.
const mailFollowScript = `T="$1"; S="$2"; W="$3"; SENT="$4"; OWN="$5"; ALL="$6"
"$T" read-agent-messages --sent --json >"$SENT" 2>&1; echo "SENT=$?"
reqs() {
  printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tuios_list_agents","arguments":{"select":"session:'"$S"'"}}}' \
    '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tuios_send_agent_message","arguments":{"session":"'"$S"'","to":"'"$W"'","text":"mcp-marker-4d1 over mcp"}}}'
}
reqs | env -u TUIOS_PANE_TOKEN "$T" mcp >"$OWN"; echo "MCPOWN=$?"
reqs | env -u TUIOS_PANE_TOKEN "$T" mcp --scope all >"$ALL"; echo "MCPALL=$?"
echo FOLLOW-DONE
`

// mcpResult is one tools/call answer: the last text block and isError.
type mcpResult struct {
	text  string
	isErr bool
}

// agentRows decodes the agents of a tuios_list_agents answer.
func agentRows(t *testing.T, r mcpResult) []map[string]any {
	t.Helper()
	var list struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.Unmarshal([]byte(r.text), &list); err != nil {
		t.Fatalf("list_agents answered with no agent list: %v\n%s", err, r.text)
	}
	return list.Agents
}

// mcpResults reads a tuios mcp output file into its tools/call answers by id.
func mcpResults(t *testing.T, path string) map[float64]mcpResult {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[float64]mcpResult{}
	for line := range strings.Lines(string(raw)) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("tuios mcp wrote a line that is not JSON: %q", line)
		}
		id, _ := m["id"].(float64)
		res, _ := m["result"].(map[string]any)
		content, _ := res["content"].([]any)
		if len(content) == 0 {
			continue
		}
		text, _ := content[len(content)-1].(map[string]any)["text"].(string)
		isErr, _ := res["isError"].(bool)
		out[id] = mcpResult{text: text, isErr: isErr}
	}
	return out
}
