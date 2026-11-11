package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/agenthook"
	"github.com/Gaurav-Gosain/tuios/internal/envvars"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// newSetAgentQuietCommand creates the command that marks a window quiet.
func newSetAgentQuietCommand() *cobra.Command {
	var sessionName, window string
	var off, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set-agent-quiet",
		Short: "Keep a pane's finished turns and questions out of the Inbox",
		Long: `Mark a window quiet, for an agent that works in the background.

A quiet window opens no finished, question or errored item in the Inbox, and it
raises no alert. Its open items of those kinds close. The agent state, the rail
glyph, the agent-state events and list-agents do not change. An approval or a
login still reaches you. The rail row says quiet, and list-agents reports
quiet: true.

The mark stays until you clear it, the agent leaves the pane or the pane closes.
A pane that tuios restores after a restart is not quiet. --off clears it. The old items do not open again,
but a question the pane waits on now opens at once.

This command needs an agent in the pane and the admin grant. A pane that runs it
leaves an open question open, so mark workers before they ask.`,
		Example: `  # Mark a background worker quiet
  tuios set-agent-quiet -w worker-1

  # Clear the mark
  tuios set-agent-quiet -w worker-1 --off`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			client, err := dialVerb()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			raw, err := client.Call("set-agent-quiet", map[string]any{
				"session": sessionName,
				"window":  window,
				"quiet":   !off,
			})
			if err != nil {
				return reportVerbError(explainVerbError("set-agent-quiet", err), jsonOutput)
			}
			if jsonOutput {
				return printVerbResult(raw, jsonOutput)
			}
			var res struct {
				WindowID string `json:"window_id"`
				Quiet    bool   `json:"quiet"`
				Closed   int    `json:"closed"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				return errfmt.Errorf("failed to parse response: %w", err)
			}
			if !res.Quiet {
				fmt.Printf("Window %s is not quiet.\n", shortWindowID(res.WindowID))
				return nil
			}
			fmt.Printf("Window %s is quiet. Inbox items closed: %d. An approval still reaches you.\n", shortWindowID(res.WindowID), res.Closed)
			return nil
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "Window to mark by name or ID (default: focused)")
	cmd.Flags().BoolVar(&off, "off", false, "Clear the mark")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// runSetAgentState reports a pane's agent state to the daemon over the verb
// protocol. It is what the reference shim calls, and what a user runs to mark a
// pane by hand.
//
// A report the daemon declines because a higher-ranked source already owns the
// window comes back as applied:false, not as an error. Saying so matters: the
// caller otherwise believes it set a state that never took.
func runSetAgentState(sessionName, windowTarget, state, message, source, harness string, extra setAgentStateExtras) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Run in a pane with no --window, the report is about that pane, not the
	// focused one. A daemon that places the caller does the same. This covers
	// one that does not.
	if windowTarget == "" && (sessionName == "" || sessionName == os.Getenv(envvars.Session)) {
		if pane := os.Getenv(envvars.PaneID); pane != "" && os.Getenv(envvars.Session) != "" {
			windowTarget, sessionName = pane, os.Getenv(envvars.Session)
		}
	}
	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"state":   state,
		"message": message,
		"source":  source,
		"harness": harness,
	}
	// Sent only when set, so a call that uses none of them works against a
	// daemon that predates them. Such a daemon ignores a param it does not
	// know instead of refusing it, so --if-state is checked first: applied
	// without its condition, the report would do what the caller ruled out.
	if extra.ifState != "" {
		if err := agenthook.RequireIfState(client); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{
		"kind":             extra.kind,
		"agent_session_id": extra.sessionID,
		"transcript_path":  extra.transcriptPath,
		"if_state":         extra.ifState,
	} {
		if v != "" {
			params[k] = v
		}
	}
	raw, err := client.Call("set-agent-state", params)
	if err != nil {
		return explainVerbError("set-agent-state", err)
	}

	var res struct {
		Applied bool   `json:"applied"`
		State   string `json:"state"`
		Source  string `json:"source"`
		Reason  string `json:"reason"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Applied {
		fmt.Fprintf(os.Stderr, "Not applied: %s It still reports %s.\n", agentRefusalText(res.Reason), res.State)
	}
	return nil
}

// runSetAgentSession stores a pane's conversation id without touching its
// state. A refused report is said on stderr, as set-agent-state does.
func runSetAgentSession(sessionName, windowTarget, harness, sessionID string) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("set-agent-session", map[string]any{
		"session":          sessionName,
		"window":           windowTarget,
		"harness":          harness,
		"agent_session_id": sessionID,
	})
	if err != nil {
		return explainVerbError("set-agent-session", err)
	}
	var res struct {
		Applied bool   `json:"applied"`
		ID      string `json:"agent_session_id"`
		Reason  string `json:"reason"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Applied {
		fmt.Fprintf(os.Stderr, "Not applied: %s The pane keeps %q.\n", agentRefusalText(res.Reason), res.ID)
	}
	return nil
}

// agentRefusalText says in words why a report was not applied.
func agentRefusalText(reason string) string {
	switch reason {
	case "if_state":
		return "the pane was not in any of the --if-state states."
	case "foreign_session":
		return "the pane's agent is mid-turn in another conversation, so this looks like a nested run."
	case "foreign_harness":
		return "another harness reported this pane mid-turn, so this looks like a nested run."
	case "foreign_pane":
		return "the harness that ran the hook is a background session outside this pane. It inherited the pane's TUIOS_PANE_ID."
	default:
		// outranked, or a daemon that predates reasons.
		return "a higher-ranked source owns this pane."
	}
}

// agentMetaTokensJSON turns key=value arguments into the tokens object of a
// set-agent-meta call, in argument order, which the rail keeps. "key=" removes
// the key.
func agentMetaTokensJSON(args []string) (json.RawMessage, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok || key == "" {
			return nil, errfmt.Errorf("%q is not key=value. Write key= to remove a key", arg)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := jsonx.Marshal(key)
		b.Write(k)
		b.WriteByte(':')
		if value == "" {
			b.WriteString("null")
			continue
		}
		v, _ := jsonx.Marshal(value)
		b.Write(v)
	}
	b.WriteByte('}')
	return json.RawMessage(b.String()), nil
}

// runSetAgentMeta records display metadata about a pane's agent. It prints
// nothing on success, like set-agent-state, except the keys whose values were
// cut to the length limit, on stderr.
func runSetAgentMeta(sessionName, windowTarget string, args []string, source string, ttl time.Duration, clearFirst, jsonOutput bool) error {
	tokens, err := agentMetaTokensJSON(args)
	if err != nil {
		return err
	}
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"source":  source,
		"ttl_ms":  ttl.Milliseconds(),
		"clear":   clearFirst,
	}
	if len(args) > 0 {
		params["tokens"] = tokens
	}
	raw, err := client.Call("set-agent-meta", params)
	if err != nil {
		return explainVerbError("set-agent-meta", err)
	}
	if jsonOutput {
		var pretty any
		if err := jsonx.Unmarshal(raw, &pretty); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		out, err := json.MarshalIndent(pretty, "", "  ")
		if err != nil {
			return errfmt.Errorf("failed to render response: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	var res struct {
		Truncated []string `json:"truncated"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Truncated) > 0 {
		fmt.Fprintf(os.Stderr, "Cut to %d characters: %s\n", session.AgentMetaMaxValue, strings.Join(res.Truncated, ", "))
	}
	return nil
}

// runWaitFor blocks until a daemon-side condition matches, so a caller can stop
// polling a pane and sleeping between captures.
//
// The read deadline is stretched past the requested timeout because the daemon
// only answers once the wait resolves: a client deadline shorter than the wait
// would report a connection failure for a wait that was still perfectly healthy.
func runWaitFor(sessionName, windowTarget, condition, pattern, until string, idle int, thread uint64, timeout int, anySession bool, selector string, every, jsonOutput bool, commandSeq *uint64, fresh bool) error {
	if fresh && condition != "window-output" {
		return errors.New("--new goes with window-output")
	}
	if anySession && (sessionName != "" || windowTarget != "") {
		return errors.New("--any-session watches every session, so it takes no --session or --window. Drop one or the other")
	}
	if selector != "" {
		if sessionName != "" || windowTarget != "" || anySession {
			return errors.New("--select watches the panes it matches in every session, so it takes no --session, --window or --any-session. Put a session: term in the selector instead")
		}
		return runWaitForSelect(condition, until, selector, every, timeout, jsonOutput)
	}
	if every {
		return errors.New("--every goes with --select")
	}
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return err
	}
	defer t.Close()

	params := map[string]any{
		"session":   sessionName,
		"window":    windowTarget,
		"condition": condition,
		"pattern":   pattern,
		"timeout":   timeout,
	}
	if until != "" {
		params["until"] = until
	}
	if idle > 0 {
		params["idle"] = idle
	}
	if thread > 0 {
		params["thread"] = thread
	}
	if anySession {
		params["any_session"] = true
	}
	if commandSeq != nil {
		params["command_seq"] = *commandSeq
	}
	// Sent only when asked for, so a wait against an older daemon keeps
	// working: it would refuse a parameter it does not know.
	if fresh {
		params["new"] = true
	}

	grace := time.Duration(timeout)*time.Millisecond + 10*time.Second
	raw, err := t.client.CallWithTimeout("wait-for", t.params(params), grace)
	if err != nil {
		if call, ok := errors.AsType[*session.VerbCallError](err); ok && fresh && call.Code == session.ErrVerbInvalidParams &&
			call.Hint != nil && call.Hint.Param == "new" && strings.Contains(call.Message, "has no parameter") {
			return reportVerbError(errStaleWaitNew, jsonOutput)
		}
		return reportVerbError(t.explain("wait-for", err), jsonOutput)
	}
	// A daemon older than v0.8.0 ignores unknown parameters, so it matched
	// old output too. It omits the field that says new was used.
	if fresh {
		var echo struct {
			New bool `json:"new"`
		}
		if jsonx.Unmarshal(raw, &echo) == nil && !echo.New {
			fmt.Fprintln(os.Stderr, "warning: "+errStaleWaitNew.Error()+". The match can be old output.")
		}
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}

	var res struct {
		Condition string `json:"condition"`
		Window    string `json:"window"`
		Session   string `json:"session"`
		ExitCode  *int   `json:"exit_code"`
		Signal    int    `json:"signal"`
		Cmdline   string `json:"cmdline"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if res.Condition == "command-finished" {
		status := "no exit status"
		if res.ExitCode != nil {
			status = fmt.Sprintf("exit %d", *res.ExitCode)
		}
		fmt.Printf("%s matched on %s%s: %s, %s\n", res.Condition, plainLine(res.Window), t.on(), status, plainLine(res.Cmdline))
		return nil
	}
	if res.Condition == "window-exit" && res.ExitCode != nil {
		status := fmt.Sprintf("exit %d", *res.ExitCode)
		if res.Signal > 0 {
			status = fmt.Sprintf("signal %d", res.Signal)
		}
		fmt.Printf("%s matched on %s%s: %s\n", res.Condition, plainLine(res.Window), t.on(), status)
		return nil
	}
	if res.Window != "" && anySession && res.Session != "" {
		fmt.Printf("%s matched on %s in session %s%s\n", res.Condition, plainLine(res.Window), plainLine(res.Session), t.on())
		return nil
	}
	if res.Window != "" {
		fmt.Printf("%s matched on %s%s\n", res.Condition, plainLine(res.Window), t.on())
		return nil
	}
	fmt.Printf("%s matched%s\n", res.Condition, t.on())
	return nil
}

// runWaitForSelect is wait-for agent-state over the panes a selector matches,
// on this machine. The call names no session, whatever TUIOS_SESSION says: a
// selector reaches every session.
func runWaitForSelect(condition, until, selector string, every bool, timeout int, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"condition": condition, "until": until, "select": selector, "timeout": timeout}
	if every {
		params["every"] = true
	}
	grace := time.Duration(timeout)*time.Millisecond + 10*time.Second
	raw, err := client.CallWithTimeout("wait-for", params, grace)
	if err != nil {
		return reportVerbError(explainVerbError("wait-for", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Window  string `json:"window"`
		Session string `json:"session"`
		State   string `json:"state"`
		Panes   []struct {
			Session string `json:"session"`
			Window  string `json:"window"`
			State   string `json:"state"`
		} `json:"panes"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if every {
		fmt.Printf("all %d panes matching %q reached %s\n", len(res.Panes), selector, until)
		for _, p := range res.Panes {
			fmt.Printf("  %s in session %s: %s\n", shortWindowID(p.Window), p.Session, p.State)
		}
		return nil
	}
	fmt.Printf("agent-state matched on %s in session %s: %s\n", shortWindowID(res.Window), res.Session, res.State)
	return nil
}

// runGetAgentState reads a pane's reported agent state and prints it. With
// jsonOutput it prints the full result; otherwise it prints the state name.
func runGetAgentState(sessionName, windowTarget string, jsonOutput bool) error {
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call("get-agent-state", t.params(map[string]any{
		"session": sessionName,
		"window":  windowTarget,
	}))
	if err != nil {
		return reportVerbError(t.explain("get-agent-state", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	var res struct {
		State string `json:"state"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(res.State)
	return nil
}

// runExplainAgentDetect prints what the detector saw in a pane and what every
// manifest made of it.
func runExplainAgentDetect(sessionName, windowTarget string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("explain-agent-detect", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
	})
	if err != nil {
		return reportVerbError(explainVerbError("explain-agent-detect", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res detectExplanation
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	printDetectExplanation(os.Stdout, res)
	return nil
}

// printDetectExplanation writes the human form. It leads with the verdict and
// the evidence, because that is the answer; what the detector read and what
// each manifest did with it follow, for the person writing a rule.
func printDetectExplanation(w io.Writer, res detectExplanation) {
	fmt.Fprintln(w, res.Verdict)
	for _, line := range res.Evidence {
		fmt.Fprintf(w, "  %s\n", line)
	}
	if len(res.Ignored) > 0 {
		fmt.Fprintln(w, "\nwords tuios saw and did not count:")
		for _, line := range res.Ignored {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}

	harnessName := res.HarnessID
	if harnessName == "" {
		harnessName = "none"
	}
	state := res.State
	if res.Source != "" {
		state += " (" + res.Source + ")"
	}
	fmt.Fprintf(w, "\npane %s  state %s  needs you: %s\n", res.WindowID, state, yesNo(res.NeedsYou))
	fmt.Fprintf(w, "harness %s  confidence %s", harnessName, orNone(res.Confidence))
	if res.Identity != "" {
		fmt.Fprintf(w, " (%s)", res.Identity)
	}
	if res.AutoDetected {
		fmt.Fprint(w, "  held by auto-detection")
	}
	fmt.Fprintln(w)

	if !res.Running {
		fmt.Fprintf(w, "\n%s\n", res.Reason)
		return
	}

	p := res.Process
	fmt.Fprintln(w, "\nwhat the detector read:")
	fmt.Fprintf(w, "  comm   %s\n", orNone(p.Comm))
	fmt.Fprintf(w, "  argv   %s\n", orNone(strings.Join(p.Argv, " ")))
	fmt.Fprintf(w, "  exe    %s\n", orNone(p.Exe))
	fmt.Fprintf(w, "  argv0  %s\n", orNone(p.Argv0))
	switch {
	case p.RunToken != "":
		fmt.Fprintf(w, "  run    %s  (an interpreter, so this one token may name an agent)\n", p.RunToken)
	case p.Interpreter:
		fmt.Fprintln(w, "  run    (none: an interpreter, but it was given nothing to run)")
	default:
		fmt.Fprintln(w, "  run    (none: not an interpreter, so no argument is read as identity)")
	}

	if len(res.Group) > 0 {
		fmt.Fprintln(w, "\nprocesses behind the wrapper:")
		for _, m := range res.Group {
			mark := " "
			if m.Matched {
				mark = "*"
			}
			fmt.Fprintf(w, "  %s %*spid %d  %s  %s\n", mark, 2*(m.Depth-1), "", m.PID, orNone(m.Comm), strings.Join(m.Argv, " "))
		}
	}

	fmt.Fprintln(w, "\nmanifests, in lookup order:")
	for _, m := range res.Manifests {
		if m.Matched {
			fmt.Fprintf(w, "  * %-14s matched on %s\n", m.ID, m.Rule)
			continue
		}
		fmt.Fprintf(w, "    %-14s %s\n", m.ID, m.Reason)
	}

	fmt.Fprintln(w)
	switch {
	case res.Matched && res.MatchedHarness != "":
		fmt.Fprintf(w, "result: %s, on %s\n", res.MatchedHarness, res.MatchedRule)
	case res.Matched:
		fmt.Fprintf(w, "result: an agent, on %s\n", res.MatchedRule)
		if res.Note != "" {
			fmt.Fprintf(w, "        %s\n", res.Note)
		}
	default:
		fmt.Fprintln(w, "result: not an agent")
		if res.NameList != "" {
			fmt.Fprintf(w, "        the name list alone would have matched: %s\n", res.NameList)
		}
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// runExplainAgentScreen prints what a harness's screen rules make of a pane.
func runExplainAgentScreen(sessionName, windowTarget, harnessID string, lines int, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("explain-agent-screen", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"harness": harnessID,
		"lines":   lines,
	})
	if err != nil {
		return reportVerbError(explainVerbError("explain-agent-screen", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res screenExplanation
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	printScreenExplanation(os.Stdout, res)
	return nil
}

// printScreenExplanation writes the human form: what the pane is, what the
// classifier read, and what each rule did with it.
func printScreenExplanation(w io.Writer, res screenExplanation) {
	harnessName := res.HarnessID
	if harnessName == "" {
		harnessName = "none"
	}
	fmt.Fprintf(w, "pane %s  state %s (%s)\nharness %s", res.WindowID, res.State, res.Source, harnessName)
	if res.HarnessID != "" {
		if res.Enabled {
			fmt.Fprintf(w, "  screen rules on, reading %d lines", res.Lines)
		} else {
			fmt.Fprint(w, "  screen rules off in the manifest")
		}
	}
	fmt.Fprintln(w)
	if res.ManifestSource != "" && res.ManifestSource != "bundled" {
		if res.ReplacesBundled {
			fmt.Fprintf(w, "manifest %s, which replaces the bundled one\n", res.ManifestSource)
		} else {
			fmt.Fprintf(w, "manifest %s\n", res.ManifestSource)
		}
	}

	fmt.Fprintln(w, "\ntail, as the classifier sees it:")
	if len(res.Tail) == 0 {
		fmt.Fprintln(w, "  (nothing: the pane's visible screen is empty)")
	}
	for i, line := range res.Tail {
		fmt.Fprintf(w, "  %2d | %s\n", i+1, line)
	}

	if res.HarnessID == "" {
		fmt.Fprintln(w, "\nno harness is attributed to this pane, so no rules ran.")
		fmt.Fprintln(w, "pass --harness to try one's rules against the tail above.")
		return
	}
	if len(res.Rules) == 0 {
		fmt.Fprintf(w, "\n%s declares no screen rules.\n", res.HarnessID)
		return
	}
	fmt.Fprintln(w, "\nrules:")
	printRuleReports(w, res.Rules, res.Rule, "tail")
	// The leading mark is only readable next to what it means.
	fmt.Fprintln(w, "\n  > the rule that decided, * matched but outranked")
	if res.Matched {
		fmt.Fprintf(w, "  rule %d would report %s\n", res.Rule, res.RuleState)
	} else {
		fmt.Fprintln(w, "  no rule matched, so the screen rules report nothing")
	}

	if len(res.TitleRules) == 0 {
		return
	}
	fmt.Fprintf(w, "\ntitle %s\n", strconv.Quote(res.Title))
	if res.Progress != "" {
		fmt.Fprintf(w, "progress %s\n", res.Progress)
	}
	fmt.Fprintln(w, "title rules:")
	printRuleReports(w, res.TitleRules, res.TitleRule, "osc_title")
	if res.TitleMatched {
		fmt.Fprintf(w, "  title rule %d would report %s\n", res.TitleRule, res.TitleRuleState)
	} else {
		fmt.Fprintln(w, "  no title rule matched")
	}
}

// printRuleReports writes one block per rule: its mark, state and priority,
// the region it reads when that is not the block's default, the text it read
// there, and why it refused.
func printRuleReports(w io.Writer, reports []harness.RuleReport, decided int, defaultRegion string) {
	for _, r := range reports {
		mark := " "
		if r.Matched {
			mark = "*"
			if r.Index == decided {
				mark = ">"
			}
		}
		region := ""
		if r.Region != "" && r.Region != defaultRegion {
			region = "  region " + r.Region
		}
		fmt.Fprintf(w, " %s rule %d  %s  priority %d%s\n", mark, r.Index, r.State, r.Priority, region)
		if r.NoRegion {
			fmt.Fprintf(w, "     region %s is not on the screen\n", orNone(r.Region))
		} else if r.NoDialog {
			fmt.Fprintf(w, "     no dialog box on the screen holds all its words\n")
		} else if r.Text != "" {
			for line := range strings.SplitSeq(r.Text, "\n") {
				fmt.Fprintf(w, "     | %s\n", line)
			}
		}
		for _, why := range ruleRefusals(r) {
			fmt.Fprintf(w, "     %s\n", why)
		}
	}
}

// ruleRefusals turns a rule's report into the lines saying why it refused.
func ruleRefusals(r harness.RuleReport) []string {
	var out []string
	if r.Empty {
		out = append(out, "names no strings, so it is refused rather than matching every pane")
	}
	for _, s := range r.Missing {
		out = append(out, "all: "+strconv.Quote(s)+" is not on the screen")
	}
	if len(r.NoneOf) > 0 {
		quoted := make([]string, 0, len(r.NoneOf))
		for _, s := range r.NoneOf {
			quoted = append(quoted, strconv.Quote(s))
		}
		out = append(out, "any: none of "+strings.Join(quoted, ", ")+" is on the screen")
	}
	for _, s := range r.Blocked {
		out = append(out, "not: "+strconv.Quote(s)+" is on the screen and vetoes the rule")
	}
	for _, s := range r.MissingRegex {
		out = append(out, "regex: "+strconv.Quote(s)+" matches nothing on the screen")
	}
	for _, s := range r.BlockedRegex {
		out = append(out, "not_regex: "+strconv.Quote(s)+" matches the screen and vetoes the rule")
	}
	out = append(out, r.Groups...)
	return out
}
