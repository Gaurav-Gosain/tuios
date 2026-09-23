package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// newStartAgentCommand builds `tuios start-agent`.
func newStartAgentCommand() *cobra.Command {
	var sessionName, name, cwd, prompt string
	var env []string
	var workspace, readyTimeout int
	var focus, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "start-agent <agent>",
		Short: "Start an agent in a new pane and return once it is ready",
		Long: `Start an agent in a new pane of a session, and return once it shows it is at
its prompt: its state reads idle or done, from a hook, its screen or its title.
With --prompt, the first prompt is typed then, and checked the way fan checks
it.

<agent> is the agent as you would type it, arguments included: claude,
"codex --model o5", or any program. It is looked up on your PATH, which the
command sends, and --env passes more of your environment. A program no
harness manifest recognises is ready only once it reports a state itself.

The pane is not focused unless you pass --focus. --name gives it the name
list-agents shows and -w takes, so you can address it as 'reviewer'.

An agent that stops on a question of its own, such as whether to trust the
folder, is not ready: the command prints what it waits on and exits non-zero,
and the pane is kept for the person to answer. So is one that shows nothing
before --ready-timeout.`,
		Example: `  # A reviewer beside you, addressed by name afterwards
  tuios start-agent claude --name reviewer
  tuios ask-agent -w reviewer 'review the diff on this branch'

  # A codex agent with a first prompt, in another directory
  tuios start-agent 'codex --model o5' --name tests --cwd ~/src/api --prompt 'Run the tests and fix what fails.'`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			callerEnv, err := fanCallerEnv(env, os.Getenv, os.Environ())
			if err != nil {
				return err
			}
			return runStartAgent(startAgentOptions{
				session: sessionName, agent: args[0], name: name, cwd: cwd, prompt: prompt,
				env: callerEnv, workspace: workspace, readyTimeout: readyTimeout, focus: focus,
			}, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVar(&name, "name", "", "The pane's name, which list-agents shows and -w takes")
	cmd.Flags().StringVar(&cwd, "cwd", "", "The directory the agent starts in (default: the focused pane's)")
	cmd.Flags().IntVar(&workspace, "workspace", 0, "The workspace to open the pane on (default: the current one)")
	cmd.Flags().BoolVar(&focus, "focus", false, "Focus the new pane")
	cmd.Flags().StringVar(&prompt, "prompt", "", "A first prompt, typed once the agent is ready")
	cmd.Flags().IntVar(&readyTimeout, "ready-timeout", 0, "Milliseconds to wait for the agent to be ready (default 120000)")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Pass a variable to the agent: NAME for your own value, NAME=VALUE to set one. Repeatable. PATH is always sent")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// startAgentOptions is what `tuios start-agent` sends.
type startAgentOptions struct {
	session, agent, name, cwd, prompt string
	env                               map[string]string
	workspace, readyTimeout           int
	focus                             bool
}

func runStartAgent(o startAgentOptions, jsonOutput bool) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	// A relative directory is this shell's, which the daemon is not in.
	cwd := o.cwd
	if cwd != "" && !filepath.IsAbs(cwd) {
		abs, err := filepath.Abs(cwd)
		if err != nil {
			return err
		}
		cwd = abs
	}
	t, err := dialSessionTarget(o.session)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(map[string]any{"agent": o.agent})
	for k, v := range map[string]string{"name": o.name, "cwd": cwd, "prompt": o.prompt} {
		if v != "" {
			params[k] = v
		}
	}
	if len(o.env) > 0 {
		params["env"] = o.env
	}
	if o.workspace > 0 {
		params["workspace"] = o.workspace
	}
	if o.readyTimeout > 0 {
		params["ready_timeout"] = o.readyTimeout
	}
	if o.focus {
		params["focus"] = true
	}
	wait := o.readyTimeout
	if wait <= 0 {
		wait = 120000
	}
	// The daemon answers when the agent is ready, then types the prompt and
	// waits for it to be taken, so the client waits past both.
	raw, err := t.client.CallWithTimeout("start-agent", params, time.Duration(wait)*time.Millisecond+30*time.Second)
	if err != nil {
		return reportVerbError(t.explain("start-agent", err), jsonOutput)
	}
	var res struct {
		WindowID     string `json:"window_id"`
		Name         string `json:"name"`
		Agent        string `json:"agent"`
		Command      string `json:"command"`
		Ready        bool   `json:"ready"`
		ReadyBy      string `json:"ready_by"`
		State        string `json:"state"`
		BlockedBy    string `json:"blocked_by"`
		Reason       string `json:"reason"`
		PromptStatus string `json:"prompt_status"`
		PromptNote   string `json:"prompt_note"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOutput {
		if err := printVerbResultOn(t, raw, true); err != nil {
			return err
		}
	} else {
		who := fmt.Sprintf("%s (%s)", orNone(plainLine(res.Name)), shortWindowID(res.WindowID))
		if res.Ready {
			fmt.Printf("%s is ready: it reads %s%s.\n", who, plainLine(res.ReadyBy), t.on())
		} else {
			state := plainLine(res.State)
			if res.BlockedBy != "" {
				state += " (" + plainLine(res.BlockedBy) + ")"
			}
			fmt.Printf("%s is not ready: it reads %s%s. %s\n", who, state, t.on(), plainLine(res.Reason))
		}
		switch res.PromptStatus {
		case "":
		case "sent":
			fmt.Println("The prompt was typed and taken.")
		default:
			fmt.Printf("The prompt is %s. %s\n", plainLine(res.PromptStatus), plainLine(res.PromptNote))
		}
	}
	if !res.Ready || (res.PromptStatus != "" && res.PromptStatus != "sent") {
		return &statusError{code: 1}
	}
	return nil
}
