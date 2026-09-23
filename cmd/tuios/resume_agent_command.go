package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// resumeAgentResult is the resume-agent result the CLI prints.
type resumeAgentResult struct {
	WindowID       string `json:"window_id"`
	Harness        string `json:"harness"`
	AgentSessionID string `json:"agent_session_id"`
	Command        string `json:"command"`
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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Typed {
		fmt.Fprintln(w, res.Command)
		return nil
	}
	fmt.Fprintf(w, "Resumed %s conversation %s in %s: %s\n",
		plainLine(res.Harness), plainLine(res.AgentSessionID), shortWindowID(res.WindowID), res.Command)
	return nil
}

// newResumeAgentCommand is `tuios resume-agent`.
func newResumeAgentCommand() *cobra.Command {
	var sessionName, windowTarget string
	var dryRun, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "resume-agent",
		Short: "Resume the agent conversation a pane ran before a daemon restart",
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
the agent was doing when the daemon stopped did not finish.`,
		Example: `  # Resume the focused pane's conversation
  tuios resume-agent

  # See the command for a pane without typing it
  tuios resume-agent -w build --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runResumeAgent(sessionName, windowTarget, dryRun, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVarP(&windowTarget, "window", "w", "", "Target window by name or ID (default: focused)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the command without typing it")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}
