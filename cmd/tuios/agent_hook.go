package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// agentHookDeadline bounds a whole hook run: reading the payload, finding the
// pane and reporting. Harnesses run some hooks synchronously, PreToolUse and
// PermissionRequest among them, so a hook that waits on a stuck or restarting
// daemon slows every tool call. Past the deadline the hook gives up and exits
// 0 having printed nothing, which a harness reads as "no opinion".
const agentHookDeadline = 500 * time.Millisecond

// agentHookMaxPayload caps what is read from stdin. A payload is a few
// kilobytes; the cap only guards against a harness piping something else.
const agentHookMaxPayload = 4 << 20

// agentHookHoldMax bounds the wait for an answer from the Inbox. The daemon
// ends every hold within 300 seconds; this only guards against a daemon that
// stops answering, and stays under the 310 second limit the Claude Code
// integration gives the hook, so the hook still exits on its own.
const agentHookHoldMax = 305 * time.Second

// timedCaller is a verbCaller whose call can wait longer than the default.
type timedCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
}

// verbCaller is the one method the hook needs from a verb client, so a test
// can stand in for the daemon.
type verbCaller interface {
	Call(verb string, params any) (json.RawMessage, error)
}

// agentHookOptions are the flags of tuios agent-hook.
type agentHookOptions struct {
	session string
	window  string
	explain bool
	timeout time.Duration
	// integration is the version marker a managed hook entry carries. It is
	// accepted so the command runs, and otherwise ignored.
	integration int
}

// agentHookIO is everything a hook run touches outside itself.
type agentHookIO struct {
	stdin    io.Reader
	stdinTTY bool
	stdout   io.Writer
	stderr   io.Writer
	getenv   func(string) string
	dial     func() (verbCaller, error)
	self     func() (sid int, ancestors []int)
	// harnessPID names the harness process among the ancestors self reports.
	harnessPID func(ancestors []int) int
	// holdMax bounds the wait for an answer from the Inbox. Zero means
	// agentHookHoldMax.
	holdMax time.Duration
	// stampDir is where agent-statusline keeps each pane's last values. A
	// turn end sends what it held back (flushStatusLine). Nil skips that.
	stampDir func() (string, error)
	now      func() time.Time
}

func newAgentHookCommand() *cobra.Command {
	var o agentHookOptions
	cmd := &cobra.Command{
		Use:   "agent-hook <harness> [event] [payload]",
		Short: "Report a harness hook event as the pane's agent state",
		Long: `Report a coding-agent harness's hook event to the tuios pane it runs in.

This is what the hooks tuios integration install writes run. The harness
hands its hook payload on stdin (or, for the Codex notify command, as the last
argument). The event comes from the payload, or from the event argument when
the payload does not name it. Supported harnesses: claude-code, codex,
gemini-cli, opencode, amp, kilo, kimi and pi, which report the pane's state;
and antigravity, copilot, crush, cursor-agent, devin, droid, grok, hermes,
qoder and qwen, which report only the conversation id (set-agent-session) and
leave the state to the pane's screen rules.

The pane is found from --window, then TUIOS_PANE_ID, then the process's
controlling terminal, then its parent processes, so a harness or sandbox
that scrubs the environment is still reported for. A payload that does not
parse, an event with no mapping, a subagent's event, and an event from a
harness other than the one TUIOS_AGENT names are all reported as nothing,
never as done.

It asks the daemon which set-agent-state fields it supports and sends only
those. A conditional report (if_state) is not sent to a daemon older than the
condition, since that daemon would apply it unconditionally.

For claude-code and codex, a prompt, a tool call, its result and the end of a
turn also carry the event itself as activity (and gemini-cli's BeforeTool its
tool name), which the daemon keeps in the pane's activity ring for
'tuios agent-log' and the rail's "now" line. Only the first line of any text is
sent, with likely secrets masked. A Stop's done report says the first line of
what the agent said last.

A report that ends a turn (done or errored) also sends what the pane's status
line feed (tuios agent-statusline) held back, with set-agent-meta for the same
pane, so the turn's last context and cost reach the rail.

It always exits 0, prints nothing a harness would read as an answer (Gemini
CLI gets an empty JSON object), and gives up after 500ms when the daemon is
slow or gone. Use --explain to see on stderr what it decided and why.

The one exception is a permission prompt the Inbox may answer. When
[agents.approvals] in the config names the harness (claude-code, opencode or
kilo), the hook for Claude Code's PermissionRequest, or for opencode's
permission.asked, waits after its report for the person to answer the Inbox
item, for up to hold_seconds (120 by default). A Claude Code plan
(ExitPlanMode) is held the same way unless hold_plans is false. It then prints
the harness's own decision. It prints nothing, and the harness asks in its pane as before,
when the wait ends without an answer, when approvals are off, when the daemon
is gone or restarts, and on any error. It never prints an approval it was not
given.`,
		Example: `  # What a Claude Code hook runs
  tuios agent-hook claude-code --integration 1

  # Try a payload by hand
  echo '{"hook_event_name":"Stop","session_id":"s1"}' | tuios agent-hook claude --explain`,
		Args: cobra.RangeArgs(1, 3),
		RunE: func(_ *cobra.Command, args []string) error {
			runAgentHook(o, args, agentHookIO{
				stdin:    os.Stdin,
				stdinTTY: term.IsTerminal(int(os.Stdin.Fd())),
				stdout:   os.Stdout,
				stderr:   os.Stderr,
				getenv:   os.Getenv,
				dial: func() (verbCaller, error) {
					return session.DialVerbClientAs(version)
				},
				self:       integration.SelfProcess,
				harnessPID: integration.HarnessPID,
				stampDir:   statusLineStampDir,
				now:        time.Now,
			})
			return nil
		},
	}
	cmd.Flags().StringVarP(&o.session, "session", "s", "", "Session of the pane to report for (default: TUIOS_SESSION)")
	cmd.Flags().StringVarP(&o.window, "window", "w", "", "Pane to report for, by window id or name (default: TUIOS_PANE_ID, then the controlling terminal, then the parent processes)")
	cmd.Flags().BoolVar(&o.explain, "explain", false, "Print what was decided and why to stderr")
	cmd.Flags().DurationVar(&o.timeout, "timeout", agentHookDeadline, "Give up after this long")
	cmd.Flags().IntVar(&o.integration, "integration", 0, "Version marker of a managed hook entry; ignored")
	_ = cmd.Flags().MarkHidden("integration")
	return cmd
}

// agentHookOutcome is what --explain prints: the decision, the pane it went
// to and how that pane was found, and what the daemon said.
type agentHookOutcome struct {
	integration.Decision
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	PaneBy  string `json:"pane_by,omitempty"`
	// HarnessPID is the harness process the hook ran under, 0 when unknown.
	HarnessPID int `json:"harness_pid,omitempty"`
	// Unsupported lists the report's fields the daemon does not know, which
	// were left out of the call.
	Unsupported []string `json:"unsupported,omitempty"`
	Applied     *bool    `json:"applied,omitempty"`
	State       string   `json:"state,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	// ActivityRecorded says whether the daemon kept the event's activity,
	// nil when none was sent.
	ActivityRecorded *bool  `json:"activity_recorded,omitempty"`
	Error            string `json:"error,omitempty"`
	// Hold is what happened to a prompt the Inbox could answer, when the
	// hook asked for one.
	Hold *approvalTrace `json:"hold,omitempty"`
	// StatusLineFlushed says the turn end sent values the pane's status line
	// feed had held back.
	StatusLineFlushed bool `json:"status_line_flushed,omitempty"`
}

// approvalTrace is the Inbox half of a hook run, for --explain.
type approvalTrace struct {
	RequestID string `json:"request_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Printed says the hook printed the harness's decision.
	Printed bool   `json:"printed"`
	Error   string `json:"error,omitempty"`
}

// runAgentHook runs one hook event under the deadline. It returns nothing,
// because nothing it could return may reach the harness as a failure.
func runAgentHook(o agentHookOptions, args []string, hio agentHookIO) {
	harness := args[0]
	if answer := integration.StdoutAnswer(harness); answer != "" {
		// Gemini CLI and Antigravity CLI parse a hook's stdout as JSON. An
		// empty object is the answer that changes nothing, and it goes out
		// first so a deadline cannot leave the hook with no answer at all.
		_, _ = io.WriteString(hio.stdout, answer)
	}
	timeout := o.timeout
	if timeout <= 0 {
		timeout = agentHookDeadline
	}
	done := make(chan agentHookOutcome, 1)
	go func() { done <- agentHook(o, args, hio) }()
	var out agentHookOutcome
	select {
	case out = <-done:
	case <-time.After(timeout):
		out = agentHookOutcome{Decision: integration.Decision{Harness: harness}, Error: "gave up after " + timeout.String()}
	}
	if holdable(out) {
		out.Hold = holdForAnswer(out, hio)
	}
	if o.explain {
		line, _ := json.Marshal(out)
		fmt.Fprintln(hio.stderr, string(line))
	}
}

// holdable reports whether a hook run may wait for an answer from the Inbox:
// the event is a prompt the harness takes a decision for, and the pane is now
// on needs_input by this hook's own report. A report that failed or was
// refused, a nested harness's for one, holds nothing.
func holdable(out agentHookOutcome) bool {
	return out.Approval != nil && out.Error == "" && out.Report != nil &&
		out.State == "needs_input" && out.Window != ""
}

// holdForAnswer asks the daemon to hold the pane's prompt for the person and
// prints the harness's decision when one comes back. Whatever goes wrong, it
// prints nothing: a harness that gets no answer asks in its own pane, so
// silence is always safe and an approval can only come from the person.
func holdForAnswer(out agentHookOutcome, hio agentHookIO) *approvalTrace {
	limit := hio.holdMax
	if limit <= 0 {
		limit = agentHookHoldMax
	}
	type answer struct {
		trace    approvalTrace
		decision string
		message  string
	}
	done := make(chan answer, 1)
	go func() {
		var a answer
		a.decision, a.message, a.trace = requestAnswer(out, hio, limit)
		done <- a
	}()
	var a answer
	select {
	case a = <-done:
	case <-time.After(limit):
		// The call may still come back; nothing reads it, so nothing it says
		// is printed.
		return &approvalTrace{Error: "gave up after " + limit.String()}
	}
	if a.decision == "" {
		return &a.trace
	}
	text, ok := out.Approval.Answer(out.Harness, a.decision, a.message)
	if !ok {
		a.trace.Error = "the harness was not offered " + a.decision + ", so nothing was printed"
		return &a.trace
	}
	if _, err := io.WriteString(hio.stdout, text); err != nil {
		a.trace.Error = "writing the decision: " + err.Error()
		return &a.trace
	}
	a.trace.Printed = true
	return &a.trace
}

// requestAnswer is the request-approval call. It returns the decision, empty
// for none, and what happened.
func requestAnswer(out agentHookOutcome, hio agentHookIO, limit time.Duration) (string, string, approvalTrace) {
	var trace approvalTrace
	client, err := hio.dial()
	if err != nil {
		trace.Error = err.Error()
		return "", "", trace
	}
	if c, ok := client.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}
	params := map[string]any{
		"session": out.Session,
		"window":  out.Window,
		"harness": out.Harness,
		"options": out.Approval.Options,
		// The line the person answers from. The daemon shows it on the
		// held item and refuses a hold whose line it would have to cut.
		"summary": out.Report.Message,
	}
	if len(out.Approval.Scope) > 0 {
		params["always_scope"] = out.Approval.Scope
	}
	// The fields the risk rules, plans and deny reasons need go only to a
	// daemon whose request-approval lists them. An older daemon would ignore
	// them, which for a plan would hold it as a plain approval the person
	// could allow without reading, so a plan is not sent to one at all.
	extra := map[string]any{}
	if a := out.Approval; a.IsPlan() {
		extra["kind"], extra["plan"] = integration.KindPlan, a.Plan
	} else {
		if a.Tool != "" {
			extra["tool"] = a.Tool
		}
		if a.Target != "" {
			extra["target"] = a.Target
		}
	}
	if out.Approval.DenyMessage {
		extra["deny_message"] = true
	}
	if len(extra) > 0 {
		known, err := verbParams(client, "request-approval")
		if err != nil {
			trace.Error = "could not ask the daemon what request-approval takes: " + err.Error()
			return "", "", trace
		}
		if out.Approval.IsPlan() && !known["plan"] {
			trace.Error = "the running daemon predates plans in the Inbox, so the plan was left to the pane"
			return "", "", trace
		}
		for k, v := range extra {
			if known[k] {
				params[k] = v
			}
		}
	}
	var raw json.RawMessage
	if tc, ok := client.(timedCaller); ok {
		raw, err = tc.CallWithTimeout("request-approval", params, limit)
	} else {
		raw, err = client.Call("request-approval", params)
	}
	if err != nil {
		// unknown_verb from a daemon older than approvals lands here too.
		trace.Error = err.Error()
		return "", "", trace
	}
	var res struct {
		RequestID string `json:"request_id"`
		Decision  string `json:"decision"`
		Message   string `json:"message"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		trace.Error = "reading the answer: " + err.Error()
		return "", "", trace
	}
	trace.RequestID, trace.Decision, trace.Reason = res.RequestID, res.Decision, res.Reason
	return res.Decision, res.Message, trace
}

// agentHook reads, decides, resolves and reports.
func agentHook(o agentHookOptions, args []string, hio agentHookIO) agentHookOutcome {
	in := integration.Input{Getenv: hio.getenv}
	rest := args[1:]
	// Codex's notify command appends its JSON payload as the last argument.
	if n := len(rest); n > 0 && strings.HasPrefix(strings.TrimSpace(rest[n-1]), "{") {
		in.Payload = []byte(rest[n-1])
		rest = rest[:n-1]
	}
	if len(rest) > 0 {
		in.Event = rest[0]
	}
	if in.Payload == nil && hio.stdin != nil && !hio.stdinTTY {
		data, err := io.ReadAll(io.LimitReader(hio.stdin, agentHookMaxPayload))
		if err != nil {
			return agentHookOutcome{Decision: integration.Decision{Harness: args[0], Event: in.Event}, Error: "reading stdin: " + err.Error()}
		}
		in.Payload = data
	}
	out := agentHookOutcome{Decision: integration.Translate(args[0], in)}
	if out.Report == nil {
		return out
	}

	client, err := hio.dial()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if c, ok := client.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}
	var sid int
	var ancestors []int
	if hio.self != nil {
		sid, ancestors = hio.self()
	}
	if hio.harnessPID != nil {
		out.HarnessPID = hio.harnessPID(ancestors)
	}
	out.Session, out.Window, out.PaneBy, err = resolveHookPane(o, hio, client, sid, ancestors)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if out.Report.SessionOnly {
		res, err := reportHookSession(client, out.Session, out.Window, out.Harness, out.HarnessPID, out.Report.SessionID)
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Applied, out.Reason = &res.Applied, res.Reason
		return out
	}
	res, dropped, err := reportHook(client, out.Session, out.Window, out.Harness, out.HarnessPID, *out.Report)
	out.Unsupported = dropped
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Applied, out.State, out.Reason = &res.Applied, res.State, res.Reason
	out.ActivityRecorded = res.ActivityRecorded
	out.StatusLineFlushed = flushAtTurnEnd(out, res, client, hio)
	return out
}

// flushAtTurnEnd sends what the pane's status line feed held back when this
// report ends a turn, since the status line does not run again until the
// conversation changes. A report the identity guard refused is a nested
// run's, and its turn is not the pane's.
func flushAtTurnEnd(out agentHookOutcome, res hookReportResult, client verbCaller, hio agentHookIO) bool {
	if hio.stampDir == nil || out.Report == nil {
		return false
	}
	if st := out.Report.State; st != "done" && st != "errored" {
		return false
	}
	if res.Reason == "foreign_session" || res.Reason == "foreign_harness" {
		return false
	}
	dir, err := hio.stampDir()
	if err != nil {
		return false
	}
	now := time.Now
	if hio.now != nil {
		now = hio.now
	}
	sent, _ := flushStatusLine(client, out.Session, out.Window, dir, now())
	return sent
}

// resolveHookPane finds the pane to report for: the --window flag, then
// TUIOS_PANE_ID, then the daemon's resolve-pane on the process's terminal
// session and its ancestors.
func resolveHookPane(o agentHookOptions, hio agentHookIO, client verbCaller, sid int, ancestors []int) (sess, window, by string, err error) {
	if o.window != "" {
		return firstNonEmptyString(o.session, hio.getenv("TUIOS_SESSION")), o.window, "flag", nil
	}
	if id := hio.getenv("TUIOS_PANE_ID"); id != "" {
		return firstNonEmptyString(o.session, hio.getenv("TUIOS_SESSION")), id, "env", nil
	}
	if sid <= 1 && len(ancestors) == 0 {
		return "", "", "", errors.New("no pane: TUIOS_PANE_ID is unset")
	}
	raw, err := client.Call("resolve-pane", map[string]any{"sid": sid, "pids": ancestors})
	if err != nil {
		return "", "", "", fmt.Errorf("no pane: TUIOS_PANE_ID is unset and %w", err)
	}
	var res struct {
		Session  string `json:"session"`
		WindowID string `json:"window_id"`
		By       string `json:"by"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", "", "", err
	}
	return res.Session, res.WindowID, res.By, nil
}

// hookReportResult is the part of set-agent-state's answer the hook reads.
type hookReportResult struct {
	Applied          bool   `json:"applied"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	ActivityRecorded *bool  `json:"activity_recorded"`
}

// hookFields are the set-agent-state params a hook report may carry beyond
// the ones every daemon takes. activity is the hook event for the pane's
// activity ring; a daemon from before it drops it, and the report goes
// without it.
var hookFields = []string{"kind", "agent_session_id", "transcript_path", "if_state", "harness_pid", "activity"}

// setAgentStateParams asks the daemon which params its set-agent-state takes.
//
// A daemon decodes params leniently and ignores a name it does not know, so a
// daemon older than the hook fields would not refuse them: it would take the
// report and drop the fields without a word. For most of them that only loses
// information, but dropping if_state turns a conditional report into an
// unconditional one, and Claude Code's idle_prompt would then overwrite done
// about a minute after every turn. So the hook asks first. The daemon keeps
// running across a tuios upgrade, which makes a new hook talking to an older
// daemon the ordinary case right after one.
func setAgentStateParams(client verbCaller) (map[string]bool, error) {
	return verbParams(client, "set-agent-state")
}

// verbParams asks the daemon which params its verb takes, by name.
func verbParams(client verbCaller, verb string) (map[string]bool, error) {
	raw, err := client.Call("list-verbs", map[string]any{"verb": verb})
	if err != nil {
		return nil, err
	}
	var res struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name string `json:"name"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, v := range res.Verbs {
		if v.Verb != verb {
			continue
		}
		for _, p := range v.Params {
			known[p.Name] = true
		}
	}
	return known, nil
}

// requireIfState returns an error unless the daemon's set-agent-state takes
// if_state. tuios set-agent-state --if-state calls it before sending.
func requireIfState(client verbCaller) error {
	known, err := setAgentStateParams(client)
	if err != nil {
		return fmt.Errorf("could not ask the daemon whether it supports --if-state: %w", err)
	}
	if !known["if_state"] {
		return errors.New("the running daemon predates --if-state, so the report was not sent. It works once the daemon restarts")
	}
	return nil
}

// reportHook sends one report. The hook fields go only to a daemon whose
// set-agent-state lists them, and the ones it does not are returned as
// dropped. A report with if_state is not sent at all to a daemon without it,
// since sent without its condition it could turn a finished pane back to
// working.
func reportHook(client verbCaller, sess, window, harness string, harnessPID int, r integration.Report) (hookReportResult, []string, error) {
	params := map[string]any{
		"session": sess,
		"window":  window,
		"state":   r.State,
		"harness": harness,
	}
	if r.Message != "" {
		params["message"] = r.Message
	}
	extra := map[string]any{}
	for k, v := range map[string]string{
		"kind":             r.Kind,
		"agent_session_id": r.SessionID,
		"transcript_path":  r.TranscriptPath,
		"if_state":         r.IfState,
	} {
		if v != "" {
			extra[k] = v
		}
	}
	if harnessPID > 1 && r.SessionID != "" {
		extra["harness_pid"] = harnessPID
	}
	if r.Activity != nil {
		extra["activity"] = r.Activity
	}
	var dropped []string
	if len(extra) > 0 {
		known, err := setAgentStateParams(client)
		if err != nil {
			return hookReportResult{}, nil, fmt.Errorf("could not ask the daemon what set-agent-state takes: %w", err)
		}
		for _, k := range hookFields {
			if _, ok := extra[k]; !ok {
				continue
			}
			if !known[k] {
				dropped = append(dropped, k)
				continue
			}
			params[k] = extra[k]
		}
		if r.IfState != "" && !known["if_state"] {
			return hookReportResult{}, dropped, errors.New("the daemon predates if_state, so a conditional report was not sent")
		}
	}
	raw, err := client.Call("set-agent-state", params)
	if err != nil {
		return hookReportResult{}, dropped, err
	}
	var res hookReportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return hookReportResult{}, dropped, err
	}
	return res, dropped, nil
}

// reportHookSession sends an identity-only report with set-agent-session. A
// daemon older than the verb would answer unknown_verb, which costs nothing,
// but the hook asks list-verbs first anyway so --explain can say why nothing
// was stored.
func reportHookSession(client verbCaller, sess, window, harness string, harnessPID int, sessionID string) (hookReportResult, error) {
	raw, err := client.Call("list-verbs", map[string]any{"verb": "set-agent-session"})
	if err != nil {
		return hookReportResult{}, fmt.Errorf("could not ask the daemon whether it has set-agent-session: %w", err)
	}
	var verbs struct {
		Verbs []struct {
			Verb string `json:"verb"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(raw, &verbs); err != nil {
		return hookReportResult{}, err
	}
	known := false
	for _, v := range verbs.Verbs {
		known = known || v.Verb == "set-agent-session"
	}
	if !known {
		return hookReportResult{}, errors.New("the running daemon predates set-agent-session, so the session was not reported. It works once the daemon restarts")
	}
	params := map[string]any{
		"session":          sess,
		"window":           window,
		"harness":          harness,
		"agent_session_id": sessionID,
	}
	if harnessPID > 1 {
		params["harness_pid"] = harnessPID
	}
	raw, err = client.Call("set-agent-session", params)
	if err != nil {
		return hookReportResult{}, err
	}
	var res hookReportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return hookReportResult{}, err
	}
	return res, nil
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
