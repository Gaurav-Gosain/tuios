package main

import (
	"encoding/json"
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
// same refusals for the same reasons. What lives here is the argument shapes,
// the sentences, and the one composition the daemon does not do: fan keep,
// which is remove-worktree over every sibling but one.

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

	var newRepo, newBase, newName, newAgent string
	var newDetach, newJSON bool
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
harness manifests describe.`,
		Example: `  # A new branch from HEAD, attached
  tuios worktree new feat/retry

  # A branch from main, headless, running Claude Code
  tuios worktree new feat/retry --base main --agent claude --detach

  # A worktree of another repository
  tuios worktree new fix/typo --repo /src/api`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runWorktreeNew(args[0], newRepo, newBase, newName, newAgent, newDetach, newJSON)
		},
	}
	newCmd.Flags().StringVar(&newRepo, "repo", "", "A directory inside the repository (default: the current directory)")
	newCmd.Flags().StringVar(&newBase, "base", "", "Ref a new branch starts from (default: HEAD)")
	newCmd.Flags().StringVar(&newName, "name", "", "Session name (default: <repo>-<branch>)")
	newCmd.Flags().StringVar(&newAgent, "agent", "", "Start this agent CLI in the session instead of a shell")
	newCmd.Flags().BoolVarP(&newDetach, "detach", "d", false, "Create the session headless without attaching a client")
	newCmd.Flags().BoolVar(&newJSON, "json", false, "Output result as JSON")

	var lsRepo, lsGroup string
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
  tuios worktree ls --group fan/add-retry --json`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runWorktreeList(lsRepo, lsGroup, lsJSON)
		},
	}
	lsCmd.Flags().StringVar(&lsRepo, "repo", "", "Only worktrees of this repository, by name")
	lsCmd.Flags().StringVar(&lsGroup, "group", "", "Only the sessions of one fan-out, by its branch stem")
	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "Output as JSON")

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
repository.`,
		Example: `  tuios worktree rm api-feat-retry
  tuios worktree rm api-feat-retry --stash
  tuios worktree rm api-feat-retry --force`,
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

	worktreeCmd.AddCommand(newCmd, lsCmd, rmCmd, diffCmd)
	return worktreeCmd
}

// newFanCommand builds `tuios fan` and `tuios fan keep`.
func newFanCommand() *cobra.Command {
	var fanAgent, fanRepo, fanBase, fanName string
	var fanWait, fanJSON bool
	fanCmd := &cobra.Command{
		Use:   "fan <count> <prompt>",
		Short: "Fan one prompt out across several agents, each in its own worktree",
		Long: `Start the same prompt in several agents at once, each in a git worktree
of its own, and watch them side by side.

tuios creates <count> worktrees of the repository you are in, starts the
agent named by --agent in each, and types the prompt into each agent once
it is ready to read. The command prints the sessions and returns. The rail
shows them under the repository. 'tuios worktree ls --group' shows which
prompts were sent.

The branches are a stem, then stem-2, stem-3 and so on. The stem is 'fan/'
and the first words of the prompt, or --name.

When one result is the one you want, 'tuios fan keep <session>' removes the
others. It refuses to discard their uncommitted work unless you say so.`,
		Example: `  # Three Claude Code agents on the same task
  tuios fan 3 --agent claude 'Add a retry with backoff to the HTTP client.'

  # From a branch, with a stem you chose, waiting until every prompt is sent
  tuios fan 2 --agent codex --base main --name try/retry --wait 'Add a retry.'

  # Keep the second one, and stash what the others did
  tuios fan keep api-fan-add-a-retry-with-2 --stash`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			count, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("count must be a number, got %q", args[0])
			}
			return runFan(count, fanAgent, args[1], fanRepo, fanBase, fanName, fanWait, fanJSON)
		},
	}
	fanCmd.Flags().StringVar(&fanAgent, "agent", "", "The agent CLI to run in every session: claude, codex, gemini (required)")
	fanCmd.Flags().StringVar(&fanRepo, "repo", "", "A directory inside the repository (default: the current directory)")
	fanCmd.Flags().StringVar(&fanBase, "base", "", "Ref every branch starts from (default: HEAD)")
	fanCmd.Flags().StringVar(&fanName, "name", "", "Branch stem (default: fan/ and the first words of the prompt)")
	fanCmd.Flags().BoolVar(&fanWait, "wait", false, "Return only when every prompt is sent or given up on")
	fanCmd.Flags().BoolVar(&fanJSON, "json", false, "Output result as JSON")
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
them. Branches are never deleted.`,
		Example: `  tuios fan keep api-fan-add-a-retry-with-2
  tuios fan keep api-fan-add-a-retry-with-2 --stash`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorktreeSessions,
		RunE: func(_ *cobra.Command, args []string) error {
			return runFanKeep(args[0], keepStash, keepForce, keepJSON)
		},
	}
	keepCmd.Flags().BoolVar(&keepStash, "stash", false, "Keep every sibling's uncommitted changes in git stash before removing it")
	keepCmd.Flags().BoolVar(&keepForce, "force", false, "Discard every sibling's uncommitted changes")
	keepCmd.Flags().BoolVar(&keepJSON, "json", false, "Output result as JSON")

	fanCmd.AddCommand(keepCmd)
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

func runWorktreeNew(branch, repo, base, name, agent string, detach, jsonOutput bool) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	repo, err := repoArg(repo)
	if err != nil {
		return err
	}
	params := map[string]any{"repo": repo, "branch": branch}
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

	client, err := dialVerb()
	if err != nil {
		return err
	}
	raw, err := client.CallWithTimeout("new-worktree", params, 60*time.Second)
	_ = client.Close()
	if err != nil {
		return reportVerbError(explainVerbError("new-worktree", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Session       string `json:"session"`
		Branch        string `json:"branch"`
		Path          string `json:"path"`
		CreatedBranch bool   `json:"created_branch"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	verb := "Checked out"
	if res.CreatedBranch {
		verb = "Created"
	}
	fmt.Printf("%s branch %s in %s.\n", verb, res.Branch, res.Path)
	fmt.Printf("Created session '%s'.\n", res.Session)
	if detach {
		fmt.Printf("Attach with 'tuios attach %s'.\n", res.Session)
		return nil
	}
	return runDaemonSession(res.Session, false)
}

// listWorktrees calls list-worktrees and decodes the rows.
func listWorktrees(repo, group string, changes bool) ([]worktreeRow, error) {
	client, err := dialVerb()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"changes": changes}
	if repo != "" {
		params["repo"] = repo
	}
	if group != "" {
		params["group"] = group
	}
	raw, err := client.CallWithTimeout("list-worktrees", params, 60*time.Second)
	if err != nil {
		return nil, explainVerbError("list-worktrees", err)
	}
	var res struct {
		Worktrees []worktreeRow `json:"worktrees"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return res.Worktrees, nil
}

func runWorktreeList(repo, group string, jsonOutput bool) error {
	rows, err := listWorktrees(repo, group, true)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printJSON(rows)
	}
	if len(rows) == 0 {
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

func runWorktreeRemove(name string, stash, force, keepSession, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.CallWithTimeout("remove-worktree", map[string]any{
		"session":      name,
		"stash":        stash,
		"force":        force,
		"keep_session": keepSession,
	}, 60*time.Second)
	if err != nil {
		return reportVerbError(explainVerbError("remove-worktree", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
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

func runFan(count int, agent, prompt, repo, base, name string, wait, jsonOutput bool) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	repo, err := repoArg(repo)
	if err != nil {
		return err
	}
	params := map[string]any{"count": count, "agent": agent, "prompt": prompt, "repo": repo}
	if base != "" {
		params["base"] = base
	}
	if name != "" {
		params["name"] = name
	}
	client, err := dialVerb()
	if err != nil {
		return err
	}
	raw, err := client.CallWithTimeout("fan", params, 5*time.Minute)
	_ = client.Close()
	if err != nil {
		return reportVerbError(explainVerbError("fan", err), jsonOutput)
	}
	var res struct {
		Group    string `json:"group"`
		Agent    string `json:"agent"`
		Command  string `json:"command"`
		Sessions []struct {
			Session string `json:"session"`
			Branch  string `json:"branch"`
			Path    string `json:"path"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !jsonOutput {
		fmt.Printf("Started %d %s on %s. Each prompt is sent when its agent is ready.\n", len(res.Sessions), pluralWord(len(res.Sessions), "agent", "agents"), res.Group)
		for _, s := range res.Sessions {
			fmt.Printf("  %s  %s  %s\n", s.Session, s.Branch, s.Path)
		}
		fmt.Printf("Watch them with 'tuios worktree ls --group %s'. Keep one with 'tuios fan keep <session>'.\n", res.Group)
	}
	if !wait {
		if jsonOutput {
			return printVerbResult(raw, true)
		}
		return nil
	}
	rows, err := waitFanPrompts(res.Group)
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
		default:
			fmt.Printf("%s: prompt not sent. %s\n", r.Session, r.PromptNote)
		}
	}
	return nil
}

// waitFanPrompts polls the group until no prompt is pending. The daemon's own
// wait bounds it: a pending prompt turns into not_sent when the ready timeout
// ends, so this loop always finishes.
func waitFanPrompts(group string) ([]worktreeRow, error) {
	for {
		rows, err := listWorktrees("", group, false)
		if err != nil {
			return nil, err
		}
		pending := false
		for _, r := range rows {
			if r.PromptStatus == session.PromptPending {
				pending = true
			}
		}
		if !pending {
			return rows, nil
		}
		time.Sleep(time.Second)
	}
}

func runFanKeep(winner string, stash, force, jsonOutput bool) error {
	rows, err := listWorktrees("", "", false)
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

	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	type outcome struct {
		Session string `json:"session"`
		Removed bool   `json:"removed"`
		Note    string `json:"note"`
	}
	var outcomes []outcome
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
			outcomes = append(outcomes, outcome{Session: r.Session, Note: explainVerbError("remove-worktree", err).Error()})
			continue
		}
		var res removedWorktree
		if err := json.Unmarshal(raw, &res); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		outcomes = append(outcomes, outcome{Session: r.Session, Removed: true, Note: res.sentences()})
	}

	if jsonOutput {
		return printJSON(map[string]any{"kept": winner, "group": kept.Group, "siblings": outcomes, "left": left})
	}
	fmt.Printf("Kept %s on %s.\n", winner, kept.Branch)
	for _, o := range outcomes {
		fmt.Println(strings.TrimRight(o.Note, "\n"))
	}
	if left > 0 {
		return &statusError{code: 1}
	}
	return nil
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
