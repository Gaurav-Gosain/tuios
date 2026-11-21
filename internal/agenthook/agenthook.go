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

const agentHookHoldMax = 305 * time.Second

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

type agentHookOutcome struct {
	integration.Decision
	Session           string         `json:"session,omitempty"`
	Window            string         `json:"window,omitempty"`
	PaneBy            string         `json:"pane_by,omitempty"`
	HarnessPID        int            `json:"harness_pid,omitempty"`
	Unsupported       []string       `json:"unsupported,omitempty"`
	Applied           *bool          `json:"applied,omitempty"`
	State             string         `json:"state,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	ActivityRecorded  *bool          `json:"activity_recorded,omitempty"`
	TranscriptRefused string         `json:"transcript_refused,omitempty"`
	Subagents         *int           `json:"subagents,omitempty"`
	ActivityError     string         `json:"activity_error,omitempty"`
	Updates           int            `json:"updates,omitempty"`
	Error             string         `json:"error,omitempty"`
	Hold              *approvalTrace `json:"hold,omitempty"`
	StatusLineFlushed bool           `json:"status_line_flushed,omitempty"`
	MailNotice        string         `json:"mail_notice,omitempty"`
	MailError         string         `json:"mail_error,omitempty"`
	MailLease         uint64         `json:"mail_lease,omitempty"`
	MailCommitted     bool           `json:"mail_committed,omitempty"`
	NameHintOff       bool           `json:"name_hint_off,omitempty"`
}

type approvalTrace struct {
	RequestID string `json:"request_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Printed   bool   `json:"printed"`
	Error     string `json:"error,omitempty"`
}

// Run runs one hook event under the deadline. It returns nothing,
// because nothing it could return may reach the harness as a failure.
func Run(o Options, args []string, hio IO) {
	harness := args[0]
	if answer := integration.StdoutAnswer(harness); answer != "" {
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

func startHint(out agentHookOutcome) bool {
	return out.Event == "SessionStart" && integration.StartsWithContext(out.Harness) &&
		out.Error == "" && (out.Report != nil || out.Context != integration.ContextNone) &&
		out.Window != "" && out.Session != "" && out.PaneBy != "flag"
}

const mailCommitDeadline = 300 * time.Millisecond

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

func fetchMailNotice(out *agentHookOutcome, client Caller) {
	params := map[string]any{"session": out.Session, "window": out.Window, "lease": true, "harness": out.Harness}
	if out.Event == "SessionStart" {
		params["reset"] = true
	}
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

func hookSessionID(out agentHookOutcome) string {
	if out.Report != nil {
		return out.Report.SessionID
	}
	return out.ContextSessionID
}

func holdable(out agentHookOutcome) bool {
	return out.Approval != nil && out.Error == "" && out.Report != nil &&
		out.State == "needs_input" && out.Window != "" && out.Reason != "foreign_pane"
}

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
		"summary": out.Report.Message,
	}
	if len(out.Approval.Scope) > 0 {
		params["always_scope"] = out.Approval.Scope
	}
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

func agentHook(o Options, args []string, hio IO) agentHookOutcome {
	in := integration.Input{Getenv: hio.Getenv}
	rest := args[1:]
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

type hookActivityResult struct {
	State     string `json:"state"`
	Recorded  bool   `json:"recorded"`
	Reason    string `json:"reason"`
	Subagents int    `json:"subagents"`
}

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

type hookReportResult struct {
	Applied           bool   `json:"applied"`
	State             string `json:"state"`
	Reason            string `json:"reason"`
	ActivityRecorded  *bool  `json:"activity_recorded"`
	TranscriptRefused string `json:"transcript_refused"`
	Session           string `json:"session"`
	Window            string `json:"window"`
}

var hookFields = []string{"kind", "agent_session_id", "transcript_path", "transcript_leaf", "if_state", "subagent_id", "if_subagent_id", "harness_pid", "activity", "agent_cwd", "session_start", "notification"}

func setAgentStateParams(client Caller) (map[string]bool, error) {
	return verbParams(client, "set-agent-state")
}

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

var errConditionUnsupported = errors.New("the daemon predates if_subagent_id, so the subagent's report was not sent. It works once the daemon restarts")

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

type codedError interface {
	error
	ErrorCode() string
}

const codeUnknownVerb = "unknown_verb"

func shortWindowID(id string) string {
	const prefix = 8
	if len(id) <= prefix {
		return id
	}
	return id[:prefix]
}
