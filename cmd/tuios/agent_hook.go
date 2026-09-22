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
gemini-cli, opencode.

The pane is found from --window, then TUIOS_PANE_ID, then the process's
controlling terminal, then its parent processes, so a harness or sandbox
that scrubs the environment is still reported for. A payload that does not
parse, an event with no mapping, a subagent's event, and an event from a
harness other than the one TUIOS_AGENT names are all reported as nothing,
never as done.

It always exits 0, prints nothing a harness would read as an answer (Gemini
CLI gets an empty JSON object), and gives up after 500ms when the daemon is
slow or gone. Use --explain to see on stderr what it decided and why.`,
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
				self: integration.SelfProcess,
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
	Applied *bool  `json:"applied,omitempty"`
	State   string `json:"state,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Error   string `json:"error,omitempty"`
}

// runAgentHook runs one hook event under the deadline. It returns nothing,
// because nothing it could return may reach the harness as a failure.
func runAgentHook(o agentHookOptions, args []string, hio agentHookIO) {
	harness := args[0]
	if id, ok := integration.Canonical(harness); ok && id == integration.GeminiCLI {
		// Gemini CLI parses a hook's stdout as JSON. An empty object is the
		// answer that changes nothing, and it goes out first so a deadline
		// cannot leave the hook with no answer at all.
		_, _ = io.WriteString(hio.stdout, "{}\n")
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
	if o.explain {
		line, _ := json.Marshal(out)
		fmt.Fprintln(hio.stderr, string(line))
	}
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
	out.Session, out.Window, out.PaneBy, err = resolveHookPane(o, hio, client)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	res, err := reportHook(client, out.Session, out.Window, out.Harness, *out.Report)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Applied, out.State, out.Reason = &res.Applied, res.State, res.Reason
	return out
}

// resolveHookPane finds the pane to report for: the --window flag, then
// TUIOS_PANE_ID, then the daemon's resolve-pane on the process's terminal
// session and its ancestors.
func resolveHookPane(o agentHookOptions, hio agentHookIO, client verbCaller) (sess, window, by string, err error) {
	if o.window != "" {
		return firstNonEmptyString(o.session, hio.getenv("TUIOS_SESSION")), o.window, "flag", nil
	}
	if id := hio.getenv("TUIOS_PANE_ID"); id != "" {
		return firstNonEmptyString(o.session, hio.getenv("TUIOS_SESSION")), id, "env", nil
	}
	if hio.self == nil {
		return "", "", "", errors.New("no pane: TUIOS_PANE_ID is unset")
	}
	sid, pids := hio.self()
	raw, err := client.Call("resolve-pane", map[string]any{"sid": sid, "pids": pids})
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
	Applied bool   `json:"applied"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
}

// reportHook sends one report. A daemon older than the hook fields rejects
// them as unknown params; the report is then sent again with only the fields
// every daemon takes, unless it carried an if_state, whose condition cannot be
// dropped without turning a finished pane back to working.
func reportHook(client verbCaller, sess, window, harness string, r integration.Report) (hookReportResult, error) {
	params := map[string]any{
		"session": sess,
		"window":  window,
		"state":   r.State,
		"harness": harness,
	}
	if r.Message != "" {
		params["message"] = r.Message
	}
	full := map[string]any{}
	for k, v := range params {
		full[k] = v
	}
	for k, v := range map[string]string{
		"kind":             r.Kind,
		"agent_session_id": r.SessionID,
		"transcript_path":  r.TranscriptPath,
		"if_state":         r.IfState,
	} {
		if v != "" {
			full[k] = v
		}
	}
	raw, err := client.Call("set-agent-state", full)
	var callErr *session.VerbCallError
	if err != nil && errors.As(err, &callErr) && callErr.Code == session.ErrVerbInvalidParams && len(full) > len(params) {
		if r.IfState != "" {
			return hookReportResult{}, fmt.Errorf("the daemon predates if_state, so a conditional report was not sent: %w", err)
		}
		raw, err = client.Call("set-agent-state", params)
	}
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
