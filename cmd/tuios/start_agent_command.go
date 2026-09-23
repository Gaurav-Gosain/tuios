package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// newStartAgentCommand builds `tuios start-agent`.
func newStartAgentCommand() *cobra.Command {
	var sessionName, name, cwd, repo, prompt string
	var env []string
	var workspace, readyTimeout int
	var focus, clone, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "start-agent <agent> [-- args...]",
		Short: "Start an agent in a new pane and return once it is ready",
		Long: `Start an agent in a new pane of a session, and return once it shows it is at
its prompt: its state reads idle or done, from a hook, its screen or its title.
With --prompt, the first prompt is typed then, and checked the way fan checks
it.

<agent> is the agent as you would type it, arguments included: claude,
"codex --model o5", or any program. Arguments after -- are passed to it as an
argv too, so nothing needs quoting. It is looked up on your PATH, which the
command sends, and --env passes more of your environment. A program no
harness manifest recognises is ready only once it reports a state itself.

The pane opens in the session -s names, which is created when it does not
exist. It starts in --cwd, or the main checkout of the repository --repo
names, or else the focused pane's directory.

-s HOST:SESSION starts it on another machine from the [hosts] table. PATH is
not sent then, and the agent is looked up on that machine's PATH; --env there
is refused, since env does not cross machines. The agent starts in that
machine's checkout of the repository the current directory is in, found by
its origin URL under [hosts.NAME] repos_root, and --clone clones it there when
the host has none. --cwd or --repo name a directory on the host instead.

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
  tuios start-agent 'codex --model o5' --name tests --cwd ~/src/api --prompt 'Run the tests and fix what fails.'

  # Claude Code on host build, in its checkout of this repository
  tuios start-agent -s build:api claude --prompt 'Profile the build.' -- --model opus`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			positional, extra := args, []string(nil)
			if dash := c.ArgsLenAtDash(); dash >= 0 {
				positional, extra = args[:dash], args[dash:]
			}
			if len(positional) != 1 {
				return fmt.Errorf("start-agent takes one agent, got %d arguments. Quote the agent with its arguments, pass the prompt with --prompt, and put more arguments for the agent after --", len(positional))
			}
			callerEnv, err := fanCallerEnv(env, os.Getenv, os.Environ())
			if err != nil {
				return err
			}
			return runStartAgent(startAgentOptions{
				session: sessionName, agent: positional[0], args: extra, name: name, cwd: cwd, repo: repo, prompt: prompt,
				env: callerEnv, explicitEnv: len(env) > 0, workspace: workspace, readyTimeout: readyTimeout, focus: focus, clone: clone,
			}, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session to open the pane in, HOST:SESSION for another machine. Created when it does not exist (default: most recently active)")
	cmd.Flags().StringVar(&name, "name", "", "The pane's name, which list-agents shows and -w takes")
	cmd.Flags().StringVar(&cwd, "cwd", "", "The directory the agent starts in (default: the focused pane's)")
	cmd.Flags().StringVar(&repo, "repo", "", "A directory inside a repository: the agent starts in its main checkout")
	cmd.Flags().BoolVar(&clone, "clone", false, "On another machine, clone this repository there when it has no checkout")
	cmd.Flags().IntVar(&workspace, "workspace", 0, "The workspace to open the pane on (default: the current one)")
	cmd.Flags().BoolVar(&focus, "focus", false, "Focus the new pane")
	cmd.Flags().StringVar(&prompt, "prompt", "", "A first prompt, typed once the agent is ready")
	cmd.Flags().IntVar(&readyTimeout, "ready-timeout", 0, "Milliseconds to wait for the agent to be ready (default 120000)")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Pass a variable to the agent: NAME for your own value, NAME=VALUE to set one. Repeatable. PATH is sent too, except to a session on another machine")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// startAgentOptions is what `tuios start-agent` sends.
type startAgentOptions struct {
	session, agent, name, cwd, repo, prompt string
	args                                    []string
	env                                     map[string]string
	// explicitEnv says the person passed --env. Without it the env holds
	// only the PATH the CLI adds on its own.
	explicitEnv             bool
	workspace, readyTimeout int
	focus, clone            bool
}

// startAgentEnv is the env start-agent sends to a target on host ("" for
// this machine). The PATH the CLI adds on its own is this machine's, so it is
// not sent to another machine: the far daemon refuses env over a link, and
// the agent is found on that machine's own PATH. An --env the person asked
// for is always sent, so a remote target refuses it plainly rather than the
// CLI dropping it quietly.
func startAgentEnv(o startAgentOptions, host string) map[string]string {
	if host != "" && !o.explicitEnv {
		return nil
	}
	return o.env
}

// startAgentPlace is where the agent starts, as verb params, for the daemon
// on host, "" for this machine. On this machine it is --cwd, made absolute
// since the daemon is not in this shell's directory, or the checkout --repo
// names, or nothing, for the focused pane's directory. On a host it is --cwd
// or --repo as a directory there, or else this repository by its origin URL.
func startAgentPlace(host string, o startAgentOptions) (map[string]any, error) {
	switch {
	case o.cwd != "" && (o.repo != "" || o.clone):
		return nil, errors.New("--cwd names the directory, and --repo and --clone name a repository. Pass one")
	case o.cwd != "" && host == "":
		abs, err := filepath.Abs(o.cwd)
		if err != nil {
			return nil, err
		}
		return map[string]any{"cwd": abs}, nil
	case o.cwd != "":
		return map[string]any{"cwd": o.cwd}, nil
	case host == "" && o.repo == "" && !o.clone:
		return map[string]any{}, nil
	case host == "" && o.repo != "" && !o.clone:
		abs, err := filepath.Abs(o.repo)
		if err != nil {
			return nil, err
		}
		return map[string]any{"repo": abs}, nil
	}
	return repoParams(host, o.repo, o.clone)
}

func runStartAgent(o startAgentOptions, jsonOutput bool) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	t, err := dialSessionTarget(o.session)
	if err != nil {
		return err
	}
	defer t.Close()
	place, err := startAgentPlace(t.host, o)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	params := t.params(map[string]any{"agent": o.agent})
	for k, v := range place {
		params[k] = v
	}
	for k, v := range map[string]string{"name": o.name, "prompt": o.prompt} {
		if v != "" {
			params[k] = v
		}
	}
	if len(o.args) > 0 {
		params["args"] = o.args
	}
	if env := startAgentEnv(o, t.host); len(env) > 0 {
		params["env"] = env
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
	// waits for it to be taken, so the client waits past both, and past a
	// clone on a host.
	raw, err := t.client.CallWithTimeout("start-agent", params, time.Duration(wait)*time.Millisecond+5*time.Minute)
	if err != nil {
		return reportVerbError(explainHostedVerb(t, "start-agent", err), jsonOutput)
	}
	var res struct {
		Session        string `json:"session"`
		CreatedSession bool   `json:"created_session"`
		WindowID       string `json:"window_id"`
		Name           string `json:"name"`
		Agent          string `json:"agent"`
		Command        string `json:"command"`
		Cwd            string `json:"cwd"`
		Cloned         bool   `json:"cloned"`
		Ready          bool   `json:"ready"`
		ReadyBy        string `json:"ready_by"`
		State          string `json:"state"`
		BlockedBy      string `json:"blocked_by"`
		Reason         string `json:"reason"`
		PromptStatus   string `json:"prompt_status"`
		PromptNote     string `json:"prompt_note"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOutput {
		if err := printVerbResultOn(t, raw, true); err != nil {
			return err
		}
	} else {
		if res.CreatedSession {
			fmt.Printf("Created session '%s'%s.\n", plainLine(res.Session), t.on())
		}
		if res.Cloned {
			fmt.Printf("Cloned the repository to %s%s.\n", plainLine(res.Cwd), t.on())
		}
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
