// Package agenthook runs one harness hook event: it reads the payload,
// decides with internal/integration what the event reports, finds the pane,
// and reports it to the daemon through a verb caller, then prints what the
// harness reads back. tuios agent-hook runs it in its own process.
package agenthook

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Deadline bounds a whole hook run: reading the payload, finding the
// pane and reporting. Harnesses run some hooks synchronously, PreToolUse and
// PermissionRequest among them, so a hook that waits on a stuck or restarting
// daemon slows every tool call. Past the deadline the hook gives up and exits
// 0 having printed nothing, which a harness reads as "no opinion".
const Deadline = 500 * time.Millisecond

// MaxPayload caps what is read from stdin. A payload is a few
// kilobytes; the cap only guards against a harness piping something else.
const MaxPayload = 4 << 20

// agentHookHoldMax bounds the wait for an answer from the Inbox. The daemon
// ends every hold within 300 seconds; this only guards against a daemon that
// stops answering, and stays under the 310 second limit the Claude Code
// integration gives the hook, so the hook still exits on its own.
const agentHookHoldMax = 305 * time.Second

// timedCaller is a Caller whose call can wait longer than the default.
type timedCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
}

// Caller is the one method the hook needs from a verb client, so a test
// can stand in for the daemon.
type Caller interface {
	Call(verb string, params any) (json.RawMessage, error)
}

// Options are the flags of tuios agent-hook.
type Options struct {
	Session string
	Window  string
	Explain bool
	Timeout time.Duration
	// Integration is the version marker a managed hook entry carries. It is
	// accepted so the command runs, and otherwise ignored.
	Integration int
}

// IO is everything a hook run touches outside itself.
type IO struct {
	Stdin    io.Reader
	StdinTTY bool
	Stdout   io.Writer
	Stderr   io.Writer
	Getenv   func(string) string
	Dial     func() (Caller, error)
	Self     func() (sid int, ancestors []int)
	// harnessPID names the harness process among the ancestors self reports.
	HarnessPID func(ancestors []int) int
	// holdMax bounds the wait for an answer from the Inbox. Zero means
	// agentHookHoldMax.
	HoldMax time.Duration
	// stampDir is where agent-statusline keeps each pane's last values. A
	// turn end sends what it held back (FlushStatusLine). Nil skips that.
	StampDir func() (string, error)
	Now      func() time.Time
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
	ActivityRecorded *bool `json:"activity_recorded,omitempty"`
	// TranscriptRefused is why the daemon did not join the pane to the
	// transcript the hook named.
	TranscriptRefused string `json:"transcript_refused,omitempty"`
	// Subagents is how many subagents the pane holds after a
	// report-agent-activity call, nil when none was made.
	Subagents *int `json:"subagents,omitempty"`
	// ActivityError is why the report-agent-activity call that followed a
	// state report failed. The state report stands.
	ActivityError string `json:"activity_error,omitempty"`
	// Updates is how many of the report's subagent updates the daemon
	// answered.
	Updates int    `json:"updates,omitempty"`
	Error   string `json:"error,omitempty"`
	// Hold is what happened to a prompt the Inbox could answer, when the
	// hook asked for one.
	Hold *approvalTrace `json:"hold,omitempty"`
	// StatusLineFlushed says the turn end sent values the pane's status line
	// feed had held back.
	StatusLineFlushed bool `json:"status_line_flushed,omitempty"`
	// MailNotice is the line about the pane's unread mail the hook adds to
	// the agent's context, empty for none. MailError is why the hook could
	// not ask for one.
	MailNotice string `json:"mail_notice,omitempty"`
	MailError  string `json:"mail_error,omitempty"`
	// MailLease is the token that makes the notice announced for good once
	// it is written. MailCommitted says that was done.
	MailLease     uint64 `json:"mail_lease,omitempty"`
	MailCommitted bool   `json:"mail_committed,omitempty"`
	// NameHintOff says [agents] name_hint is off, so the start hint leaves
	// out the line that names the pane. Unread, or any failure to read it,
	// means on.
	NameHintOff bool `json:"name_hint_off,omitempty"`
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

// Run runs one hook event under the deadline. It returns nothing,
// because nothing it could return may reach the harness as a failure.
func Run(o Options, args []string, hio IO) {
	harness := args[0]
	if answer := integration.StdoutAnswer(harness); answer != "" {
		// Gemini CLI and Antigravity CLI parse a hook's stdout as JSON. An
		// empty object is the answer that changes nothing, and it goes out
		// first so a deadline cannot leave the hook with no answer at all.
		_, _ = io.WriteString(hio.Stdout, answer)
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = Deadline
	}
	done := make(chan agentHookOutcome, 1)
	go func() { done <- agentHook(o, args, hio) }()
	var out agentHookOutcome
	select {
	case out = <-done:
	case <-time.After(timeout):
		out = agentHookOutcome{Decision: integration.Decision{Harness: harness}, Error: "gave up after " + timeout.String()}
	}
	if writeContext(out, hio.Stdout) && out.MailLease != 0 {
		out.MailCommitted = commitMailNotice(out, hio)
	}
	if holdable(out) {
		out.Hold = holdForAnswer(out, hio)
	}
	if o.Explain {
		line, _ := jsonx.Marshal(out)
		fmt.Fprintln(hio.Stderr, string(line))
	}
}

// startHint reports whether a hook run tells the agent where it runs: a
// SessionStart for a harness that reads the hook's output as context, reported
// for a pane the hook found. Outside tuios, or when the report failed, it
// prints nothing. A pane named with --window is a run by hand or by a script,
// and the name it was given is not the pane's id, so that prints nothing too.
func startHint(out agentHookOutcome) bool {
	return out.Event == "SessionStart" && integration.StartsWithContext(out.Harness) &&
		out.Error == "" && (out.Report != nil || out.Context != integration.ContextNone) &&
		out.Window != "" && out.Session != "" && out.PaneBy != "flag"
}

// mailCommitDeadline bounds the call that makes a written notice announced
// for good. Past it the lease runs out, and the next hook announces the mail
// again: a repeat, never a notice lost.
const mailCommitDeadline = 300 * time.Millisecond

// commitMailNotice tells the daemon the notice was written.
func commitMailNotice(out agentHookOutcome, hio IO) bool {
	done := make(chan bool, 1)
	go func() {
		client, err := hio.Dial()
		if err != nil {
			done <- false
			return
		}
		if c, ok := client.(io.Closer); ok {
			defer func() { _ = c.Close() }()
		}
		raw, err := client.Call("agent-mail-notice", map[string]any{"session": out.Session, "window": out.Window, "commit": out.MailLease})
		var res struct {
			Committed bool `json:"committed"`
		}
		done <- err == nil && jsonx.Unmarshal(raw, &res) == nil && res.Committed
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(mailCommitDeadline):
		return false
	}
}

// writeContext prints what the hook adds to the agent's context: where it
// runs, on a SessionStart, and the line about its unread mail, in the form
// the harness reads for the event. It reports whether it wrote the notice.
func writeContext(out agentHookOutcome, w io.Writer) bool {
	var text string
	if startHint(out) {
		text = integration.SessionStartHint(shortWindowID(out.Window), out.Session, !out.NameHintOff)
	}
	if out.MailNotice != "" {
		text += out.MailNotice + "\n"
	}
	if text == "" {
		return false
	}
	if out.Context == integration.ContextJSON {
		text = integration.ContextJSONOutput(out.Event, strings.TrimSuffix(text, "\n"))
	}
	_, err := io.WriteString(w, text)
	return err == nil && out.MailNotice != ""
}

// mayNotice reports whether a hook run asks for the notice about the pane's
// unread mail: the harness reads this event's output as context, and the
// hook found the pane by itself, and the daemon did not refuse the report as
// another agent's. A pane named with --window is a run by hand, which must
// not use up the notice the pane's agent would get.
func mayNotice(out agentHookOutcome) bool {
	if out.Context == integration.ContextNone || out.Error != "" || out.Window == "" || out.Session == "" || out.PaneBy == "flag" {
		return false
	}
	switch out.Reason {
	case "foreign_session", "foreign_harness", "foreign_pane":
		return false
	}
	return true
}

// fetchMailNotice asks the daemon for the line about the pane's unread mail.
// A conversation that starts, resumes or compacts asks with reset, since the
// agent no longer holds an earlier notice. A daemon from before the verb
// answers unknown_verb, and the hook prints nothing.
func fetchMailNotice(out *agentHookOutcome, client Caller) {
	params := map[string]any{"session": out.Session, "window": out.Window, "lease": true, "harness": out.Harness}
	if out.Event == "SessionStart" {
		params["reset"] = true
	}
	// The same guard a report from this hook meets: the compact
	// SessionStart sends no report, so this is its only check.
	if id := hookSessionID(*out); id != "" {
		params["agent_session_id"] = id
		if out.HarnessPID > 1 {
			params["harness_pid"] = out.HarnessPID
		}
	}
	raw, err := client.Call("agent-mail-notice", params)
	if err != nil {
		out.MailError = err.Error()
		return
	}
	var res struct {
		Notice string `json:"notice"`
		Lease  uint64 `json:"lease"`
		Reason string `json:"reason"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		out.MailError = err.Error()
		return
	}
	if res.Reason != "" {
		out.MailError = "no notice: " + res.Reason
		return
	}
	out.MailNotice, out.MailLease = res.Notice, res.Lease
}

// hookSessionID is the conversation id the hook event names, which a skip
// such as the compact SessionStart keeps only in its context decision.
func hookSessionID(out agentHookOutcome) string {
	if out.Report != nil {
		return out.Report.SessionID
	}
	return out.ContextSessionID
}

// holdable reports whether a hook run may wait for an answer from the Inbox:
// the event is a prompt the harness takes a decision for, and the pane is now
// on needs_input by this hook's own report. A report that failed or was
// refused, a nested harness's for one, holds nothing. A harness outside the
// pane holds nothing even when the pane's own agent is on needs_input: the
// prompt on the pane is not its prompt.
func holdable(out agentHookOutcome) bool {
	return out.Approval != nil && out.Error == "" && out.Report != nil &&
		out.State == "needs_input" && out.Window != "" && out.Reason != "foreign_pane"
}

// holdForAnswer asks the daemon to hold the pane's prompt for the person and
// prints the harness's decision when one comes back. Whatever goes wrong, it
// prints nothing: a harness that gets no answer asks in its own pane, so
// silence is always safe and an approval can only come from the person.
func holdForAnswer(out agentHookOutcome, hio IO) *approvalTrace {
	limit := hio.HoldMax
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
	if _, err := io.WriteString(hio.Stdout, text); err != nil {
		a.trace.Error = "writing the decision: " + err.Error()
		return &a.trace
	}
	a.trace.Printed = true
	return &a.trace
}

// requestAnswer is the request-approval call. It returns the decision, empty
// for none, and what happened.
func requestAnswer(out agentHookOutcome, hio IO, limit time.Duration) (string, string, approvalTrace) {
	var trace approvalTrace
	client, err := hio.Dial()
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
		if a.Cwd != "" {
			extra["cwd"] = a.Cwd
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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		trace.Error = "reading the answer: " + err.Error()
		return "", "", trace
	}
	trace.RequestID, trace.Decision, trace.Reason = res.RequestID, res.Decision, res.Reason
	return res.Decision, res.Message, trace
}

// agentHook reads, decides, resolves and reports.
func agentHook(o Options, args []string, hio IO) agentHookOutcome {
	in := integration.Input{Getenv: hio.Getenv}
	rest := args[1:]
	// Codex's notify command appends its JSON payload as the last argument.
	if n := len(rest); n > 0 && strings.HasPrefix(strings.TrimSpace(rest[n-1]), "{") {
		in.Payload = []byte(rest[n-1])
		rest = rest[:n-1]
	}
	if len(rest) > 0 {
		in.Event = rest[0]
	}
	if in.Payload == nil && hio.Stdin != nil && !hio.StdinTTY {
		data, err := io.ReadAll(io.LimitReader(hio.Stdin, MaxPayload))
		if err != nil {
			return agentHookOutcome{Decision: integration.Decision{Harness: args[0], Event: in.Event}, Error: "reading stdin: " + err.Error()}
		}
		in.Payload = data
	}
	out := agentHookOutcome{Decision: integration.Translate(args[0], in)}
	if out.Report == nil && out.Context == integration.ContextNone {
		return out
	}

	client, err := hio.Dial()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if c, ok := client.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}
	var sid int
	var ancestors []int
	if hio.Self != nil {
		sid, ancestors = hio.Self()
	}
	if hio.HarnessPID != nil {
		out.HarnessPID = hio.HarnessPID(ancestors)
	}
	out.Session, out.Window, out.PaneBy, err = ResolvePane(o, hio, client, sid, ancestors)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if out.Report != nil {
		out = reportHookEvent(out, client, hio)
	}
	if mayNotice(out) {
		fetchMailNotice(&out, client)
	}
	if startHint(out) {
		out.NameHintOff = nameHintOff(out, client)
	}
	return out
}

// nameHintOff asks the daemon for [agents] name_hint, which holds the
// session's override and config.toml. Any failure reads as on, the default: a
// daemon from before the option answers unknown_option, and a hook must not
// fail for want of a line.
func nameHintOff(out agentHookOutcome, client Caller) bool {
	raw, err := client.Call("get-option", map[string]any{"session": out.Session, "key": "agents.name_hint"})
	if err != nil {
		return false
	}
	var res struct {
		Value string `json:"value"`
	}
	return jsonx.Unmarshal(raw, &res) == nil && config.NormalizeNameHint(res.Value) == config.NameHintOff
}

// reportHookEvent sends what one hook event reports, once the pane is found.
func reportHookEvent(out agentHookOutcome, client Caller, hio IO) agentHookOutcome {
	if out.Report.SessionOnly {
		res, dropped, err := reportHookSession(client, out.Session, out.Window, out.Harness, out.HarnessPID, *out.Report)
		out.Unsupported = dropped
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Applied, out.Reason = &res.Applied, res.Reason
		out.TranscriptRefused = res.TranscriptRefused
		return out
	}
	// An event set-agent-state does not take goes with report-agent-activity:
	// on its own for a report with no state, and after the state report for
	// one with a state, so a new conversation has taken the pane over by
	// the time its session_start is checked against it.
	r := *out.Report
	var alone *integration.Activity
	if r.Activity != nil && !integration.StateActivity(r.Activity.Event) {
		alone, r.Activity = r.Activity, nil
	}
	if r.State != "" {
		res, dropped, err := reportHook(client, out.Session, out.Window, out.Harness, out.HarnessPID, r)
		out.Unsupported = dropped
		switch {
		case errors.Is(err, errConditionUnsupported):
			out.Error = err.Error()
		case err != nil:
			out.Error = err.Error()
			return out
		default:
			out.Applied, out.State, out.Reason = &res.Applied, res.State, res.Reason
			if res.Window != "" {
				// The rest of the event, and the start hint, are about the
				// pane the report reached.
				out.Session, out.Window = cmp.Or(res.Session, out.Session), res.Window
			}
			out.ActivityRecorded = res.ActivityRecorded
			out.TranscriptRefused = res.TranscriptRefused
			out.StatusLineFlushed = flushAtTurnEnd(out, res, client, hio)
		}
	}
	if alone != nil {
		res, err := reportHookActivity(client, out.Session, out.Window, out.Harness, out.HarnessPID, r.SessionID, *alone)
		switch {
		case err != nil && r.State == "":
			out.Error = err.Error()
			return out
		case err != nil:
			out.ActivityError = err.Error()
		default:
			out.ActivityRecorded, out.Subagents = &res.Recorded, &res.Subagents
			if r.State == "" {
				out.State, out.Reason = res.State, res.Reason
			}
		}
	}
	// The subagent updates the same event carries, in order. A daemon from
	// before subagent_update refuses each with invalid_params, which changes
	// nothing; the first refusal ends the run, since the rest would be
	// refused alike.
	for _, a := range r.Extra {
		res, err := reportHookActivity(client, out.Session, out.Window, out.Harness, out.HarnessPID, r.SessionID, a)
		if err != nil {
			out.ActivityError = err.Error()
			break
		}
		out.Updates++
		out.Subagents = &res.Subagents
	}
	return out
}

// hookActivityResult is the part of report-agent-activity's answer the hook
// reads.
type hookActivityResult struct {
	State     string `json:"state"`
	Recorded  bool   `json:"recorded"`
	Reason    string `json:"reason"`
	Subagents int    `json:"subagents"`
}

// reportHookActivity sends one hook event with report-agent-activity. A
// daemon from before the verb answers unknown_verb, which changes nothing, so
// the hook does not ask list-verbs first; the error says why nothing was
// recorded.
func reportHookActivity(client Caller, sess, window, harness string, harnessPID int, sessionID string, a integration.Activity) (hookActivityResult, error) {
	params := map[string]any{
		"session":  sess,
		"window":   window,
		"harness":  harness,
		"activity": a,
	}
	if sessionID != "" {
		params["agent_session_id"] = sessionID
		if harnessPID > 1 {
			params["harness_pid"] = harnessPID
		}
	}
	raw, err := client.Call("report-agent-activity", params)
	if verr, ok := errors.AsType[codedError](err); ok && verr.ErrorCode() == codeUnknownVerb {
		return hookActivityResult{}, errors.New("the running daemon predates report-agent-activity, so the " + a.Event + " was not reported. It works once the daemon restarts")
	}
	if err != nil {
		return hookActivityResult{}, err
	}
	var res hookActivityResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return hookActivityResult{}, err
	}
	return res, nil
}

// flushAtTurnEnd sends what the pane's status line feed held back when this
// report ends a turn, since the status line does not run again until the
// conversation changes. A report the identity guard refused is a nested
// run's, or a harness's that runs outside the pane, and its turn is not the
// pane's.
func flushAtTurnEnd(out agentHookOutcome, res hookReportResult, client Caller, hio IO) bool {
	if hio.StampDir == nil || out.Report == nil {
		return false
	}
	if st := out.Report.State; st != "done" && st != "errored" {
		return false
	}
	if res.Reason == "foreign_session" || res.Reason == "foreign_harness" || res.Reason == "foreign_pane" {
		return false
	}
	dir, err := hio.StampDir()
	if err != nil {
		return false
	}
	now := time.Now
	if hio.Now != nil {
		now = hio.Now
	}
	sent, _ := FlushStatusLine(client, out.Session, out.Window, dir, now())
	return sent
}

// ResolvePane finds the pane to report for: the --window flag, then
// TUIOS_PANE_ID, then the daemon's resolve-pane on the process's terminal
// session and its ancestors.
func ResolvePane(o Options, hio IO, client Caller, sid int, ancestors []int) (sess, window, by string, err error) {
	if o.Window != "" {
		return cmp.Or(o.Session, hio.Getenv("TUIOS_SESSION")), o.Window, "flag", nil
	}
	if id := hio.Getenv("TUIOS_PANE_ID"); id != "" {
		return cmp.Or(o.Session, hio.Getenv("TUIOS_SESSION")), id, "env", nil
	}
	if sid <= 1 && len(ancestors) == 0 {
		return "", "", "", errors.New("no pane: TUIOS_PANE_ID is unset")
	}
	raw, err := client.Call("resolve-pane", map[string]any{"sid": sid, "pids": ancestors})
	if err != nil {
		return "", "", "", errfmt.Errorf("no pane: TUIOS_PANE_ID is unset and %w", err)
	}
	var res struct {
		Session  string `json:"session"`
		WindowID string `json:"window_id"`
		By       string `json:"by"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
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
	// TranscriptRefused is why the daemon did not join the pane to the
	// transcript_path the report named, "" when it did or none was named.
	TranscriptRefused string `json:"transcript_refused"`
	// Session and Window name the pane the daemon placed the report on, when
	// the pane the hook named is gone: a shared server's hook, whose
	// TUIOS_PANE_ID is the pane that started the server. Empty otherwise.
	Session string `json:"session"`
	Window  string `json:"window"`
}

// hookFields are the set-agent-state params a hook report may carry beyond
// the ones every daemon takes. activity is the hook event for the pane's
// activity ring; a daemon from before it drops it, and the report goes
// without it.
var hookFields = []string{"kind", "agent_session_id", "transcript_path", "transcript_leaf", "if_state", "subagent_id", "if_subagent_id", "harness_pid", "activity", "agent_cwd", "session_start", "notification"}

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
func setAgentStateParams(client Caller) (map[string]bool, error) {
	return verbParams(client, "set-agent-state")
}

// verbParams asks the daemon which params its verb takes, by name.
func verbParams(client Caller, verb string) (map[string]bool, error) {
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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
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

// RequireIfState returns an error unless the daemon's set-agent-state takes
// if_state. tuios set-agent-state --if-state calls it before sending.
func RequireIfState(client Caller) error {
	known, err := setAgentStateParams(client)
	if err != nil {
		return errfmt.Errorf("could not ask the daemon whether it supports --if-state: %w", err)
	}
	if !known["if_state"] {
		return errors.New("the running daemon predates --if-state, so the report was not sent. It works once the daemon restarts")
	}
	return nil
}

// errConditionUnsupported is reportHook's error for a report whose condition
// the daemon predates, which is not sent. What else the event reports still
// goes.
var errConditionUnsupported = errors.New("the daemon predates if_subagent_id, so the subagent's report was not sent. It works once the daemon restarts")

// reportHook sends one report. The hook fields go only to a daemon whose
// set-agent-state lists them, and the ones it does not are returned as
// dropped. A report with if_state is not sent at all to a daemon without it,
// since sent without its condition it could turn a finished pane back to
// working.
func reportHook(client Caller, sess, window, harness string, harnessPID int, r integration.Report) (hookReportResult, []string, error) {
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
		"subagent_id":      r.SubagentID,
		"if_subagent_id":   r.IfSubagentID,
		"agent_cwd":        r.Cwd,
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
	if r.SessionStart && r.SessionID != "" {
		extra["session_start"] = true
	}
	if r.Notification {
		extra["notification"] = true
	}
	if r.TranscriptLeaf != nil {
		extra["transcript_leaf"] = *r.TranscriptLeaf
	}
	var dropped []string
	if len(extra) > 0 {
		known, err := setAgentStateParams(client)
		if err != nil {
			return hookReportResult{}, nil, errfmt.Errorf("could not ask the daemon what set-agent-state takes: %w", err)
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
		if r.IfSubagentID != "" && !known["if_subagent_id"] {
			// Sent without it, a subagent's report would clear the main
			// agent's prompt as well.
			return hookReportResult{}, dropped, errConditionUnsupported
		}
	}
	raw, err := client.Call("set-agent-state", params)
	if err != nil {
		return hookReportResult{}, dropped, err
	}
	var res hookReportResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return hookReportResult{}, dropped, err
	}
	return res, dropped, nil
}

// reportHookSession sends an identity-only report with set-agent-session. A
// daemon older than the verb would answer unknown_verb, which costs nothing,
// but the hook asks list-verbs first anyway so --explain can say why nothing
// was stored.
func reportHookSession(client Caller, sess, window, harness string, harnessPID int, r integration.Report) (hookReportResult, []string, error) {
	raw, err := client.Call("list-verbs", map[string]any{"verb": "set-agent-session"})
	if err != nil {
		return hookReportResult{}, nil, errfmt.Errorf("could not ask the daemon whether it has set-agent-session: %w", err)
	}
	var verbs struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name string `json:"name"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if err := jsonx.Unmarshal(raw, &verbs); err != nil {
		return hookReportResult{}, nil, err
	}
	known := false
	fields := map[string]bool{}
	for _, v := range verbs.Verbs {
		known = known || v.Verb == "set-agent-session"
		if v.Verb == "set-agent-session" {
			for _, p := range v.Params {
				fields[p.Name] = true
			}
		}
	}
	if !known {
		return hookReportResult{}, nil, errors.New("the running daemon predates set-agent-session, so the session was not reported. It works once the daemon restarts")
	}
	params := map[string]any{
		"session":          sess,
		"window":           window,
		"harness":          harness,
		"agent_session_id": r.SessionID,
	}
	if harnessPID > 1 {
		params["harness_pid"] = harnessPID
	}
	var dropped []string
	for _, key := range []string{"transcript_path", "transcript_leaf"} {
		var value any
		if key == "transcript_path" && r.TranscriptPath != "" {
			value = r.TranscriptPath
		}
		if key == "transcript_leaf" && r.TranscriptLeaf != nil {
			value = *r.TranscriptLeaf
		}
		if value == nil {
			continue
		}
		if fields[key] {
			params[key] = value
		} else {
			dropped = append(dropped, key)
		}
	}
	raw, err = client.Call("set-agent-session", params)
	if err != nil {
		return hookReportResult{}, dropped, err
	}
	var res hookReportResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return hookReportResult{}, dropped, err
	}
	return res, dropped, nil
}

// codedError is a verb error that carries the daemon's stable error code,
// such as session.VerbCallError.
type codedError interface {
	error
	ErrorCode() string
}

// codeUnknownVerb is the daemon's error code for a verb it does not have
// (session.ErrVerbUnknownVerb). Error codes are part of the protocol and do
// not change.
const codeUnknownVerb = "unknown_verb"

// shortWindowID is the first eight characters of a window id, the form the
// hint names a pane by.
func shortWindowID(id string) string {
	const prefix = 8
	if len(id) <= prefix {
		return id
	}
	return id[:prefix]
}
