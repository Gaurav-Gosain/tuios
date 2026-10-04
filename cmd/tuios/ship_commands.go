package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newShipCommand builds `tuios ship`: commit an agent's work in its worktree,
// merge the branch into its base, push it, and open a pull request.
func newShipCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ship",
		Short: "Commit, merge, push and open a pull request for a pane's worktree",
		Long: `Take an agent's work from its worktree to a merged change.

'tuios ship commit' commits every change in the pane's work tree on its
branch. 'tuios ship merge' merges that branch into its base in the main
checkout. 'tuios ship push' pushes the branch. 'tuios ship pr' pushes it and
opens a pull request with the gh CLI. 'tuios ship status' shows the pull
request and its checks.

git runs with your own identity, signing and hooks. tuios adds nothing to a
commit message and never forces a merge or a push. A merge that conflicts is
undone, and the files that conflict are listed.

A push and a pull request send work off this machine. The command shows what
it will send and asks you first. Use --yes to skip the question. When the
command runs inside a tuios pane, you must also allow it in the Inbox.`,
		Example: `  tuios ship commit -w build -m 'Add a retry to the client'
  tuios ship merge -s api-feat-retry
  tuios ship pr -s api-feat-retry --draft
  tuios ship status -s api-feat-retry --refresh`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newShipCommitCommand(), newShipMergeCommand(), newShipPushCommand(), newShipPRCommand(), newShipStatusCommand())
	return cmd
}

// shipFlags are the flags every ship subcommand takes.
type shipFlags struct {
	session, window string
	json            bool
}

func (f *shipFlags) add(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.session, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&f.window, "window", "w", "", "The pane, by name or id (default: the focused pane)")
	cmd.Flags().BoolVar(&f.json, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
}

// dialShip connects for a ship verb, on this machine only.
func dialShip(f shipFlags) (*verbTarget, error) {
	host, sess, _, err := resolveTarget(f.session, f.window)
	if err != nil {
		return nil, err
	}
	if host != "" {
		return nil, &diagnosticError{
			What:  fmt.Sprintf("ship works on this machine's sessions, and %s:%s is on %s.", host, sess, host),
			Cause: "shipping a session on a linked machine is not supported yet.",
			Fix:   fmt.Sprintf("attach to %s and run 'tuios ship' there, or bring the work here with 'tuios worktree pull %s:%s'.", host, host, sess),
		}
	}
	return dialTarget(f.session, f.window)
}

// shipParams is the parameter map with the target's session and window.
func shipParams(t *verbTarget, params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	params["window"] = ""
	params = t.params(params)
	if t.window == "" {
		delete(params, "window")
	}
	return params
}

// shipFailed reports a failed ship verb: the daemon's hint as text, or under
// --json an object with the code and the files a conflict or a dirty
// checkout names.
func shipFailed(t *verbTarget, verb string, err error, jsonOutput bool) error {
	if !jsonOutput {
		return t.explain(verb, err)
	}
	out := map[string]any{"success": false, "error": err.Error()}
	if call, ok := errors.AsType[*session.VerbCallError](err); ok {
		out["error"] = call.Message
		out["code"] = call.Code
		if call.Hint != nil && len(call.Hint.Available) > 0 {
			out["files"] = call.Hint.Available
		}
	}
	outputJSON(out)
	os.Exit(1)
	return nil
}

// newShipCommitCommand builds `tuios ship commit`.
func newShipCommitCommand() *cobra.Command {
	var f shipFlags
	var message string
	var force bool
	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Commit every change in a pane's work tree on its branch",
		Long: `Stage every change in the pane's git work tree and commit it on the branch
checked out there. Untracked files are included. Ignored files are not.

git uses your own name, email, signing setup and hooks. The message is used
as you give it. Without -m, the message is the pane's last prompt, or the
line its last turn ended with. When there is neither, give -m.

The commit is refused while the pane's agent is working or waiting on a
prompt. Use --force to commit then.`,
		Example: `  tuios ship commit -w build -m 'Add a retry to the client'
  tuios ship commit -s api-feat-retry`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			t, err := dialShip(f)
			if err != nil {
				return err
			}
			defer t.Close()
			params := map[string]any{}
			if message != "" {
				params["message"] = message
			}
			if force {
				params["force"] = true
			}
			raw, err := t.client.CallWithTimeout("ship-commit", shipParams(t, params), 3*time.Minute)
			if err != nil {
				return shipFailed(t, "ship-commit", err, f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			var res struct {
				Branch  string `json:"branch"`
				Commit  string `json:"commit"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			subject, _, _ := strings.Cut(res.Message, "\n")
			fmt.Printf("Committed %s on %s: %s\n", shortHash(res.Commit), plainLine(res.Branch), plainLine(subject))
			return nil
		},
	}
	f.add(cmd)
	cmd.Flags().StringVarP(&message, "message", "m", "", "The commit message (default: the pane's last prompt or turn summary)")
	cmd.Flags().BoolVar(&force, "force", false, "Commit while the agent is working or waiting on a prompt")
	return cmd
}

// shortHash is the first seven characters of a commit hash.
func shortHash(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// mergeMode reads --squash and --ff-only.
func mergeMode(squash, ffOnly bool) (string, error) {
	switch {
	case squash && ffOnly:
		return "", errors.New("--squash and --ff-only cannot be used together. Use one of them")
	case squash:
		return "squash", nil
	case ffOnly:
		return "ff-only", nil
	}
	return "", nil
}

// shipMergeResult is what ship-merge returns, and keep-fan in its merge field.
type shipMergeResult struct {
	Session     string `json:"session"`
	Branch      string `json:"branch"`
	Into        string `json:"into"`
	RepoRoot    string `json:"repo_root"`
	Mode        string `json:"mode"`
	After       string `json:"after"`
	Commits     int    `json:"commits"`
	FastForward bool   `json:"fast_forward"`
	UpToDate    bool   `json:"up_to_date"`
	Uncommitted int    `json:"uncommitted"`
}

// sentences says what a merge did.
func (r shipMergeResult) sentences() string {
	var b strings.Builder
	switch {
	case r.UpToDate:
		fmt.Fprintf(&b, "%s already has every commit of %s. Nothing was merged.\n", plainLine(r.Into), plainLine(r.Branch))
	case r.FastForward:
		fmt.Fprintf(&b, "Merged %s into %s in %s: fast-forward, %d %s, now at %s.\n", plainLine(r.Branch), plainLine(r.Into), plainLine(r.RepoRoot), r.Commits, pluralWord(r.Commits, "commit", "commits"), shortHash(r.After))
	default:
		fmt.Fprintf(&b, "Merged %s into %s in %s: %s, %d %s, now at %s.\n", plainLine(r.Branch), plainLine(r.Into), plainLine(r.RepoRoot), plainLine(r.Mode), r.Commits, pluralWord(r.Commits, "commit", "commits"), shortHash(r.After))
	}
	if r.Uncommitted > 0 {
		fmt.Fprintf(&b, "The worktree still has %d uncommitted %s, which were not merged. Commit them with 'tuios ship commit -s %s'.\n", r.Uncommitted, pluralWord(r.Uncommitted, "change", "changes"), plainLine(r.Session))
	}
	return b.String()
}

// newShipMergeCommand builds `tuios ship merge`.
func newShipMergeCommand() *cobra.Command {
	var f shipFlags
	var into, message string
	var squash, ffOnly bool
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge a worktree's branch into its base in the main checkout",
		Long: `Merge the branch of the pane's worktree into its base branch, in the
repository's main checkout. The base is the branch the worktree was made from.
Use --into to name another branch. The main checkout must have that branch
checked out and no uncommitted change.

By default git fast-forwards when it can and makes a merge commit when it
cannot. --squash makes one commit. --ff-only refuses a branch that cannot be
fast-forwarded.

When the merge conflicts, it is undone and the main checkout is as it was.
The command lists the files that conflict. Uncommitted changes in the
worktree are not merged. Commit them first with 'tuios ship commit'.`,
		Example: `  tuios ship merge -s api-feat-retry
  tuios ship merge -s api-feat-retry --squash -m 'Add a retry to the client'
  tuios ship merge -s api-feat-retry --into release --ff-only`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			mode, err := mergeMode(squash, ffOnly)
			if err != nil {
				return err
			}
			t, err := dialShip(f)
			if err != nil {
				return err
			}
			defer t.Close()
			params := map[string]any{}
			if into != "" {
				params["into"] = into
			}
			if mode != "" {
				params["mode"] = mode
			}
			if message != "" {
				params["message"] = message
			}
			raw, err := t.client.CallWithTimeout("ship-merge", shipParams(t, params), 3*time.Minute)
			if err != nil {
				return shipFailed(t, "ship-merge", err, f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			var res shipMergeResult
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Print(res.sentences())
			return nil
		},
	}
	f.add(cmd)
	cmd.Flags().StringVar(&into, "into", "", "The branch to merge into (default: the base the worktree was made from)")
	cmd.Flags().BoolVar(&squash, "squash", false, "Make one commit with the branch's change")
	cmd.Flags().BoolVar(&ffOnly, "ff-only", false, "Only fast-forward, and refuse a branch that cannot be")
	cmd.Flags().StringVarP(&message, "message", "m", "", "The message of the merge or squash commit (default: git's own)")
	return cmd
}

// outboundFlags are the flags of ship push and ship pr.
type outboundFlags struct {
	shipFlags
	remote  string
	yes     bool
	request string
	wait    time.Duration
}

func (f *outboundFlags) add(cmd *cobra.Command) {
	f.shipFlags.add(cmd)
	cmd.Flags().StringVar(&f.remote, "remote", "", "The remote to push to (default: the one the branch follows, else the only one, else origin)")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "Do not ask before sending")
	cmd.Flags().StringVar(&f.request, "request", "", "Wait again for the answer to a question an earlier call put in the Inbox")
	cmd.Flags().DurationVar(&f.wait, "wait", 2*time.Minute, "How long to wait for an answer in the Inbox, when one is needed")
}

// shipConfirm is how an outbound ship command asks before it sends.
type shipConfirm struct {
	yes bool
	in  io.Reader
	tty bool
	out io.Writer
}

func newShipConfirm(yes bool) shipConfirm {
	return shipConfirm{yes: yes, in: os.Stdin, tty: term.IsTerminal(int(os.Stdin.Fd())), out: os.Stderr}
}

// callOutbound calls ship-push or ship-pr. The first call carries no token,
// so the daemon answers confirm_required with what it would send. That is
// printed, and the call is made again with the token when the person says
// yes or passed --yes. A change between the two calls is refused by the
// daemon and reported, never retried.
func callOutbound(t *verbTarget, verb, action string, params map[string]any, f outboundFlags, c shipConfirm) (json.RawMessage, error) {
	if f.remote != "" {
		params["remote"] = f.remote
	}
	if f.wait > 0 {
		params["wait"] = f.wait.Milliseconds()
	}
	if f.request != "" {
		params["request_id"] = f.request
	}
	params = shipParams(t, params)
	timeout := f.wait + 4*time.Minute
	raw, err := t.client.CallWithTimeout(verb, params, timeout)
	if err == nil {
		// The daemon asked for no confirmation.
		return raw, nil
	}
	callErr, ok := errors.AsType[*session.VerbCallError](err)
	if !ok || callErr.Code != session.ErrVerbConfirmRequired || callErr.Hint == nil || callErr.Hint.Confirm == "" {
		// Nothing to confirm: a refusal before anything would be sent.
		return nil, err
	}
	fmt.Fprintf(c.out, "tuios ship will:\n")
	for _, line := range callErr.Hint.Available {
		fmt.Fprintln(c.out, "  "+plainLine(line))
	}
	switch {
	case c.yes:
	case c.tty:
		fmt.Fprintf(c.out, "%s? [y/N] ", action)
		line, _ := bufio.NewReader(c.in).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return nil, &diagnosticError{What: "Nothing was sent.", Cause: "you did not confirm."}
		}
	default:
		return nil, &diagnosticError{
			What:  "Nothing was sent.",
			Cause: "a push sends work off this machine only when someone confirms it, and this is not a terminal to ask at.",
			Fix:   "check the list above, then run the command again with --yes.",
		}
	}
	params["confirm"] = callErr.Hint.Confirm
	return t.client.CallWithTimeout(verb, params, timeout)
}

// newShipPushCommand builds `tuios ship push`.
func newShipPushCommand() *cobra.Command {
	var f outboundFlags
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push a pane's branch to its remote",
		Long: `Push the branch checked out in the pane's work tree to a remote, and set it
as the branch's upstream. The remote is the one the branch follows, else the
only one, else origin. Use --remote to name another.

The command shows what it will push and asks you first. Use --yes to skip the
question. A push is never forced. When the remote branch moved on, git
refuses the push. Pull or rebase in the worktree, then push again.

When the command runs inside a tuios pane, the push also needs your yes in the
Inbox. The command waits for it (--wait).`,
		Example: `  tuios ship push -s api-feat-retry
  tuios ship push -s api-feat-retry --remote fork --yes`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			t, err := dialShip(f.shipFlags)
			if err != nil {
				return err
			}
			defer t.Close()
			raw, err := callOutbound(t, "ship-push", "Push", map[string]any{}, f, newShipConfirm(f.yes))
			if err != nil {
				return shipFailed(t, "ship-push", err, f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			var res struct {
				Branch string `json:"branch"`
				Commit string `json:"commit"`
				Remote string `json:"remote"`
				URL    string `json:"url"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("Pushed %s at %s to %s (%s).\n", plainLine(res.Branch), shortHash(res.Commit), plainLine(res.Remote), plainLine(res.URL))
			return nil
		},
	}
	f.add(cmd)
	return cmd
}

// newShipPRCommand builds `tuios ship pr`.
func newShipPRCommand() *cobra.Command {
	var f outboundFlags
	var base, title, body string
	var draft bool
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Push a pane's branch and open a pull request with gh",
		Long: `Push the branch checked out in the pane's work tree, and open a pull request
for it with the gh CLI. tuios uses your own gh login and holds no GitHub
token. Install gh from https://cli.github.com and run 'gh auth login' first.

The pull request merges into the base the worktree was made from. Use --base
to name another. With no --title, gh fills the title and the body from the
commits. When the branch already has an open pull request, the push updates
it and no new one is opened.

The command shows what it will do and asks you first. Use --yes to skip the
question. When the command runs inside a tuios pane, it also needs your yes
in the Inbox.

The rail shows the pull request and its checks on the agent's row, and
'tuios worktree ls' lists it.`,
		Example: `  tuios ship pr -s api-feat-retry
  tuios ship pr -s api-feat-retry --draft --title 'Add a retry' --body 'Retries with backoff.'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if body != "" && title == "" {
				return errors.New("--body needs --title. Give both, or neither for gh to fill them from the commits")
			}
			t, err := dialShip(f.shipFlags)
			if err != nil {
				return err
			}
			defer t.Close()
			params := map[string]any{}
			if base != "" {
				params["base"] = base
			}
			if title != "" {
				params["title"] = title
				params["body"] = body
			}
			if draft {
				params["draft"] = true
			}
			raw, err := callOutbound(t, "ship-pr", "Push and open the pull request", params, f, newShipConfirm(f.yes))
			if err != nil {
				return shipFailed(t, "ship-pr", err, f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			var res struct {
				Branch  string   `json:"branch"`
				Commit  string   `json:"commit"`
				Remote  string   `json:"remote"`
				Created bool     `json:"created"`
				PR      *ghpr.PR `json:"pr"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			fmt.Printf("Pushed %s at %s to %s.\n", plainLine(res.Branch), shortHash(res.Commit), plainLine(res.Remote))
			switch {
			case res.PR == nil:
				fmt.Println("gh opened the pull request, and tuios could not read it back. Run 'tuios ship status --refresh' to read it.")
			case res.Created:
				fmt.Printf("Opened pull request #%d: %s\n", res.PR.Number, plainLine(res.PR.URL))
			default:
				fmt.Printf("Pull request #%d was already open, and now has the new commits: %s\n", res.PR.Number, plainLine(res.PR.URL))
			}
			return nil
		},
	}
	f.add(cmd)
	cmd.Flags().StringVar(&base, "base", "", "The branch the pull request merges into (default: the worktree's base, else the repository's default branch)")
	cmd.Flags().StringVar(&title, "title", "", "The title (default: gh fills it from the commits)")
	cmd.Flags().StringVar(&body, "body", "", "The body. Needs --title")
	cmd.Flags().BoolVar(&draft, "draft", false, "Open it as a draft")
	return cmd
}

// newShipStatusCommand builds `tuios ship status`.
func newShipStatusCommand() *cobra.Command {
	var f shipFlags
	var refresh bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the pull request of a pane's branch and its checks",
		Long: `Show the pull request of the branch in the pane's work tree, and its checks,
as tuios last read them from gh. Use --refresh to ask gh now. tuios also
reads an open pull request again every minute while a client is attached.`,
		Example: `  tuios ship status -s api-feat-retry
  tuios ship status -s api-feat-retry --refresh --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			t, err := dialShip(f)
			if err != nil {
				return err
			}
			defer t.Close()
			params := map[string]any{}
			if refresh {
				params["refresh"] = true
			}
			raw, err := t.client.CallWithTimeout("ship-status", shipParams(t, params), 2*time.Minute)
			if err != nil {
				return shipFailed(t, "ship-status", err, f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			var res struct {
				Branch string   `json:"branch"`
				GH     bool     `json:"gh"`
				PR     *ghpr.PR `json:"pr"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return fmt.Errorf("failed to parse response: %w", err)
			}
			switch {
			case res.PR != nil:
				fmt.Printf("%s: %s\n", plainLine(res.Branch), plainLine(res.PR.Badge()))
				fmt.Println(plainLine(res.PR.URL))
				if res.PR.Checks != "" {
					fmt.Printf("Checks: %d passed, %d failed, %d pending.\n", res.PR.Passed, res.PR.Failed, res.PR.Pending)
				}
			case !res.GH:
				fmt.Printf("%s: no pull request is known. The gh CLI is not installed, so tuios cannot read one.\n", plainLine(res.Branch))
			default:
				fmt.Printf("%s: no pull request is known. Open one with 'tuios ship pr', or use --refresh to ask gh.\n", plainLine(res.Branch))
			}
			return nil
		},
	}
	f.add(cmd)
	cmd.Flags().BoolVar(&refresh, "refresh", false, "Ask gh now")
	return cmd
}
