package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
	"github.com/spf13/cobra"
)

// The worktree commands are thin: every decision is the daemon's, made in the
// worktree verbs, so a person at a shell and an agent on the socket get the
// same refusals for the same reasons. What lives here is the argument shapes
// and the sentences. fan keep is the daemon's keep-fan; against a daemon from
// before that verb it is composed here, as remove-worktree over every sibling
// but one.

// worktreeRow is one entry of list-worktrees as the CLI reads it.
type worktreeRow struct {
	Session      string `json:"session"`
	Repo         string `json:"repo"`
	RepoRoot     string `json:"repo_root"`
	Branch       string `json:"branch"`
	Path         string `json:"path"`
	Base         string `json:"base"`
	Group        string `json:"group"`
	Managed      bool   `json:"managed"`
	Gone         bool   `json:"gone"`
	State        string `json:"state"`
	Harness      string `json:"harness"`
	Windows      int    `json:"windows"`
	Attached     bool   `json:"attached"`
	PromptStatus string `json:"prompt_status"`
	PromptNote   string `json:"prompt_note"`
	ReadyBy      string `json:"prompt_ready_by"`
	Agent        string `json:"agent"`
	Changes      *int   `json:"changes"`
	Ahead        *int   `json:"ahead"`
}

// newWorktreeCommand builds `tuios worktree` and its subcommands.
func newWorktreeCommand() *cobra.Command {
	worktreeCmd := &cobra.Command{
		Use:   "worktree",
		Short: "Work in git worktrees, one session per worktree",
		Long: `A git worktree as a session.

'worktree new' makes a worktree of the repository you are in and a session
whose directory is that worktree. The rail groups these sessions under the
repository and labels each by its branch. 'worktree ls' lists them with the
agent state in each. 'worktree rm' removes one. It refuses to discard
uncommitted work unless you say so, and it never deletes a branch.

Worktrees go under $XDG_DATA_HOME/tuios/worktrees/<repo>/<branch>.`,
		Example: `  # A worktree on a new branch, and a session in it
  tuios worktree new feat/retry

  # List worktree sessions with their agent state
  tuios worktree ls

  # Remove one and keep its uncommitted changes in git stash
  tuios worktree rm api-feat-retry --stash`,
	}

	var newRepo, newBase, newName, newAgent, newHost string
	var newDetach, newJSON, newClone bool
	newCmd := &cobra.Command{
		Use:   "new <branch>",
		Short: "Create a worktree and a session in it",
		Long: `Create a git worktree on a branch and open a session in it.

The repository is the one the current directory is in, or the one --repo
names. A branch that does not exist is created from --base, or from HEAD.

The session is named <repo>-<branch>, with every slash in the branch turned
into a hyphen. --name picks another name. The session attaches at once, or
stays headless with --detach.

--agent starts an agent CLI in the session instead of a shell. Name it the
way you type it: claude, codex, gemini. tuios recognises the agents its
harness manifests describe.

--host makes the worktree on another machine from the [hosts] table. The
repository is the one the current directory is in, found there by its
origin URL, under the host's repos_root when [hosts.NAME] sets one. --clone
clones it there when the host has no checkout. With --host, --repo names a
directory on the host instead.`,
		Example: `  # A new branch from HEAD, attached
  tuios worktree new feat/retry

  # A branch from main, headless, running Claude Code
  tuios worktree new feat/retry --base main --agent claude --detach

  # A worktree of another repository
  tuios worktree new fix/typo --repo /src/api

  # The same repository on host build, cloned there if it is missing
  tuios worktree new feat/retry --host build --clone --detach`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runWorktreeNew(args[0], newRepo, newBase, newName, newAgent, newHost, newClone, newDetach, newJSON)
		},
	}
	newCmd.Flags().StringVar(&newRepo, "repo", "", "A directory inside the repository (default: the current directory)")
	newCmd.Flags().StringVar(&newBase, "base", "", "Ref a new branch starts from (default: HEAD)")
	newCmd.Flags().StringVar(&newName, "name", "", "Session name (default: <repo>-<branch>)")
	newCmd.Flags().StringVar(&newAgent, "agent", "", "Start this agent CLI in the session instead of a shell")
	newCmd.Flags().BoolVarP(&newDetach, "detach", "d", false, "Create the session headless without attaching a client")
	newCmd.Flags().BoolVar(&newJSON, "json", false, "Output result as JSON")
	newCmd.Flags().StringVar(&newHost, "host", "", "Make the worktree on this machine from the [hosts] table")
	newCmd.Flags().BoolVar(&newClone, "clone", false, "With --host, clone the repository there when the host has no checkout")
	_ = newCmd.RegisterFlagCompletionFunc("host", completeConfiguredHosts)

	var lsRepo, lsGroup, lsHost string
	var lsJSON bool
	lsCmd := &cobra.Command{
		Use:   "ls",
		Short: "List worktree sessions",
		Long: `List every session whose directory is a git worktree.

Each row shows the session, repository, branch, the agent state in it, how
many uncommitted changes it holds, and what became of a fan prompt. A row
marked gone is a session whose worktree directory was removed under it. The
session is kept, so what the agent printed can still be read.`,
		Example: `  tuios worktree ls
  tuios worktree ls --repo api
  tuios worktree ls --group fan/add-retry --json
  tuios worktree ls --host build`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWorktreeList(lsHost, lsRepo, lsGroup, lsJSON)
		},
	}
	lsCmd.Flags().StringVar(&lsRepo, "repo", "", "Only worktrees of this repository, by name")
	lsCmd.Flags().StringVar(&lsGroup, "group", "", "Only the sessions of one fan-out, by its branch stem")
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "Output as JSON")
	lsCmd.Flags().StringVar(&lsHost, "host", "", "List the worktree sessions on this machine from the [hosts] table")
	_ = lsCmd.RegisterFlagCompletionFunc("host", completeConfiguredHosts)

	var rmStash, rmForce, rmKeepSession, rmJSON bool
	rmCmd := &cobra.Command{
		Use:   "rm <session>",
		Short: "Remove a worktree and kill its session",
		Long: `Remove a worktree session's worktree with git worktree remove, then kill
the session.

A worktree with uncommitted changes is refused. Nothing is removed. Run
again with --stash to keep the changes in git stash, or with --force to
discard them. --force is the only option that discards work.

The branch is never deleted. Every commit made in the worktree stays in the
repository.

HOST:SESSION removes a worktree session on another machine.`,
		Example: `  tuios worktree rm api-feat-retry
  tuios worktree rm api-feat-retry --stash
  tuios worktree rm api-feat-retry --force
  tuios worktree rm build:api-feat-retry --stash`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorktreeSessions,
		RunE: func(_ *cobra.Command, args []string) error {
			return runWorktreeRemove(args[0], rmStash, rmForce, rmKeepSession, rmJSON)
		},
	}
	rmCmd.Flags().BoolVar(&rmStash, "stash", false, "Keep uncommitted changes in git stash before removing")
	rmCmd.Flags().BoolVar(&rmForce, "force", false, "Discard uncommitted changes")
	rmCmd.Flags().BoolVar(&rmKeepSession, "keep-session", false, "Leave the session running after the worktree is removed")
	rmCmd.Flags().BoolVar(&rmJSON, "json", false, "Output result as JSON")

	var diffStat bool
	diffCmd := &cobra.Command{
		Use:   "diff <session>",
		Short: "Show what a worktree session changed",
		Long: `Print the commits a worktree session made on top of its base, then its
uncommitted changes against HEAD, then its untracked files.

Use it to compare what each session of a fan-out produced before you keep
one.`,
		Example: `  tuios worktree diff api-fan-add-retry-2
  tuios worktree diff api-fan-add-retry-2 --stat`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorktreeSessions,
		RunE: func(_ *cobra.Command, args []string) error {
			return runWorktreeDiff(args[0], diffStat)
		},
	}
	diffCmd.Flags().BoolVar(&diffStat, "stat", false, "Show the summary form of the diff")

	var pullRepo, pullBranch, pullName string
	var pullDetach, pullJSON bool
	pullCmd := &cobra.Command{
		Use:   "pull <host:session>",
		Short: "Bring a worktree session's work from another machine into a worktree here",
		Long: `Copy what a worktree session on another machine has done into a new
worktree session on this one.

The session's commits come across as a git bundle and are fetched into a new
branch of the repository you are in, or the one --repo names. Its uncommitted
work, untracked files included, comes across as a patch and is applied,
uncommitted, in a new worktree on that branch. A session is made in the
worktree the way 'tuios worktree new' makes one.

Only the commits past the worktree's base are sent when this repository has
the base commit. Otherwise the whole branch is sent. Nothing on the other
machine is changed.

The branch here has the same name as there, or the name --branch gives. A
branch that already exists here is refused, so nothing is overwritten.`,
		Example: `  # The second agent of a fan-out on build, attached here
  tuios worktree pull build:api-fan-add-retry-2

  # Under another branch name, headless
  tuios worktree pull build:api-fan-add-retry-2 --branch try/retry-build --detach`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runWorktreePull(args[0], pullRepo, pullBranch, pullName, pullDetach, pullJSON)
		},
	}
	pullCmd.Flags().StringVar(&pullRepo, "repo", "", "A directory inside the repository to pull into (default: the current directory)")
	pullCmd.Flags().StringVar(&pullBranch, "branch", "", "Name of the branch here (default: the branch's name there)")
	pullCmd.Flags().StringVar(&pullName, "name", "", "Session name (default: <repo>-<branch>)")
	pullCmd.Flags().BoolVarP(&pullDetach, "detach", "d", false, "Create the session headless without attaching a client")
	pullCmd.Flags().BoolVar(&pullJSON, "json", false, "Output result as JSON")

	worktreeCmd.AddCommand(newCmd, lsCmd, rmCmd, diffCmd, pullCmd)
	return worktreeCmd
}

// newFanCommand builds `tuios fan` and `tuios fan keep`.
func newFanCommand() *cobra.Command {
	var fanAgents, fanEnv, fanPrompts, fanGrants []string
	var fanRepo, fanBase, fanName, fanHost string
	var fanWait, fanJSON, fanClone bool
	fanCmd := &cobra.Command{
		Use:   "fan <count> <prompt>",
		Short: "Fan a prompt out across several agents, each in its own worktree",
		Long: `Start a prompt in several agents at once, each in a git worktree of its
own, and watch them side by side.

tuios creates <count> worktrees of the repository you are in, starts an agent
in each, and types the prompt into each agent once the agent shows it is at
its prompt. The command prints the sessions and returns. The rail shows them
under the repository. 'tuios worktree ls --group' shows which prompts were
sent.

--agent names the agent the way you would type it, arguments included:
claude, "codex --model o5", or any program. Several, separated by commas or
given as several --agent flags, are cycled across the sessions, so
--agent claude,codex,gemini with a count of 3 starts one of each. The program
is looked up on your PATH, which the command sends, and --env passes more of
your environment (--env NAME for your value, --env NAME=VALUE to set one).

--prompt, given once per session, gives each session its own prompt instead
of one for all; the count is then how many there are.

An agent that has not shown it is at its prompt after 30 seconds is marked
held, and the Inbox asks you to look at its pane: most often it shows a
first-run choice only you can answer. Its prompt is typed as soon as it is
ready.

The branches are a stem, then stem-2, stem-3 and so on. The stem is 'fan/'
and the first words of the prompt, or --name.

--grants says what every agent may do through tuios: read, write, fan,
respond, admin, or none. Without it they hold the default of
[agents.permissions]; a fan run from a pane without admin gives them that
pane's own grants.

'tuios fan compare <session>' shows the attempts side by side, with what each
changed and its last check. 'tuios fan verify <session> -- <command>' runs
one check in every attempt, and 'tuios fan diff A B' shows what two attempts
did differently.

When one result is the one you want, 'tuios fan keep <session>' removes the
others. It refuses to discard their uncommitted work unless you say so.

--host runs the fan-out on another machine from the [hosts] table. The
repository is the one the current directory is in, found there by its
origin URL, and --clone clones it there when the host has none. The agent
must be installed on that machine. The sessions show in the rail under the
host. 'tuios fan keep HOST:SESSION' keeps one there, and 'tuios worktree
pull HOST:SESSION' brings its work here.`,
		Example: `  # Three Claude Code agents on the same task
  tuios fan 3 --agent claude 'Add a retry with backoff to the HTTP client.'

  # One each of three harnesses, one with its own arguments
  tuios fan 3 --agent 'claude,codex --model o5,gemini' 'Add a retry with backoff.'

  # Two agents, each with its own prompt, and an API key from your shell
  tuios fan --agent claude --env ANTHROPIC_API_KEY --prompt 'Add a retry.' --prompt 'Add a timeout.'

  # From a branch, with a stem you chose, waiting until every prompt is sent
  tuios fan 2 --agent codex --base main --name try/retry --wait 'Add a retry.'

  # Keep the second one, and stash what the others did
  tuios fan keep api-fan-add-a-retry-with-2 --stash

  # Three agents on host build, then the winner's work brought here
  tuios fan 3 --host build --agent claude 'Add a retry.'
  tuios worktree pull build:api-fan-add-retry-2`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			count, prompt := 0, ""
			switch {
			case len(fanPrompts) > 0 && len(args) == 2:
				return fmt.Errorf("--prompt gives each session its own prompt, so the command takes no prompt argument")
			case len(fanPrompts) == 0 && len(args) != 2:
				return fmt.Errorf("fan takes a count and a prompt, or --prompt once per session")
			case len(args) == 2:
				prompt = args[1]
			}
			if len(args) > 0 {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("count must be a number, got %q", args[0])
				}
				count = n
			}
			env, err := fanCallerEnv(fanEnv, os.Getenv, os.Environ())
			if err != nil {
				return err
			}
			return runFan(fanOptions{
				host: fanHost, count: count, agents: fanAgents, prompt: prompt, prompts: fanPrompts, env: env,
				explicitEnv: len(fanEnv) > 0, repo: fanRepo, base: fanBase, name: fanName, clone: fanClone,
				grants: fanGrants,
			}, fanWait, fanJSON)
		},
	}
	fanCmd.Flags().StringSliceVar(&fanAgents, "agent", nil, "The agent to run, as you would type it, arguments included: claude, \"codex --model o5\". Several, comma-separated or repeated, are cycled across the sessions (required)")
	fanCmd.Flags().StringArrayVar(&fanEnv, "env", nil, "Pass a variable to every agent: NAME for your own value, NAME=VALUE to set one. Repeatable. PATH is always sent")
	fanCmd.Flags().StringArrayVar(&fanPrompts, "prompt", nil, "One session's prompt. Repeat it once per session instead of giving one prompt for all")
	fanCmd.Flags().StringVar(&fanRepo, "repo", "", "A directory inside the repository (default: the current directory)")
	fanCmd.Flags().StringVar(&fanBase, "base", "", "Ref every branch starts from (default: HEAD)")
	fanCmd.Flags().StringVar(&fanName, "name", "", "Branch stem (default: fan/ and the first words of the prompt)")
	fanCmd.Flags().BoolVar(&fanWait, "wait", false, "Return only when every prompt is sent or given up on")
	fanCmd.Flags().BoolVar(&fanJSON, "json", false, "Output result as JSON")
	fanCmd.Flags().StringVar(&fanHost, "host", "", "Run the fan-out on this machine from the [hosts] table")
	fanCmd.Flags().BoolVar(&fanClone, "clone", false, "With --host, clone the repository there when the host has no checkout")
	fanCmd.Flags().StringSliceVar(&fanGrants, "grants", nil, "What every agent may do through tuios, comma separated: read, write, fan, respond, admin, or none (default: [agents.permissions], or the calling pane's own)")
	_ = fanCmd.RegisterFlagCompletionFunc("host", completeConfiguredHosts)
	_ = fanCmd.RegisterFlagCompletionFunc("grants", completeGrantNames)
	_ = fanCmd.MarkFlagRequired("agent")

	var keepStash, keepForce, keepJSON bool
	keepCmd := &cobra.Command{
		Use:   "keep <session>",
		Short: "Keep one session of a fan-out and remove the others",
		Long: `Keep one worktree session of a fan-out and remove its siblings: the other
sessions that share its branch stem.

The session you name is not touched. Each sibling is removed the way
'tuios worktree rm' removes it. A sibling with uncommitted changes is left
in place unless --stash keeps its changes in git stash or --force discards
them. Branches are never deleted.

HOST:SESSION keeps a session of a fan-out on another machine and removes
its siblings there.`,
		Example: `  tuios fan keep api-fan-add-a-retry-with-2
  tuios fan keep api-fan-add-a-retry-with-2 --stash
  tuios fan keep build:api-fan-add-a-retry-with-2`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorktreeSessions,
		RunE: func(_ *cobra.Command, args []string) error {
			return runFanKeep(args[0], keepStash, keepForce, keepJSON)
		},
	}
	keepCmd.Flags().BoolVar(&keepStash, "stash", false, "Keep every sibling's uncommitted changes in git stash before removing it")
	keepCmd.Flags().BoolVar(&keepForce, "force", false, "Discard every sibling's uncommitted changes")
	keepCmd.Flags().BoolVar(&keepJSON, "json", false, "Output result as JSON")

	fanCmd.AddCommand(keepCmd, newFanCompareCommand(), newFanDiffCommand(), newFanVerifyCommand())
	return fanCmd
}

// repoArg is the repo parameter: the flag, or the directory the person is in.
func repoArg(repo string) (string, error) {
	if repo != "" {
		return repo, nil
	}
	return os.Getwd()
}

// resolveAgentCommand turns the name a person types for an agent into the
// program that starts it, refusing a name no harness manifest knows.
func resolveAgentCommand(agent string) (string, error) {
	reg, _ := harness.Load(harness.UserDir())
	_, command, ok := reg.Resolve(agent)
	if !ok {
		return "", &diagnosticError{
			What:  fmt.Sprintf("%q is not an agent tuios recognises.", agent),
			Cause: "the name matches no harness manifest.",
			Fix:   "name one of: " + strings.Join(reg.IDs(), ", ") + ".",
		}
	}
	return command, nil
}

func runWorktreeNew(branch, repo, base, name, agent, host string, clone, detach, jsonOutput bool) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	params, err := repoParams(host, repo, clone)
	if err != nil {
		return err
	}
	params["branch"] = branch
	if base != "" {
		params["base"] = base
	}
	if name != "" {
		params["name"] = name
	}
	if agent != "" {
		command, err := resolveAgentCommand(agent)
		if err != nil {
			return err
		}
		params["command"] = []string{command}
	}

	t, err := dialHost(host)
	if err != nil {
		return err
	}
	// A clone on another machine can take minutes, and the daemon there
	// bounds it at four, so the call waits a little longer than that.
	raw, err := t.client.CallWithTimeout("new-worktree", params, 5*time.Minute)
	t.Close()
	if err != nil {
		return reportVerbError(explainHostedVerb(t, "new-worktree", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Session       string `json:"session"`
		Branch        string `json:"branch"`
		Path          string `json:"path"`
		RepoRoot      string `json:"repo_root"`
		CreatedBranch bool   `json:"created_branch"`
		Cloned        bool   `json:"cloned"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.Cloned {
		fmt.Printf("Cloned the repository to %s%s.\n", res.RepoRoot, t.on())
	}
	verb := "Checked out"
	if res.CreatedBranch {
		verb = "Created"
	}
	fmt.Printf("%s branch %s in %s%s.\n", verb, res.Branch, res.Path, t.on())
	fmt.Printf("Created session '%s'%s.\n", res.Session, t.on())
	if detach {
		if host != "" {
			fmt.Printf("Attach with 'tuios attach --host %s %s'.\n", host, res.Session)
			return nil
		}
		fmt.Printf("Attach with 'tuios attach %s'.\n", res.Session)
		return nil
	}
	return runDaemonSessionOn(t.host, res.Session, false)
}

// listWorktrees calls list-worktrees on this machine and decodes the rows.
func listWorktrees(repo, group string, changes bool) ([]worktreeRow, error) {
	return listWorktreesOn("", repo, group, changes)
}

// listWorktreesOn calls list-worktrees on host, "" for this machine.
func listWorktreesOn(host, repo, group string, changes bool) ([]worktreeRow, error) {
	t, err := dialHost(host)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	params := map[string]any{"changes": changes}
	if repo != "" {
		params["repo"] = repo
	}
	if group != "" {
		params["group"] = group
	}
	raw, err := t.client.CallWithTimeout("list-worktrees", params, 60*time.Second)
	if err != nil {
		return nil, t.explain("list-worktrees", err)
	}
	var res struct {
		Worktrees []worktreeRow `json:"worktrees"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return res.Worktrees, nil
}

func runWorktreeList(host, repo, group string, jsonOutput bool) error {
	rows, err := listWorktreesOn(host, repo, group, true)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printJSON(rows)
	}
	if len(rows) == 0 {
		if host != "" && host != "local" {
			fmt.Printf("No worktree sessions on %s. Create one with 'tuios worktree new <branch> --host %s'.\n", host, host)
			return nil
		}
		fmt.Println("No worktree sessions. Create one with 'tuios worktree new <branch>'.")
		return nil
	}
	fmt.Println(renderWorktreeTable(rows))
	fmt.Printf("\n%d worktree session(s)\n", len(rows))
	return nil
}

// renderWorktreeTable draws the worktree listing in the same table the
// session listing uses, so the two read alike.
func renderWorktreeTable(rows []worktreeRow) string {
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		status := "detached"
		if r.Attached {
			status = "attached"
		}
		if r.Gone {
			status = "gone"
		}
		changes := "-"
		if r.Changes != nil && *r.Changes >= 0 {
			changes = strconv.Itoa(*r.Changes)
			if r.Ahead != nil && *r.Ahead > 0 {
				changes += fmt.Sprintf(" +%d commits", *r.Ahead)
			}
		}
		prompt := "-"
		switch r.PromptStatus {
		case session.PromptPending:
			prompt = "pending"
		case session.PromptSent:
			prompt = "sent"
		case session.PromptNotSent:
			prompt = "not sent"
		case session.PromptStalled:
			prompt = "stalled"
		case session.PromptHeld:
			prompt = "held: look at the pane"
		}
		cells = append(cells, []string{r.Session, r.Repo, r.Branch, orNone(r.State), changes, prompt, status})
	}
	return renderTable([]string{"SESSION", "REPO", "BRANCH", "AGENT", "CHANGES", "PROMPT", "STATUS"}, cells)
}

// renderTable draws a listing with the border and colours of the session
// table, for any set of columns.
func renderTable(headers []string, rows [][]string) string {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			if col == 0 {
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			}
			return base
		}).Render()
}

func runWorktreeRemove(target string, stash, force, keepSession, jsonOutput bool) error {
	host, name := splitHostSession(target)
	t, err := dialHost(host)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.CallWithTimeout("remove-worktree", map[string]any{
		"session":      name,
		"stash":        stash,
		"force":        force,
		"keep_session": keepSession,
	}, 60*time.Second)
	if err != nil {
		return reportVerbError(t.explain("remove-worktree", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res removedWorktree
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(res.sentences())
	return nil
}

// removedWorktree is the result of remove-worktree as the CLI reads it.
type removedWorktree struct {
	Session       string `json:"session"`
	Branch        string `json:"branch"`
	Path          string `json:"path"`
	Changes       int    `json:"changes"`
	Stashed       bool   `json:"stashed"`
	StashMessage  string `json:"stash_message"`
	Discarded     bool   `json:"discarded"`
	SessionKilled bool   `json:"session_killed"`
	Gone          bool   `json:"gone"`
	Note          string `json:"note"`
}

// sentences says what was removed, what was kept, and where the changes went.
func (r removedWorktree) sentences() string {
	var b strings.Builder
	if r.Gone {
		fmt.Fprintf(&b, "%s\n", r.Note)
	} else {
		fmt.Fprintf(&b, "Removed worktree %s. Branch %s is kept.\n", r.Path, r.Branch)
	}
	switch {
	case r.Stashed:
		fmt.Fprintf(&b, "%d uncommitted %s %s in git stash as '%s'.\n", r.Changes, pluralWord(r.Changes, "change", "changes"), pluralWord(r.Changes, "is", "are"), r.StashMessage)
	case r.Discarded:
		fmt.Fprintf(&b, "%d uncommitted %s %s discarded.\n", r.Changes, pluralWord(r.Changes, "change", "changes"), pluralWord(r.Changes, "was", "were"))
	}
	if r.SessionKilled {
		fmt.Fprintf(&b, "Killed session '%s'.", r.Session)
	} else {
		fmt.Fprintf(&b, "Session '%s' is still running.", r.Session)
	}
	return b.String()
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func runWorktreeDiff(name string, stat bool) error {
	if host, session := splitHostSession(name); host != "" {
		return &diagnosticError{
			What:  fmt.Sprintf("worktree diff reads the worktree's files, and %s is on %s.", session, host),
			Cause: "the diff is made by git on this machine.",
			Fix:   fmt.Sprintf("run 'tuios worktree pull %s' to bring its work here and diff it, or 'tuios worktree diff %s' on %s.", name, session, host),
		}
	}
	rows, err := listWorktrees("", "", false)
	if err != nil {
		return err
	}
	var row *worktreeRow
	for i := range rows {
		if rows[i].Session == name {
			row = &rows[i]
		}
	}
	if row == nil {
		return &diagnosticError{
			What:  fmt.Sprintf("session %q is not a worktree session.", name),
			Cause: "it is not in the worktree listing.",
			Fix:   "run 'tuios worktree ls'.",
		}
	}
	if row.Gone {
		return &diagnosticError{
			What:  fmt.Sprintf("the worktree of %q is gone.", name),
			Cause: row.Path + " no longer exists.",
		}
	}
	if row.Base != "" {
		log, err := worktree.Log(row.Path, row.Base)
		if err != nil {
			return err
		}
		if log = strings.TrimSpace(log); log != "" {
			fmt.Printf("commits on %s since %s:\n%s\n\n", row.Branch, row.Base, log)
		}
	}
	diff, err := worktree.Diff(row.Path, stat)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Printf("%s has no uncommitted changes.\n", row.Session)
		return nil
	}
	fmt.Print(diff)
	if !strings.HasSuffix(diff, "\n") {
		fmt.Println()
	}
	return nil
}

// fanOptions is what `tuios fan` sends.
type fanOptions struct {
	// host is the machine to fan out on, "" for this one.
	host    string
	clone   bool
	count   int
	agents  []string
	prompt  string
	prompts []string
	env     map[string]string
	// explicitEnv says the person passed --env, so a daemon that cannot take
	// env is an error rather than a reason to send without it.
	explicitEnv      bool
	repo, base, name string
	// grants is --grants, nil when it was not passed.
	grants []string
}

// fanCallerEnv builds the environment fan sends: PATH always, so the daemon
// finds the agents where this shell does, and each --env: NAME takes this
// process's value, NAME=VALUE sets one. A NAME this process does not have is
// an error, not an empty variable.
func fanCallerEnv(flags []string, getenv func(string) string, environ []string) (map[string]string, error) {
	env := map[string]string{}
	if path := getenv("PATH"); path != "" {
		env["PATH"] = path
	}
	have := map[string]bool{}
	for _, kv := range environ {
		if k, _, ok := strings.Cut(kv, "="); ok {
			have[k] = true
		}
	}
	for _, f := range flags {
		if k, v, ok := strings.Cut(f, "="); ok {
			env[k] = v
			continue
		}
		if !have[f] {
			return nil, fmt.Errorf("--env %s: this shell has no variable %s. Pass --env %s=VALUE to set one", f, f, f)
		}
		env[f] = getenv(f)
	}
	return env, nil
}

func runFan(o fanOptions, wait, jsonOutput bool) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	if o.host != "" && o.host != "local" {
		// The daemon on another machine refuses env from a link, and this
		// machine's PATH means nothing there: the host looks the agents up
		// on its own.
		if o.explicitEnv {
			return errors.New("--env passes this shell's variables to agents on this machine, and a call to another machine may not carry any. Set them in the host's environment instead")
		}
		o.env = nil
	}
	params, err := repoParams(o.host, o.repo, o.clone)
	if err != nil {
		return err
	}
	if o.count > 0 {
		params["count"] = o.count
	}
	// One agent goes as agent, which every daemon takes; several as agents.
	if len(o.agents) == 1 {
		params["agent"] = o.agents[0]
	} else {
		params["agents"] = o.agents
	}
	if len(o.prompts) > 0 {
		params["prompts"] = o.prompts
	} else {
		params["prompt"] = o.prompt
	}
	if len(o.env) > 0 {
		params["env"] = o.env
	}
	if o.base != "" {
		params["base"] = o.base
	}
	if o.name != "" {
		params["name"] = o.name
	}
	if len(o.grants) > 0 {
		params["grants"] = o.grants
	}
	t, err := dialHost(o.host)
	if err != nil {
		return err
	}
	raw, err := t.client.CallWithTimeout("fan", params, 5*time.Minute)
	// A daemon from before env refuses it. The PATH the CLI sends on its own
	// is a convenience, so the call is made again without it, as it always
	// was; an --env the person asked for is not dropped quietly.
	var callErr *session.VerbCallError
	if err != nil && !o.explicitEnv && errors.As(err, &callErr) && callErr.Code == session.ErrVerbInvalidParams &&
		callErr.Hint != nil && callErr.Hint.Param == "env" && params["env"] != nil {
		delete(params, "env")
		raw, err = t.client.CallWithTimeout("fan", params, 5*time.Minute)
	}
	t.Close()
	if err != nil {
		return reportVerbError(explainHostedVerb(t, "fan", err), jsonOutput)
	}
	raw = t.result(raw)
	var res struct {
		Group    string `json:"group"`
		Agent    string `json:"agent"`
		Command  string `json:"command"`
		Sessions []struct {
			Session string `json:"session"`
			Branch  string `json:"branch"`
			Path    string `json:"path"`
			Command string `json:"command"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !jsonOutput {
		fmt.Printf("Started %d %s on %s%s. Each prompt is sent when its agent is ready.\n", len(res.Sessions), pluralWord(len(res.Sessions), "agent", "agents"), res.Group, t.on())
		for _, s := range res.Sessions {
			line := fmt.Sprintf("  %s  %s  %s", s.Session, s.Branch, s.Path)
			if len(o.agents) > 1 && s.Command != "" {
				line += "  " + s.Command
			}
			fmt.Println(line)
		}
		if t.host != "" {
			fmt.Printf("Watch them with 'tuios worktree ls --host %s --group %s'. Keep one with 'tuios fan keep %s:<session>', or bring its work here with 'tuios worktree pull %s:<session>'.\n", t.host, res.Group, t.host, t.host)
		} else {
			fmt.Printf("Watch them with 'tuios worktree ls --group %s'. Keep one with 'tuios fan keep <session>'.\n", res.Group)
		}
	}
	if !wait {
		if jsonOutput {
			return printVerbResult(raw, true)
		}
		return nil
	}
	rows, err := waitFanPrompts(t.host, res.Group)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printJSON(rows)
	}
	for _, r := range rows {
		switch r.PromptStatus {
		case session.PromptSent:
			fmt.Printf("%s: prompt sent.\n", r.Session)
		case session.PromptStalled:
			fmt.Printf("%s: prompt typed, not taken. %s\n", r.Session, r.PromptNote)
		default:
			fmt.Printf("%s: prompt not sent. %s\n", r.Session, r.PromptNote)
		}
	}
	return nil
}

// waitFanPrompts polls the group until no prompt is pending or held. The
// daemon's own wait bounds it: a waiting prompt turns into not_sent when the
// ready timeout ends, so this loop always finishes.
func waitFanPrompts(host, group string) ([]worktreeRow, error) {
	for {
		rows, err := listWorktreesOn(host, "", group, false)
		if err != nil {
			return nil, err
		}
		pending := false
		for _, r := range rows {
			if session.PromptWaiting(r.PromptStatus) {
				pending = true
			}
		}
		if !pending {
			return rows, nil
		}
		time.Sleep(time.Second)
	}
}

// fanKeepOutcome is one sibling of a fan keep, as the CLI reports it.
type fanKeepOutcome struct {
	Session string `json:"session"`
	Removed bool   `json:"removed"`
	Note    string `json:"note"`
}

// runFanKeep keeps one session of a fan with the daemon's keep-fan. A daemon
// from before the verb gets the loop the CLI ran before it, with the same
// output.
func runFanKeep(target string, stash, force, jsonOutput bool) error {
	host, winner := splitHostSession(target)
	t, err := dialHost(host)
	if err != nil {
		return err
	}
	raw, err := t.client.CallWithTimeout("keep-fan", map[string]any{"session": winner, "stash": stash, "force": force}, 5*time.Minute)
	t.Close()
	var call *session.VerbCallError
	if err != nil && errors.As(err, &call) && call.Code == session.ErrVerbUnknownVerb {
		return runFanKeepLoop(host, winner, stash, force, jsonOutput)
	}
	if err != nil {
		return reportVerbError(t.explain("keep-fan", err), jsonOutput)
	}
	var res struct {
		Kept    string            `json:"kept"`
		Branch  string            `json:"branch"`
		Group   string            `json:"group"`
		Left    int               `json:"left"`
		Removed []json.RawMessage `json:"removed"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	outcomes := make([]fanKeepOutcome, 0, len(res.Removed))
	for _, entry := range res.Removed {
		var head struct {
			Session string `json:"session"`
			Removed bool   `json:"removed"`
			Note    string `json:"note"`
			Code    string `json:"code"`
		}
		if err := json.Unmarshal(entry, &head); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		if !head.Removed {
			note := head.Note
			if head.Code == session.ErrVerbWorktreeDirty {
				note += " Pass --stash to keep the changes in git stash, or --force to discard them."
			}
			outcomes = append(outcomes, fanKeepOutcome{Session: head.Session, Note: note})
			continue
		}
		var removed removedWorktree
		if err := json.Unmarshal(entry, &removed); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		outcomes = append(outcomes, fanKeepOutcome{Session: head.Session, Removed: true, Note: removed.sentences()})
	}
	return reportFanKeep(t, res.Kept, res.Branch, res.Group, outcomes, res.Left, jsonOutput)
}

// reportFanKeep prints what a fan keep did, and exits 1 when a sibling was
// left in place.
func reportFanKeep(t *verbTarget, winner, branch, group string, outcomes []fanKeepOutcome, left int, jsonOutput bool) error {
	if jsonOutput {
		out := map[string]any{"kept": winner, "group": group, "siblings": outcomes, "left": left}
		if t.host != "" {
			out["host"] = t.host
		}
		return printJSON(out)
	}
	fmt.Printf("Kept %s on %s%s.\n", winner, branch, t.on())
	for _, o := range outcomes {
		fmt.Println(strings.TrimRight(o.Note, "\n"))
	}
	if left > 0 {
		return &statusError{code: 1}
	}
	return nil
}

// runFanKeepLoop is fan keep for a daemon without keep-fan: remove-worktree
// over every sibling but the kept one.
func runFanKeepLoop(host, winner string, stash, force, jsonOutput bool) error {
	rows, err := listWorktreesOn(host, "", "", false)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	var kept *worktreeRow
	for i := range rows {
		if rows[i].Session == winner {
			kept = &rows[i]
		}
	}
	if kept == nil {
		return reportVerbError(&diagnosticError{
			What:  fmt.Sprintf("session %q is not a worktree session.", winner),
			Cause: "it is not in the worktree listing.",
			Fix:   "run 'tuios worktree ls'.",
		}, jsonOutput)
	}
	if kept.Group == "" {
		return reportVerbError(&diagnosticError{
			What:  fmt.Sprintf("session %q is not part of a fan-out.", winner),
			Cause: "it has no siblings to remove.",
			Fix:   "run 'tuios worktree rm <session>' to remove one worktree.",
		}, jsonOutput)
	}

	t, err := dialHost(host)
	if err != nil {
		return err
	}
	defer t.Close()
	client := t.client

	var outcomes []fanKeepOutcome
	left := 0
	for _, r := range rows {
		if r.Group != kept.Group || r.Repo != kept.Repo || r.Session == winner {
			continue
		}
		raw, err := client.CallWithTimeout("remove-worktree", map[string]any{
			"session": r.Session, "stash": stash, "force": force,
		}, 60*time.Second)
		if err != nil {
			left++
			outcomes = append(outcomes, fanKeepOutcome{Session: r.Session, Note: t.explain("remove-worktree", err).Error()})
			continue
		}
		var res removedWorktree
		if err := json.Unmarshal(raw, &res); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		outcomes = append(outcomes, fanKeepOutcome{Session: r.Session, Removed: true, Note: res.sentences()})
	}
	return reportFanKeep(t, winner, kept.Branch, kept.Group, outcomes, left, jsonOutput)
}

// completeWorktreeSessions offers the worktree session names to the shell.
func completeWorktreeSessions(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	rows, err := listWorktrees("", "", false)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Session)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
