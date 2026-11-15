package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadMailCards runs two scripted agents, alpha and beta, that
// talk through the real CLI in a scratch daemon, and a third, gamma, that
// closes. Each agent's transcript holds its Bash calls with what the command
// really printed:
//
//   - alpha sends beta a message by its pane id, with a subject and a list,
//     as Claude Code passes a long message ("$(cat <<'EOF' ... EOF)"), then
//     sends gamma one by name, then writes a long answer.
//   - beta reads its mail and answers alpha's message with --reply-to.
//
// In the agents view, alpha's thread draws each send as a message card: "Sent
// to", the other agent by its name and harness (beta, not its pane id), the
// subject and the Markdown. The ring says beta read the message, and beta's
// reply is under it. gamma, closed after the client listed it, keeps its name
// and is marked closed. . then enter on gamma says it is closed. A click on
// beta's name selects beta, whose thread draws the read as a message from
// alpha. ctrl+o goes back to alpha's thread at the place it was scrolled to.
// The artifacts are the frames mail-cards-alpha, mail-cards-beta and
// mail-cards-back.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on a build without message cards (origin/main), the send is the plain
//     Bash card, and the test fails at "no message card".
//   - with the jump not pushed in ckPeerGo, ctrl+o does nothing, and the
//     test fails at "ctrl+o did not go back".
func TestCockpitThreadMailCards(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	writeConfig(t, base, "")
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	cli("new", "api", "--detach")
	cli("new-window", "-s", "api", "beta", "--no-focus")
	cli("new-window", "-s", "api", "gamma", "--no-focus")
	// The panes open on their own time.
	var ids map[string]string
	waitTextOf(t, "the three panes", shellTimeout, func() string {
		out, _ := tuiosCLI(t, base, "list-windows", "-s", "api", "--json")
		return out
	}, func(out string) bool {
		ids = mailWindowIDsOf(out)
		return len(ids) == 3 && ids["beta"] != "" && ids["gamma"] != ""
	})
	for name, id := range ids {
		if name != "beta" && name != "gamma" {
			ids["alpha"] = id
		}
	}
	cli("set-window", "-s", "api", "-w", ids["alpha"], "--name", "alpha")
	alpha, beta, gamma := ids["alpha"], ids["beta"], ids["gamma"]

	// The talk, through the real CLI.
	body := "rebased onto **main**, please retest:\n\n- `go test ./...`\n- the e2e shard for mail"
	sendBeta := cli("send-agent-message", "-s", "api", "-w", beta[:8], "--from", alpha, "--subject", "retest please", body)
	sendGamma := cli("send-agent-message", "-s", "api", "-w", "gamma", "--from", alpha, "gamma: please take the docs")
	read := cli("read-agent-messages", "-s", "api", "-w", beta)
	reply := cli("send-agent-message", "-s", "api", "-w", alpha, "--from", beta, "--reply-to", mailFirstID(t, sendBeta), "retested, all green")

	now := time.Now()
	dir := filepath.Join(atProjects(base), "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	quoted := "tuios send-agent-message -s api -w " + beta[:8] + " --subject 'retest please' \"$(cat <<'EOF'\n" + body + "\nEOF\n)\""
	var long []string
	for i := 1; i <= 40; i++ {
		long = append(long, fmt.Sprintf("- step %d is done", i))
	}
	alphaPath := filepath.Join(dir, "a1a1a1a1.jsonl")
	writeMailTranscript(t, alphaPath, now, "Get beta to retest, and hand gamma the docs.", []mailCall{
		{command: quoted, out: sendBeta},
		{command: "tuios send-agent-message -s api -w gamma 'gamma: please take the docs'", out: sendGamma},
	}, "Both are told.\n\n"+strings.Join(long, "\n"))
	betaPath := filepath.Join(dir, "b2b2b2b2.jsonl")
	writeMailTranscript(t, betaPath, now, "Check your mail.", []mailCall{
		{command: `tuios read-agent-messages -s api -w "$TUIOS_PANE_ID"`, out: read},
		{command: "tuios send-agent-message -s api -w alpha --reply-to 1 'retested, all green'", out: reply},
	}, "Done.")
	for _, a := range []struct{ win, path, sid, state string }{
		{"alpha", alphaPath, "a1a1a1a1", "working"}, {"beta", betaPath, "b2b2b2b2", "idle"}, {"gamma", "", "", "idle"},
	} {
		if a.path != "" {
			hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", "api", "--window", a.win)
			hook.Dir = workDirIn(t, base)
			hook.Env = append(os.Environ(), "SHELL=/bin/sh")
			for _, key := range xdgKeys {
				hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
			}
			hook.Stdin = bytes.NewReader(hookPayload(a.path, a.sid))
			if out, err := hook.CombinedOutput(); err != nil {
				t.Fatalf("agent-hook SessionStart for %s: %v\n%s", a.win, err, out)
			}
		}
		cli("set-agent-state", a.state, "-s", "api", "-w", a.win, "--harness", "claude-code")
	}

	host := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"attach", "api"}, cols: 160, rows: 50, shippedLooks: true, out: host,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	// alpha works, so it is the first row and its thread shows.
	waitText(t, term, "alpha's thread", "step 40 is done")
	_ = term.WaitStable(uiTimeout)
	// gamma closes while the view lists it.
	waitText(t, term, "gamma in the fleet", "gamma")
	cli("close-window", "-s", "api", gamma)
	scrollUntil(t, term, "K", "retest please")
	scrollToCardHead(t, term, "retest please", "Sent to")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "mail-cards-alpha", hostPictures(t, []byte(host.String()))...)

	text := term.Screen().Text()
	if !strings.Contains(text, "Sent to") {
		t.Fatalf("ASSERTION: no message card in alpha's thread\n%s", term.Snapshot())
	}
	sent := scrollToCardHead(t, term, "retest please", "Sent to")
	if line := term.Screen().Line(sent - 1); !strings.Contains(line, "Sent to") || !strings.Contains(line, "beta") || !strings.Contains(line, "claude") || strings.Contains(line, beta[:8]) {
		t.Fatalf("ASSERTION: the card does not name beta by its name and harness: %q\n%s", line, term.Snapshot())
	}
	for _, w := range []string{"go test ./...", "the e2e shard for mail", "read ", "Reply from", "retested, all green"} {
		if !strings.Contains(text, w) {
			t.Fatalf("ASSERTION: alpha's thread does not show %q\n%s", w, term.Snapshot())
		}
	}
	if strings.Contains(text, "$(cat") || strings.Contains(text, "**main**") {
		t.Fatalf("ASSERTION: the card shows the command line, not the message\n%s", term.Snapshot())
	}
	scrollUntil(t, term, "J", "please take the docs")
	if row, _ := findText(t, term, "please take the docs"); !strings.Contains(term.Screen().Line(row-1), "gamma") || !strings.Contains(term.Screen().Line(row-1), "closed") {
		t.Fatalf("ASSERTION: gamma is not marked closed: %q\n%s", term.Screen().Line(row-1), term.Snapshot())
	}

	// . stops on gamma first, the newest, and enter says it is closed.
	if err := term.SendKeys("."); err != nil {
		t.Fatal(err)
	}
	_ = term.WaitStable(uiTimeout)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the jump to a closed agent", "gamma is closed.")

	// A click on beta's name selects beta.
	sent = scrollToCardHead(t, term, "retest please", "Sent to")
	headRow := sent - 1
	col := strings.Index(term.Screen().Line(headRow), "beta")
	if col < 0 {
		t.Fatalf("ASSERTION: no beta on the card's line\n%s", term.Snapshot())
	}
	col = len([]rune(term.Screen().Line(headRow)[:col]))
	mouseClick(t, term, col+1, headRow, tuitest.MouseLeft, 0)
	waitText(t, term, "beta's thread", "Check your mail.", "Read mail")
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "mail-cards-beta", hostPictures(t, []byte(host.String()))...)
	if text := term.Screen().Text(); !strings.Contains(text, "From") || !strings.Contains(text, "alpha") || !strings.Contains(text, "the e2e shard for mail") {
		t.Fatalf("ASSERTION: beta's read is not drawn as a message from alpha\n%s", term.Snapshot())
	}

	// ctrl+o goes back to alpha, at the same place.
	if err := term.SendKeys(tuitest.Ctrl('o')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Get beta to retest") || strings.Contains(s.Text(), "retest please") && !strings.Contains(s.Text(), "Check your mail.")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: ctrl+o did not go back to alpha: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	if row, _ := findText(t, term, "retest please"); row != sent {
		t.Fatalf("ASSERTION: ctrl+o went back to alpha at row %d of the card, not %d where it was\n%s", row, sent, term.Snapshot())
	}
	saveCockpitFrames(t, term, "mail-cards-back", hostPictures(t, []byte(host.String()))...)
	alive(t, term, "after the mail cards")
}

// mailCall is one Bash call of a scripted transcript and what it printed.
type mailCall struct{ command, out string }

// writeMailTranscript writes a Claude Code transcript: a prompt, each call
// with its result, and an answer, at times just before now.
func writeMailTranscript(t *testing.T, path string, now time.Time, prompt string, calls []mailCall, answer string) {
	t.Helper()
	at := func(ago time.Duration) string { return now.Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z") }
	ago := 3 * time.Minute
	recs := []map[string]any{{"type": "user", "timestamp": at(ago), "message": map[string]any{"role": "user", "content": prompt}}}
	for i, c := range calls {
		ago -= 20 * time.Second
		id := fmt.Sprintf("toolu_m%d", i)
		recs = append(recs,
			map[string]any{"type": "assistant", "timestamp": at(ago), "message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": c.command}},
			}}},
			map[string]any{"type": "user", "timestamp": at(ago - time.Second), "message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": id, "content": c.out},
			}}})
	}
	recs = append(recs, map[string]any{"type": "assistant", "timestamp": at(ago - 10*time.Second), "message": map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "text", "text": answer},
	}}})
	var buf bytes.Buffer
	for i, r := range recs {
		r["uuid"] = fmt.Sprintf("%s-u%d", filepath.Base(path), i)
		r["isSidechain"] = false
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// mailWindowIDsOf are a session's window ids by name, from list-windows
// --json, empty when out does not decode.
func mailWindowIDsOf(out string) map[string]string {
	var payload struct {
		Windows []struct {
			ID   string `json:"window_id"`
			Name string `json:"display_name"`
		} `json:"windows"`
	}
	ids := map[string]string{}
	if json.Unmarshal([]byte(out), &payload) != nil {
		return ids
	}
	for _, w := range payload.Windows {
		ids[w.Name] = w.ID
	}
	return ids
}

// mailFirstID is the message id a send printed: "message 12 delivered to".
func mailFirstID(t *testing.T, out string) string {
	t.Helper()
	f := strings.Fields(out)
	if len(f) < 2 || f[0] != "message" {
		t.Fatalf("the send printed no message id: %q", out)
	}
	return f[1]
}

// scrollToCardHead presses K until the card whose text has want shows its
// head line, the line above want, and returns want's row.
func scrollToCardHead(t *testing.T, term *tuitest.Terminal, want, head string) int {
	t.Helper()
	for range 15 {
		s := term.Screen()
		_, rows := s.Size()
		for r := 1; r < rows; r++ {
			if strings.Contains(s.Line(r), want) && strings.Contains(s.Line(r-1), head) {
				return r
			}
		}
		if err := term.SendKeys("K"); err != nil {
			t.Fatal(err)
		}
		_ = term.WaitStable(uiTimeout)
	}
	t.Fatalf("ASSERTION: the card head %q over %q never showed while pressing K\n%s", head, want, term.Snapshot())
	return 0
}
