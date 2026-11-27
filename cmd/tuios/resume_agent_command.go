package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/spf13/cobra"
)

// resumeAgentResult is the resume-agent result the CLI prints.
type resumeAgentResult struct {
	WindowID       string `json:"window_id"`
	Harness        string `json:"harness"`
	AgentSessionID string `json:"agent_session_id"`
	Command        string `json:"command"`
	Warning        string `json:"warning"`
	Typed          bool   `json:"typed"`
}

func runResumeAgent(sessionName, windowTarget string, dryRun, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"session": sessionName, "window": windowTarget}
	if dryRun {
		params["dry_run"] = true
	}
	raw, err := client.Call("resume-agent", params)
	if err != nil {
		if jsonOutput {
			return reportVerbError(err, true)
		}
		return explainVerbError("resume-agent", err)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	return printResumeAgent(os.Stdout, raw)
}

// printResumeAgent says what was typed, or with a dry run what would be.
func printResumeAgent(w io.Writer, raw json.RawMessage) error {
	var res resumeAgentResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	switch {
	case !res.Typed:
		fmt.Fprintln(w, res.Command)
	case res.AgentSessionID == "":
		// A program that reported its own command (OSC 7577): no harness,
		// no conversation.
		fmt.Fprintf(w, "Resumed the program in %s: %s\n", shortWindowID(res.WindowID), res.Command)
	default:
		what := "conversation " + plainLine(res.AgentSessionID)
		if h := plainLine(res.Harness); h != "" {
			what = h + " " + what
		}
		fmt.Fprintf(w, "Resumed the %s in %s: %s\n", what, shortWindowID(res.WindowID), res.Command)
	}
	if res.Warning != "" {
		fmt.Fprintln(w, plainLine(res.Warning))
	}
	return nil
}

// newResumeAgentCommand is `tuios resume-agent`.
func newResumeAgentCommand() *cobra.Command {
	var sessionName, windowTarget string
	var dryRun, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "resume-agent",
		Short: "Resume the agent conversation or the program a pane ran before a daemon restart",
		Long: `Type a pane's resume command into its shell: the harness's command from its
manifest's [resume] block, with the conversation id a hook recorded for the pane.
Claude Code's is 'claude --resume <id>', Codex's is 'codex resume <id>'.

A daemon restart ends every program in every pane. The restore brings back the
layout with a new shell in each pane, and daemon.resume_agents says what happens
to the conversations: ask (the default) puts a Resume item in the Inbox for each
pane, auto types this command for you, and off does neither. This command is
the answer to the ask, and works any time a pane has a recorded conversation.

It types only when the pane's shell is at its prompt, so the command never lands
in a running program. It brings back the conversation, not the process: whatever
the agent was doing when the daemon stopped did not finish.

A program that told tuios how to start it again (OSC 7577) gets the same Resume
item. For such a pane, this command types the program's resume line instead.`,
		Example: `  # Resume the focused pane's conversation
  tuios resume-agent

  # See the command for a pane without typing it
  tuios resume-agent -w build --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runResumeAgent(sessionName, windowTarget, dryRun, jsonOutput)
		},
	}
	addSessionFlag(cmd, &sessionName)
	addWindowFlag(cmd, &windowTarget)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the command without typing it")
	addJSONFlag(cmd, &jsonOutput)
	return cmd
}
