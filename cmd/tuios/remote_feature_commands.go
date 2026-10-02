//go:build !slim

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/google/uuid"
)

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
	if windowTarget == "" && (sessionName == "" || sessionName == os.Getenv("TUIOS_SESSION")) {
		if pane := os.Getenv("TUIOS_PANE_ID"); pane != "" && os.Getenv("TUIOS_SESSION") != "" {
			windowTarget, sessionName = pane, os.Getenv("TUIOS_SESSION")
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
		if err := requireIfState(client); err != nil {
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Applied {
		fmt.Fprintf(os.Stderr, "Not applied: %s The pane keeps %q.\n", agentRefusalText(res.Reason), res.ID)
	}
	return nil
}

// setAgentStateExtras are the set-agent-state fields a hook reporter adds.
type setAgentStateExtras struct {
	kind           string
	sessionID      string
	transcriptPath string
	ifState        string
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
			return nil, fmt.Errorf("%q is not key=value. Write key= to remove a key", arg)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		b.Write(k)
		b.WriteByte(':')
		if value == "" {
			b.WriteString("null")
			continue
		}
		v, _ := json.Marshal(value)
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
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		out, err := json.MarshalIndent(pretty, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to render response: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	var res struct {
		Truncated []string `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Truncated) > 0 {
		fmt.Fprintf(os.Stderr, "Cut to %d characters: %s\n", session.AgentMetaMaxValue, strings.Join(res.Truncated, ", "))
	}
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(res.State)
	return nil
}

// detectExplanation is the explain-agent-detect result, decoded for printing.
type detectExplanation struct {
	WindowID       string                 `json:"window_id"`
	State          string                 `json:"state"`
	Source         string                 `json:"source"`
	HarnessID      string                 `json:"harness_id"`
	AutoDetected   bool                   `json:"auto_detected"`
	Identity       string                 `json:"identity"`
	Confidence     string                 `json:"confidence"`
	NeedsYou       bool                   `json:"needs_you"`
	Activity       string                 `json:"activity"`
	Running        bool                   `json:"running"`
	Reason         string                 `json:"reason"`
	Verdict        string                 `json:"verdict"`
	Evidence       []string               `json:"evidence"`
	Ignored        []string               `json:"ignored"`
	Process        harness.ProcReport     `json:"process"`
	Manifests      []harness.DetectReport `json:"manifests"`
	Group          []detectGroupMember    `json:"group"`
	NameList       string                 `json:"name_list"`
	Matched        bool                   `json:"matched"`
	MatchedHarness string                 `json:"matched_harness"`
	MatchedRule    string                 `json:"matched_rule"`
	MatchedVia     []string               `json:"matched_via"`
	Note           string                 `json:"note"`
}

// detectGroupMember is one process the detector read behind a wrapper.
type detectGroupMember struct {
	PID     int      `json:"pid"`
	Depth   int      `json:"depth"`
	Comm    string   `json:"comm"`
	Argv    []string `json:"argv"`
	Exe     string   `json:"exe"`
	Matched bool     `json:"matched"`
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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

// screenExplanation is the explain-agent-screen result, decoded for printing.
type screenExplanation struct {
	WindowID  string               `json:"window_id"`
	HarnessID string               `json:"harness_id"`
	State     string               `json:"state"`
	Source    string               `json:"source"`
	Enabled   bool                 `json:"enabled"`
	Lines     int                  `json:"lines"`
	Tail      []string             `json:"tail"`
	Matched   bool                 `json:"matched"`
	Rule      int                  `json:"rule"`
	RuleState string               `json:"rule_state"`
	Rules     []harness.RuleReport `json:"rules"`
	// ManifestSource is "bundled" or the user file in force, and
	// ReplacesBundled says that file took a bundled manifest's place.
	ManifestSource  string `json:"manifest_source"`
	ReplacesBundled bool   `json:"replaces_bundled"`
	// The title block: the pane's title, its last progress report, and what
	// each title rule made of them.
	Title          string               `json:"title"`
	Progress       string               `json:"progress"`
	TitleMatched   bool                 `json:"title_matched"`
	TitleRule      int                  `json:"title_rule"`
	TitleRuleState string               `json:"title_rule_state"`
	TitleRules     []harness.RuleReport `json:"title_rules"`
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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

// runTapeExec executes a tape file in a running TUIOS session.
func runTapeExec(sessionName, filePath string) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	// Read the tape file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}
	script := string(content)

	// Validate the script first
	lexer := tape.New(script)
	parser := tape.NewParser(lexer)
	commands := parser.Parse()

	if len(commands) == 0 {
		return fmt.Errorf("tape script has no commands or contains errors")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the execute command with tape script
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		TapeScript:  script,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResult(client, msg, requestID); err != nil {
		return err
	}

	return nil
}

// screenshotRequest is the CLI's resolved screenshot call.
type screenshotRequest struct {
	session    string
	window     string
	format     string
	theme      string
	frame      string
	out        string
	lines      int
	scrollback bool
	cursor     bool
	copy       bool
	noCopy     bool
	jsonOutput bool
}

// runScreenshot renders a window to an image file through the daemon and then
// says where the file is.
//
// The daemon writes the file, not the CLI. They talk over a unix socket, so
// they are the same machine and the path the daemon reports is a path the
// caller can open. That is also why a clipboard copy is attempted here: this
// process is on the user's own machine, which is the rule an image copy has to
// obey.
func runScreenshot(req screenshotRequest) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("screenshot", map[string]any{
		"session":    req.session,
		"window":     req.window,
		"format":     req.format,
		"theme":      req.theme,
		"frame":      req.frame,
		"scrollback": req.scrollback,
		"lines":      req.lines,
		"cursor":     req.cursor,
		"out":        req.out,
	})
	if err != nil {
		return reportVerbError(explainVerbError("screenshot", err), req.jsonOutput)
	}

	var res struct {
		Path     string   `json:"path"`
		Host     string   `json:"host"`
		Format   string   `json:"format"`
		Cols     int      `json:"cols"`
		Rows     int      `json:"rows"`
		Bytes    int      `json:"bytes"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	copied, copyNote := maybeCopyScreenshot(res.Path, res.Format, req)

	if req.jsonOutput {
		out := map[string]any{
			"success": true, "path": res.Path, "host": res.Host,
			"format": res.Format, "cols": res.Cols, "rows": res.Rows,
			"bytes": res.Bytes, "copied": copied,
			"warnings": append(append([]string{}, res.Warnings...), copyNote...),
		}
		outputJSON(out)
		return nil
	}
	fmt.Println(res.Path)
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	for _, w := range copyNote {
		fmt.Fprintln(os.Stderr, w)
	}
	return nil
}

// maybeCopyScreenshot attempts the clipboard copy the flags asked for and
// reports, in plain words, what actually happened.
func maybeCopyScreenshot(path, format string, req screenshotRequest) (bool, []string) {
	if req.noCopy || !req.copy || path == "" {
		return false, nil
	}
	f, ok := shot.ParseFormat(format)
	if !ok {
		return false, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 - the daemon just wrote this path
	if err != nil {
		return false, []string{"The file was written but could not be read back to copy it."}
	}
	if _, err := capture.CopyImage(path, data, f.MediaType()); err != nil {
		return false, []string{"The file was saved. " + err.Error()}
	}
	return true, nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// sendAndWaitForResult sends a message and waits for the result (human-readable output).
func sendAndWaitForResult(client *session.Client, msg *session.Message, requestID string) error {
	return sendAndWaitForResultWithFormat(client, msg, requestID, false, nil)
}
