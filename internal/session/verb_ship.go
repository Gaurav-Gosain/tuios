package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Shipping: from an agent's worktree to a merged change.
//
// ship-commit commits everything in a pane's work tree on its branch.
// ship-merge merges the worktree's branch into its base in the main checkout.
// ship-push sends the branch to a remote, and ship-pr also opens a pull
// request for it through the person's own gh CLI. ship-status reads the pull
// request the branch has, and its checks.
//
// The git work runs in the daemon with the daemon's environment, which is the
// person's: their identity, their signing setup and their hooks. tuios sets no
// identity and adds nothing to a message. Nothing is forced: a merge that
// conflicts is aborted and reported, and a push that is not a fast-forward is
// refused by git.
//
// Who may do what:
//
//   - ship-commit and ship-merge write files a pane's agent works on, as
//     restore-checkpoint does. They are scopeWrite, and a pane without admin
//     may name only a pane it could type into (typingRefusal).
//   - ship-push and ship-pr send work off this machine. Each first answers
//     confirm_required with what it would send and a token, and goes ahead
//     only when called again with that token. The token is a hash of the
//     branch, its commit, the remote and the pull request asked for, so what
//     goes out is what the caller looked at. A caller the daemon cannot count
//     as the person (one inside a pane, a restricted connection such as tuios
//     mcp, a link) also needs the person's yes: the call puts the question in
//     the Inbox and waits for it, as ask-human does. Only an attached client
//     can answer it there.
//   - ship-status reads, and is scopeRead.
//
// A pane on another machine is refused: its repository is there.

// Error codes the ship verbs raise, on top of the shared ones.
const (
	// ErrVerbNothingToCommit reports a work tree with no change. Nothing was
	// committed.
	ErrVerbNothingToCommit = "nothing_to_commit"
	// ErrVerbMergeConflict reports a merge that conflicted and was aborted.
	// The main checkout is as it was.
	ErrVerbMergeConflict = "merge_conflict"
	// ErrVerbCheckoutDirty reports a main checkout with uncommitted changes,
	// or one that is not on the branch to merge into. Nothing was merged.
	ErrVerbCheckoutDirty = "checkout_dirty"
	// ErrVerbNoRemote reports a repository with no remote to push to.
	ErrVerbNoRemote = "no_remote"
	// ErrVerbGhUnavailable reports that the gh CLI is not installed or not
	// logged in. Nothing was pushed or opened.
	ErrVerbGhUnavailable = "gh_unavailable"
)

// shipTimeout bounds the git work of one commit or merge, hooks included.
// shipPushTimeout bounds a push, and shipGhTimeout one gh call.
const (
	shipTimeout     = 2 * time.Minute
	shipPushTimeout = 2 * time.Minute
	shipGhTimeout   = 60 * time.Second
)

// shipMessageMax bounds a commit message.
const shipMessageMax = 64 << 10

// Merge modes as the verbs take them.
var shipMergeModes = []string{"merge", "squash", "ff-only"}

// shipVerbs are the registry entries of the ship verbs.
func shipVerbs() map[string]verbEntry {
	prFields := "number, url, state (open, merged or closed), checks (pass, fail or pending, omitted when there are none), passed, failed, pending, branch and checked_at (Unix nanoseconds)"
	pushParams := []verbParam{
		sessionParam,
		windowParam,
		{Name: "remote", Type: "string", Description: "The remote to push to. Omit for the one the branch follows, else the only one, else origin."},
		{Name: "confirm", Type: "string", Description: "The token from the confirm_required answer to a call without it. The call goes ahead only with the token of what it would send now."},
		{Name: "request_id", Type: "string", Description: "Come back for the person's answer to a question an earlier call put in the Inbox."},
		{Name: "wait", Type: "int", Description: "How long to wait for the person's answer in the Inbox, in milliseconds, at most 3600000.", Default: "120000"},
	}
	return map[string]verbEntry{
		"ship-commit": {
			description: "Stage every change in a pane's git work tree, untracked files included and ignored ones not, and commit it on the branch checked out there. git runs with the person's own identity, signing and hooks, and the message is used as given. Refused while the pane's agent is working or waiting on a prompt, unless force is given.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "message", Type: "string", Description: "The commit message. Omit to use the pane's last prompt or turn summary. The call is refused when there is none."},
				{Name: "force", Type: "bool", Description: "Commit while the agent is mid-turn.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree."},
				{Name: "branch", Type: "string", Description: "The branch the commit is on."},
				{Name: "commit", Type: "string", Description: "The new commit."},
				{Name: "message", Type: "string", Description: "The message the commit has."},
			},
			examples: []string{
				`{"id":1,"verb":"ship-commit","params":{"session":"work","window":"build","message":"Add a retry to the client"}}`,
			},
			handler: (*Daemon).verbShipCommit,
		},
		"ship-merge": {
			description: "Merge a worktree pane's branch into its base branch in the repository's main checkout. The main checkout must have that branch checked out and no uncommitted change. A merge that conflicts is aborted, the main checkout is left as it was, and the conflicting files are reported. Uncommitted changes in the worktree are not merged. Nothing is forced.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "into", Type: "string", Description: "The branch to merge into. Omit for the base the worktree was made from, else the branch the main checkout is on."},
				{Name: "mode", Type: "string", Description: "merge fast-forwards when it can and makes a merge commit when it cannot. squash makes one commit. ff-only refuses a branch that cannot be fast-forwarded.", Accepted: shipMergeModes, Default: "merge"},
				{Name: "message", Type: "string", Description: "The message of the merge or squash commit. Omit for git's own."},
			},
			returns: shipMergeReturns(),
			examples: []string{
				`{"id":1,"verb":"ship-merge","params":{"session":"work","window":"build"}}`,
				`{"id":1,"verb":"ship-merge","params":{"session":"work","window":"build","into":"main","mode":"squash"}}`,
			},
			handler: (*Daemon).verbShipMerge,
		},
		"ship-push": {
			description: "Push a pane's branch to a remote and set it as the branch's upstream. Never forced. The first call answers confirm_required with what would be pushed and a token. Call again with that token to push. A caller inside a pane, or on a restricted connection, also needs the person to allow it in the Inbox, and the call waits for that answer.",
			params:      pushParams,
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree."},
				{Name: "branch", Type: "string", Description: "The branch pushed."},
				{Name: "commit", Type: "string", Description: "The commit pushed."},
				{Name: "remote", Type: "string", Description: "The remote pushed to."},
				{Name: "url", Type: "string", Description: "Where the push went: the push URL's host and path, without user name, password or token."},
				{Name: "approved_by", Type: "string", Description: "human when the person allowed it in the Inbox, omitted when the caller is the person."},
			},
			examples: []string{
				`{"id":1,"verb":"ship-push","params":{"session":"work","window":"build"}}`,
			},
			handler: (*Daemon).verbShipPush,
		},
		"ship-pr": {
			description: "Push a pane's branch and open a pull request for it with the gh CLI, as the person is logged in to gh. tuios holds no GitHub token. When the branch already has an open pull request, the push updates it and no new one is opened. The first call answers confirm_required with what would happen and a token. Call again with that token. A caller inside a pane, or on a restricted connection, also needs the person to allow it in the Inbox.",
			params: append(append([]verbParam{}, pushParams...),
				verbParam{Name: "base", Type: "string", Description: "The branch the pull request merges into. Omit for the base the worktree was made from, else the repository's default branch."},
				verbParam{Name: "title", Type: "string", Description: "The title. With title and body both omitted, gh fills them from the commits."},
				verbParam{Name: "body", Type: "string", Description: "The body. Needs title."},
				verbParam{Name: "draft", Type: "bool", Description: "Open it as a draft.", Default: "false"},
			),
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree."},
				{Name: "branch", Type: "string", Description: "The branch pushed."},
				{Name: "commit", Type: "string", Description: "The commit pushed."},
				{Name: "remote", Type: "string", Description: "The remote pushed to."},
				{Name: "created", Type: "bool", Description: "Whether a new pull request was opened. False when one was already open."},
				{Name: "pr", Type: "object", Description: "The pull request: " + prFields + ".", Nullable: true},
				{Name: "approved_by", Type: "string", Description: "human when the person allowed it in the Inbox, omitted when the caller is the person."},
			},
			examples: []string{
				`{"id":1,"verb":"ship-pr","params":{"session":"work","window":"build","draft":true}}`,
			},
			handler: (*Daemon).verbShipPR,
		},
		"ship-status": {
			description: "Read the pull request of a pane's branch as tuios last saw it, and with refresh ask gh now. A worktree session with an open pull request is polled in the background while a client is attached. Off when gh is not installed.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "refresh", Type: "bool", Description: "Ask gh now instead of reading what tuios last saw.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree."},
				{Name: "branch", Type: "string", Description: "The branch checked out there, empty on a detached HEAD."},
				{Name: "gh", Type: "bool", Description: "Whether the gh CLI is installed."},
				{Name: "pr", Type: "object", Description: "The pull request: " + prFields + ". Null when tuios knows of none.", Nullable: true},
			},
			examples: []string{
				`{"id":1,"verb":"ship-status","params":{"session":"work","window":"build"}}`,
			},
			handler: (*Daemon).verbShipStatus,
		},
	}
}

// shipMergeReturns are the result fields of ship-merge, which keep-fan
// carries in its merge field.
func shipMergeReturns() []verbParam {
	return []verbParam{
		{Name: "session", Type: "string", Description: "The session of the worktree."},
		{Name: "worktree", Type: "string", Description: "The root of the worktree."},
		{Name: "repo_root", Type: "string", Description: "The main checkout merged in."},
		{Name: "branch", Type: "string", Description: "The branch merged."},
		{Name: "into", Type: "string", Description: "The branch merged into."},
		{Name: "mode", Type: "string", Description: "merge, squash or ff-only."},
		{Name: "before", Type: "string", Description: "The commit into was on before."},
		{Name: "after", Type: "string", Description: "The commit into is on now."},
		{Name: "commits", Type: "int", Description: "How many commits of the branch into did not have."},
		{Name: "fast_forward", Type: "bool", Description: "Whether into only moved forward."},
		{Name: "up_to_date", Type: "bool", Description: "Whether into already had every commit, so nothing changed."},
		{Name: "uncommitted", Type: "int", Description: "How many uncommitted changes the worktree holds, which the merge did not take."},
	}
}

// shipTarget resolves the pane a ship verb is about and the work tree under
// it. A call that names neither a session nor a window from inside a pane is
// about that pane, not the most recently active session: a push of whatever
// someone else has focused is not what an agent in a worktree means. A pane
// on another machine is refused.
func (d *Daemon) shipTarget(cs *connState, sessionName, window string) (*Session, WindowState, reviewRepo, *verbError) {
	if sessionName == "" && window == "" {
		if fromPane, own := d.peerPane(cs); fromPane && own != "" {
			sessionName, window = d.sessionOfWindow(own), own
		}
	}
	sess, target, verr := d.resolveVerbPane(sessionName, window)
	if verr != nil {
		return nil, WindowState{}, reviewRepo{}, verr
	}
	if target.Host != "" {
		return nil, WindowState{}, reviewRepo{}, hintedVerbError(ErrVerbNotRepo, "window "+shortWindowID(target.ID)+" runs on "+echoName(target.Host)+", and its repository is there, not here", &VerbHint{
			Command: "tuios worktree pull " + target.Host + ":<session>",
			Detail:  "Nothing was changed. Shipping a pane on another machine is not supported yet. Attach to that machine and ship it there, or bring its work here with tuios worktree pull.",
		})
	}
	repo, verr := d.paneRepo(sess, target)
	if verr != nil {
		return nil, WindowState{}, reviewRepo{}, verr
	}
	return sess, target, repo, nil
}

// shipBranch is the branch checked out in the work tree, or the refusal for a
// detached HEAD.
func shipBranch(ctx context.Context, root string) (string, *verbError) {
	branch, err := worktree.CurrentBranch(root)
	if err != nil {
		return "", shipGitFailed(ctx, err)
	}
	if branch == "" {
		return "", hintedVerbError(ErrVerbNotWorktree, "the work tree "+root+" has a detached HEAD, so there is no branch to ship", &VerbHint{
			Detail: "Nothing was changed. Check out a branch in the work tree with git switch -c <name>, then call again.",
		})
	}
	return branch, nil
}

// shipGitFailed is the refusal for a git call of a ship verb that failed.
func shipGitFailed(ctx context.Context, err error) *verbError {
	detail := "The repository is as it was."
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		detail = "git did not finish in time. " + detail
	}
	return hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{Detail: detail})
}

// shipPaneRefusal holds a pane caller without admin to the panes it could
// type into, as restore-checkpoint does.
func (d *Daemon) shipPaneRefusal(cs *connState, verb string, target WindowState) *verbError {
	if pa := d.paneAuthority(cs); pa != nil && !pa.hosted && !pa.grants.Has(GrantAdmin) {
		if why := d.typingRefusal(pa, target, true); why != "" {
			return grantForbidden(verb, pa, why+". "+verb+" changes the files that pane's agent works on")
		}
	}
	return nil
}

// shipBusy refuses a pane whose agent is mid-turn.
func shipBusy(sess *Session, target WindowState, command string) *verbError {
	if target.AgentState != AgentStateWorking && target.AgentState != AgentStateNeedsInput {
		return nil
	}
	return hintedVerbError(ErrVerbNotReady, "the agent in window "+shortWindowID(target.ID)+" is "+target.AgentState.Name()+", and may still be changing the files", &VerbHint{
		Param:   "force",
		Command: command + " -s " + sess.Name() + " -w " + shortWindowID(target.ID) + " --force",
		Detail:  "Nothing was changed. Wait for the agent to finish its turn, or pass force to go ahead now.",
	})
}

// shipCommitMessage is the default commit message: the pane's last prompt,
// else the line its last turn ended with. Only the first line is kept.
func (d *Daemon) shipCommitMessage(sess *Session, window string) string {
	label := d.checkpointLabel(sess, window, "")
	label, _, _ = strings.Cut(strings.TrimSpace(label), "\n")
	label = strings.TrimSpace(label)
	if r := []rune(label); len(r) > 100 {
		label = strings.TrimSpace(string(r[:100]))
	}
	return label
}

// verbShipCommit answers ship-commit.
func (d *Daemon) verbShipCommit(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Message string `json:"message"`
		Force   bool   `json:"force"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Message) > shipMessageMax {
		return nil, invalidParam("message", "message is at most 64 KiB")
	}
	sess, target, repo, verr := d.shipTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	if verr := d.shipPaneRefusal(cs, "ship-commit", target); verr != nil {
		return nil, verr
	}
	if !p.Force {
		if verr := shipBusy(sess, target, "tuios ship commit"); verr != nil {
			return nil, verr
		}
	}
	msg := strings.TrimSpace(p.Message)
	if msg == "" {
		msg = d.shipCommitMessage(sess, target.ID)
	}
	if msg == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "message is required: window "+shortWindowID(target.ID)+" has no prompt or turn summary to use", &VerbHint{
			Param:   "message",
			Command: "tuios ship commit -s " + sess.Name() + " -w " + shortWindowID(target.ID) + " -m '<message>'",
			Detail:  "Nothing was committed. Give the commit message.",
		})
	}
	ctx, cancel := context.WithTimeout(d.ctx, shipTimeout)
	defer cancel()
	d.shipMu.Lock()
	defer d.shipMu.Unlock()
	branch, verr := shipBranch(ctx, repo.root)
	if verr != nil {
		return nil, verr
	}
	commit, err := worktree.CommitAll(ctx, repo.root, msg)
	switch {
	case errors.Is(err, worktree.ErrNothingToCommit):
		return nil, hintedVerbError(ErrVerbNothingToCommit, "the work tree "+repo.root+" has no change to commit", &VerbHint{
			Detail: "Nothing was committed. The work tree matches HEAD.",
		})
	case err != nil:
		verr := shipGitFailed(ctx, err)
		verr.Hint.Detail = "Nothing was committed, and the index is as it was. A hook or the signing setup may have refused the commit: git's message says which."
		return nil, verr
	}
	LogBasic("Committed %s on %s in %s for window %s", shortCommit(commit), branch, repo.root, shortWindowID(target.ID))
	return map[string]any{
		"type":     "ship_committed",
		"session":  sess.Name(),
		"window":   target.ID,
		"worktree": repo.root,
		"branch":   branch,
		"commit":   commit,
		"message":  msg,
	}, nil
}

// shortCommit is the first seven characters of a hash.
func shortCommit(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// shipMergeTarget resolves the branch a worktree merges into: into when
// given, else the recorded base when it is a local branch, else the branch
// the main checkout is on.
func shipMergeInto(repoRoot, recorded, into string) string {
	if into != "" {
		return into
	}
	if recorded != "" && worktree.BranchExists(repoRoot, recorded) {
		return recorded
	}
	b, _ := worktree.CurrentBranch(repoRoot)
	return b
}

// shipMerge merges the branch checked out at root into into, in the main
// checkout at repoRoot. sessionName names the worktree's session in the
// result. ship-merge and keep-fan both call it.
func (d *Daemon) shipMerge(sessionName, root, repoRoot, recorded, into, mode, message string) (map[string]any, *verbError) {
	if root == "" || repoRoot == "" || canonRoot(root) == canonRoot(repoRoot) {
		return nil, hintedVerbError(ErrVerbNotWorktree, "the pane is in the main checkout "+repoRoot+", not a linked worktree, so there is no branch to merge", &VerbHint{
			Verb:    "list-worktrees",
			Command: "tuios worktree ls",
			Detail:  "Nothing was merged. Name a pane in a worktree session.",
		})
	}
	ctx, cancel := context.WithTimeout(d.ctx, shipTimeout)
	defer cancel()
	d.shipMu.Lock()
	defer d.shipMu.Unlock()
	branch, verr := shipBranch(ctx, root)
	if verr != nil {
		return nil, verr
	}
	into = shipMergeInto(repoRoot, recorded, into)
	if into == "" {
		return nil, hintedVerbError(ErrVerbCheckoutDirty, "the main checkout "+repoRoot+" has a detached HEAD, and no branch to merge into was given", &VerbHint{
			Param:  "into",
			Detail: "Nothing was merged. Check out the branch to merge into in the main checkout, or pass into.",
		})
	}
	m := worktree.MergeDefault
	switch mode {
	case "squash":
		m = worktree.MergeSquash
	case "ff-only":
		m = worktree.MergeFFOnly
	}
	res, err := worktree.MergeBranch(ctx, repoRoot, branch, into, m, message)
	var conflict *worktree.ConflictError
	var dirty *worktree.DirtyError
	switch {
	case errors.As(err, &conflict):
		LogBasic("Merge of %s into %s in %s conflicted and was aborted: %v", branch, into, repoRoot, conflict.Files)
		return nil, hintedVerbError(ErrVerbMergeConflict, "merging "+branch+" into "+into+" conflicts in "+strconv.Itoa(len(conflict.Files))+" "+plural.Word(len(conflict.Files), "file", "files"), &VerbHint{
			Available: conflict.Files,
			Detail:    "The merge was aborted, and the main checkout is as it was. Rebase " + branch + " on " + into + " in the worktree and resolve the conflicts there, then merge again.",
		})
	case errors.As(err, &dirty):
		return nil, hintedVerbError(ErrVerbCheckoutDirty, "the main checkout "+repoRoot+" has "+strconv.Itoa(dirty.Count)+" uncommitted "+plural.Word(dirty.Count, "change", "changes"), &VerbHint{
			Available: dirty.Files,
			Detail:    "Nothing was merged. Commit or stash the changes in the main checkout, then merge again.",
		})
	case err != nil && strings.Contains(err.Error(), "the main checkout"):
		return nil, hintedVerbError(ErrVerbCheckoutDirty, err.Error(), &VerbHint{
			Param:  "into",
			Detail: "Nothing was merged. Check out " + into + " in the main checkout, or finish what is in progress there, then merge again.",
		})
	case err != nil && m == worktree.MergeFFOnly && strings.Contains(err.Error(), "cannot be fast-forwarded"):
		return nil, hintedVerbError(ErrVerbCheckoutDirty, err.Error(), &VerbHint{
			Param:  "mode",
			Detail: "Nothing was merged. Merge without ff-only, or rebase " + branch + " on " + into + " first.",
		})
	case err != nil:
		return nil, shipGitFailed(ctx, err)
	}
	uncommitted, _ := worktree.ChangesCtx(ctx, root)
	if !res.UpToDate {
		LogBasic("Merged %s into %s in %s (%s, %d commits)", branch, into, repoRoot, res.Mode, res.Commits)
	}
	return map[string]any{
		"session":      sessionName,
		"worktree":     canonRoot(root),
		"repo_root":    repoRoot,
		"branch":       branch,
		"into":         res.Into,
		"mode":         res.Mode,
		"before":       res.Before,
		"after":        res.After,
		"commits":      res.Commits,
		"fast_forward": res.FastForward,
		"up_to_date":   res.UpToDate,
		"uncommitted":  uncommitted,
	}, nil
}

// verbShipMerge answers ship-merge.
func (d *Daemon) verbShipMerge(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Into    string `json:"into"`
		Mode    string `json:"mode"`
		Message string `json:"message"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Mode != "" && !slices.Contains(shipMergeModes, p.Mode) {
		return nil, invalidParam("mode", "mode is merge, squash or ff-only", shipMergeModes...)
	}
	if p.Into != "" {
		if err := worktree.ValidBranch(p.Into); err != nil {
			return nil, invalidParam("into", err.Error())
		}
	}
	if len(p.Message) > shipMessageMax {
		return nil, invalidParam("message", "message is at most 64 KiB")
	}
	sess, target, repo, verr := d.shipTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	if verr := d.shipPaneRefusal(cs, "ship-merge", target); verr != nil {
		return nil, verr
	}
	res, verr := d.shipMerge(sess.Name(), repo.root, repo.repoRoot, repo.recorded, p.Into, p.Mode, p.Message)
	if verr != nil {
		return nil, verr
	}
	res["type"] = "ship_merged"
	res["window"] = target.ID
	return res, nil
}

// shipPushParams are what ship-push takes, and ship-pr on top of them.
type shipPushParams struct {
	Session   string `json:"session"`
	Window    string `json:"window"`
	Remote    string `json:"remote"`
	Confirm   string `json:"confirm"`
	RequestID string `json:"request_id"`
	Wait      int    `json:"wait"`
	Base      string `json:"base"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Draft     bool   `json:"draft"`
}

// shipOutbound is what a push or a pull request would send, resolved.
type shipOutbound struct {
	sess   *Session
	target WindowState
	repo   reviewRepo
	branch string
	commit string
	push   worktree.PushTarget
	// base is the pull request's base, empty for the default branch.
	base string
	// existing is the branch's open pull request, for ship-pr.
	existing *ghpr.PR
}

// token is the hash a confirm must carry: everything the call would send.
func (o shipOutbound) token(verb string, p shipPushParams) string {
	h := sha256.New()
	for _, s := range []string{verb, o.repo.root, o.branch, o.commit, o.push.Remote, o.push.URL, o.base, p.Title, p.Body, strconv.FormatBool(p.Draft)} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// shipResolveOutbound resolves what a push from the pane would send.
func (d *Daemon) shipResolveOutbound(cs *connState, p shipPushParams) (shipOutbound, *verbError) {
	var o shipOutbound
	if p.Remote != "" && (strings.HasPrefix(p.Remote, "-") || strings.ContainsAny(p.Remote, " \t\n")) {
		return o, invalidParam("remote", "remote is the name of a git remote")
	}
	sess, target, repo, verr := d.shipTarget(cs, p.Session, p.Window)
	if verr != nil {
		return o, verr
	}
	o.sess, o.target, o.repo = sess, target, repo
	ctx, cancel := context.WithTimeout(d.ctx, shipTimeout)
	defer cancel()
	branch, verr := shipBranch(ctx, repo.root)
	if verr != nil {
		return o, verr
	}
	o.branch = branch
	commit, err := worktree.BranchCommitCtx(ctx, repo.root, branch)
	if err != nil {
		return o, hintedVerbError(ErrVerbNothingToCommit, "branch "+branch+" has no commit yet", &VerbHint{
			Command: "tuios ship commit -s " + sess.Name() + " -w " + shortWindowID(target.ID),
			Detail:  "Nothing was pushed. Commit the work first.",
		})
	}
	o.commit = commit
	pt, err := worktree.ResolvePush(ctx, repo.root, branch, p.Remote)
	switch {
	case errors.Is(err, worktree.ErrNoRemote):
		return o, hintedVerbError(ErrVerbNoRemote, "the repository of "+repo.root+" has no remote to push to", &VerbHint{
			Detail: "Nothing was pushed. Add one with git remote add origin <url>, then call again.",
		})
	case err != nil:
		return o, hintedVerbError(ErrVerbNoRemote, err.Error(), &VerbHint{
			Param:  "remote",
			Detail: "Nothing was pushed. Name the remote to push to.",
		})
	}
	o.push = pt
	return o, nil
}

// shipConfirmLines describe an outbound call for the person, one line each.
func shipConfirmLines(ctx context.Context, verb string, o shipOutbound, p shipPushParams) []string {
	lines := []string{"push " + o.branch + " at " + shortCommit(o.commit) + " to " + o.push.Remote + " (" + pushDestination(o.push.URL) + ")"}
	base := o.push.Remote + "/" + o.branch
	if worktree.RemoteBranchCommit(ctx, o.repo.root, o.push) == "" {
		base = ""
		if o.repo.recorded != "" {
			base = o.repo.recorded
		}
	}
	if subjects, err := worktree.Subjects(ctx, o.repo.root, base, o.commit, 10); err == nil {
		for _, s := range subjects {
			lines = append(lines, "  commit: "+oneLineOf(s, 100))
		}
	}
	if verb == "ship-pr" {
		switch {
		case o.existing != nil:
			lines = append(lines, "update pull request #"+strconv.Itoa(o.existing.Number)+" ("+o.existing.URL+")")
		default:
			into := o.base
			if into == "" {
				into = "the default branch"
			}
			kind := "pull request"
			if p.Draft {
				kind = "draft pull request"
			}
			title := p.Title
			if title == "" {
				title = "from the commits"
			}
			lines = append(lines, "open a "+kind+" into "+into+" with gh, title: "+oneLineOf(title, 100))
		}
	}
	return lines
}

// oneLineOf is s on one line, cut to limit runes.
func oneLineOf(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "..."
	}
	return s
}

// shipAsks remembers which Inbox question was put for which outbound call,
// so a caller that comes back with request_id gets only the call the person
// was asked about.
type shipAsks struct {
	mu    sync.Mutex
	asked map[string]shipAsked
}

type shipAsked struct {
	token  string
	window string
}

const shipAsksMax = 64

func (a *shipAsks) put(id string, v shipAsked) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.asked == nil {
		a.asked = make(map[string]shipAsked)
	}
	if len(a.asked) >= shipAsksMax {
		for k := range a.asked {
			delete(a.asked, k)
			break
		}
	}
	a.asked[id] = v
}

func (a *shipAsks) get(id string) (shipAsked, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.asked[id]
	return v, ok
}

func (a *shipAsks) drop(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.asked, id)
}

// Answers to a ship question in the Inbox.
const (
	shipAllow = "allow"
	shipDeny  = "deny"
)

// shipCallerIsPerson reports whether the caller on cs counts as the person
// for an outbound ship verb: a local process outside every pane, on a
// connection it did not restrict.
func (d *Daemon) shipCallerIsPerson(cs *connState) bool {
	if cs == nil {
		return true
	}
	if cs.viaLink || cs.scope.Load() != nil {
		return false
	}
	return d.mayActAsHuman(cs)
}

// shipOutboundGate runs the confirm token and, for a caller that is not the
// person, the Inbox question. It returns approvedBy ("human" when the person
// allowed it in the Inbox, "" when the caller is the person), or the refusal.
func (d *Daemon) shipOutboundGate(cs *connState, verb, cli string, o shipOutbound, p shipPushParams) (string, *verbError) {
	ctx, cancel := context.WithTimeout(d.ctx, shipTimeout)
	defer cancel()
	token := o.token(verb, p)
	retry := cli + " -s " + o.sess.Name() + " -w " + shortWindowID(o.target.ID)
	if p.Confirm != token {
		msg := verb + " sends work off this machine, and was called without a confirm token, and sent nothing"
		detail := "Check what would be sent, listed in available, then call again with confirm set to the token in confirm."
		if p.Confirm != "" {
			msg = "what " + verb + " would send changed since the token was issued, so nothing was sent"
			detail = "The branch, its commit, the remote or the pull request asked for changed between the look and the call. Check what is listed in available now, then call again with the new token."
		}
		return "", hintedVerbError(ErrVerbConfirmRequired, msg, &VerbHint{
			Param:     "confirm",
			Available: shipConfirmLines(ctx, verb, o, p),
			Confirm:   token,
			Command:   retry + " --yes",
			Detail:    detail,
		})
	}
	if cs != nil && cs.viaLink {
		return "", hintedVerbError(ErrVerbForbidden, verb+" is refused over a link: the person on this machine allows a push here", &VerbHint{
			Detail: "Nothing was sent. Run it on the machine the repository is on.",
		})
	}
	if d.shipCallerIsPerson(cs) {
		return "", nil
	}
	return d.shipAskPerson(cs, verb, o, p, token)
}

// shipAskPerson puts the outbound call to the person in the Inbox, as the
// caller's own pane, and waits for the answer.
func (d *Daemon) shipAskPerson(cs *connState, verb string, o shipOutbound, p shipPushParams, token string) (string, *verbError) {
	fromPane, own := d.peerPane(cs)
	if !fromPane || own == "" {
		// A restricted connection outside every pane, or a pane the daemon
		// could not name: the question is about the target pane.
		own = o.target.ID
	}
	wait := 120 * time.Second
	if p.Wait > 0 {
		wait = min(time.Duration(p.Wait)*time.Millisecond, maxAskWait)
	}
	var hold *askHold
	if p.RequestID != "" {
		asked, ok := d.shipAsks.get(p.RequestID)
		if !ok || asked.window != own {
			return "", hintedVerbError(ErrVerbInvalidParams, "no question was put to the person for this call under request "+echoName(p.RequestID), &VerbHint{
				Param:  "request_id",
				Detail: "Nothing was sent. Call again without request_id to ask the person.",
			})
		}
		if asked.token != token {
			d.shipAsks.drop(p.RequestID)
			return "", hintedVerbError(ErrVerbConfirmRequired, "what "+verb+" would send changed since the person was asked, so nothing was sent", &VerbHint{
				Param:   "request_id",
				Confirm: token,
				Detail:  "The person was asked about what was there then. Call again without request_id to ask about what is there now.",
			})
		}
		h, out, settled, err := d.attention.joinAsk(p.RequestID)
		switch {
		case errors.Is(err, errAskBusy):
			return "", invalidParam("request_id", "another call is already waiting on this question")
		case err != nil:
			return "", invalidParam("request_id", "no question was asked under request "+echoName(p.RequestID))
		case settled:
			return d.shipAskOutcome(verb, p.RequestID, out)
		}
		hold = h
	} else {
		sess := d.sessionHoldingWindow(own)
		if sess == nil {
			sess = o.sess
		}
		question := shipQuestion(verb, o)
		item := AttentionItem{Session: sess.Name(), Summary: question, Options: []string{shipAllow, shipDeny}, Name: verb}
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, own); err == nil {
			w := st.Windows[idx]
			item.Window, item.Name = w.ID, windowLabelOf(w)
			item.Harness, item.Workspace = w.AgentHarness, w.Workspace
		}
		h, err := d.attention.openAsk(item, true)
		if err != nil {
			return "", hintedVerbError(ErrVerbRateLimited, "the session holds too many open questions", &VerbHint{
				Verb:   "list-attention",
				Detail: "Nothing was sent. Wait for the person to answer or dismiss one, then call again.",
			})
		}
		d.shipAsks.put(h.id, shipAsked{token: token, window: own})
		LogBasic("Asked the person to allow %s of %s for window %s (request %s)", verb, o.branch, shortWindowID(own), h.id)
		hold = h
	}
	res := d.awaitAsk(cs, hold, wait)
	status, _ := res["status"].(string)
	if status == AskPending || status == askEndShutdown {
		return "", hintedVerbError(ErrVerbNotReady, "the person has not answered yet, so nothing was sent", &VerbHint{
			Param:   "request_id",
			Command: "tuios ship " + strings.TrimPrefix(verb, "ship-") + " -s " + o.sess.Name() + " -w " + shortWindowID(o.target.ID) + " --yes --request " + hold.id,
			Detail:  "The question stays in the Inbox under request " + hold.id + ". Call again with the same confirm and this request_id to wait for the answer.",
		})
	}
	answer, _ := res["answer"].(string)
	return d.shipAskOutcome(verb, hold.id, askOutcome{Reason: status, Answer: answer})
}

// shipAskOutcome turns how the person's question ended into the go-ahead or
// the refusal.
func (d *Daemon) shipAskOutcome(verb, requestID string, out askOutcome) (string, *verbError) {
	d.shipAsks.drop(requestID)
	if out.Reason == AskAnswered && out.Answer == shipAllow {
		return queueByHuman, nil
	}
	why := "the person did not allow it"
	if out.Reason != AskAnswered {
		why = "the question ended without an answer (" + out.Reason + ")"
	}
	return "", hintedVerbError(ErrVerbForbidden, verb+" was refused: "+why, &VerbHint{
		Detail: "Nothing was sent. Do not ask again unless the person tells you to.",
	})
}

// shipQuestion is the one-line question the Inbox shows. It names where the
// push goes, the remote and its address, since a remote's name says nothing
// about where it points: an agent can point origin anywhere. A long address
// keeps its start, which holds the host, and the end of its path.
func shipQuestion(verb string, o shipOutbound) string {
	dest := pushDestination(o.push.URL)
	ask := func(dest string) string {
		where := o.push.Remote + " (" + dest + ")"
		if verb == "ship-pr" {
			return "Push " + o.branch + " (" + shortCommit(o.commit) + ") to " + where + " and open a pull request?"
		}
		return "Push " + o.branch + " (" + shortCommit(o.commit) + ") to " + where + "?"
	}
	what := ask(dest)
	if over := len(what) - attentionMaxSummary; over > 0 {
		what = ask(shortenDestination(dest, len(dest)-over))
	}
	if !askLineShown(what, attentionMaxSummary) {
		what = "Push branch at " + shortCommit(o.commit) + " to " + shortenDestination(dest, 80) + "?"
	}
	if !askLineShown(what, attentionMaxSummary) {
		what = "Push branch at " + shortCommit(o.commit) + " to a remote?"
	}
	return what
}

// pushDestination is a push URL as a person is shown it: its host and path,
// with no user name, password or token, and no query or fragment. A local
// path is shown as it is, and an scp-like address (user@host:path) loses the
// user.
func pushDestination(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			return oneLineOf(u.Host+u.Path, 512)
		} else if err == nil && u.Scheme == "file" {
			return oneLineOf(u.Path, 512)
		}
		// Unreadable as a URL: keep what follows the last @ of the
		// authority, so no credential is shown.
		authority, path, _ := strings.Cut(raw[i+3:], "/")
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
		path, _, _ = strings.Cut(path, "?")
		path, _, _ = strings.Cut(path, "#")
		return oneLineOf(authority+"/"+path, 512)
	}
	// scp-like: [user@]host:path, with a colon before the first slash once
	// the user is gone. A local path has none. The user is cut at the last @
	// before the first slash, since it may itself hold a colon.
	rest := raw
	before, _, _ := strings.Cut(raw, "/")
	if at := strings.LastIndex(before, "@"); at >= 0 {
		rest = raw[at+1:]
	}
	if colon := strings.Index(rest, ":"); colon > 0 && !strings.Contains(rest[:colon], "/") {
		return oneLineOf(rest, 512)
	}
	return oneLineOf(raw, 512)
}

// shortenDestination fits dest in about limit bytes, keeping its start and
// its end, cut on rune boundaries.
func shortenDestination(dest string, limit int) string {
	const gap = "..."
	limit = max(limit, 24)
	if len(dest) <= limit {
		return dest
	}
	head := (limit - len(gap)) / 2
	tail := limit - len(gap) - head
	for head > 0 && !utf8.RuneStart(dest[head]) {
		head--
	}
	start := len(dest) - tail
	for start < len(dest) && !utf8.RuneStart(dest[start]) {
		start++
	}
	return dest[:head] + gap + dest[start:]
}

// verbShipPush answers ship-push.
func (d *Daemon) verbShipPush(cs *connState, params json.RawMessage) (any, *verbError) {
	var p shipPushParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Wait < 0 {
		return nil, invalidParam("wait", "wait is milliseconds and cannot be negative")
	}
	o, verr := d.shipResolveOutbound(cs, p)
	if verr != nil {
		return nil, verr
	}
	approvedBy, verr := d.shipOutboundGate(cs, "ship-push", "tuios ship push", o, p)
	if verr != nil {
		return nil, verr
	}
	if verr := d.shipPush(o); verr != nil {
		return nil, verr
	}
	res := map[string]any{
		"type":     "ship_pushed",
		"session":  o.sess.Name(),
		"window":   o.target.ID,
		"worktree": o.repo.root,
		"branch":   o.branch,
		"commit":   o.commit,
		"remote":   o.push.Remote,
		"url":      pushDestination(o.push.URL),
	}
	if approvedBy != "" {
		res["approved_by"] = approvedBy
	}
	return res, nil
}

// shipPush pushes the resolved branch.
func (d *Daemon) shipPush(o shipOutbound) *verbError {
	ctx, cancel := context.WithTimeout(d.ctx, shipPushTimeout)
	defer cancel()
	d.shipMu.Lock()
	defer d.shipMu.Unlock()
	if err := worktree.Push(ctx, o.repo.root, o.push, o.commit); err != nil {
		verr := shipGitFailed(ctx, err)
		verr.Hint.Detail = "Nothing was pushed. When the remote branch moved on, pull or rebase in the worktree first. tuios never force-pushes."
		return verr
	}
	LogBasic("Pushed %s at %s to %s", o.branch, shortCommit(o.commit), o.push.Remote)
	return nil
}

// shipGhReady refuses when gh is not installed or not logged in.
func shipGhReady(ctx context.Context, dir string) *verbError {
	if _, err := ghpr.Path(); err != nil {
		return hintedVerbError(ErrVerbGhUnavailable, "the gh CLI is not installed, and tuios opens pull requests only through it", &VerbHint{
			Detail: "Nothing was pushed or opened. Install gh from https://cli.github.com and run gh auth login. Then call again.",
		})
	}
	if err := ghpr.CheckAuth(ctx, dir); err != nil {
		return hintedVerbError(ErrVerbGhUnavailable, "gh is not logged in: "+err.Error(), &VerbHint{
			Detail: "Nothing was pushed or opened. Run gh auth login in a terminal. Then call again.",
		})
	}
	return nil
}

// shipPRBase is the base a pull request opens into: the parameter, else the
// worktree's recorded base without a remote prefix, else empty for the
// repository's default branch.
func shipPRBase(base, recorded, remote string) string {
	if base != "" {
		return base
	}
	if recorded == "" {
		return ""
	}
	return strings.TrimPrefix(recorded, remote+"/")
}

// verbShipPR answers ship-pr.
func (d *Daemon) verbShipPR(cs *connState, params json.RawMessage) (any, *verbError) {
	var p shipPushParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Wait < 0 {
		return nil, invalidParam("wait", "wait is milliseconds and cannot be negative")
	}
	if p.Body != "" && strings.TrimSpace(p.Title) == "" {
		return nil, invalidParam("title", "a body needs a title. Give both, or neither for gh to fill them from the commits")
	}
	if len(p.Title) > 256 || strings.ContainsAny(p.Title, "\n\r") {
		return nil, invalidParam("title", "title is one line of at most 256 bytes")
	}
	if len(p.Body) > shipMessageMax {
		return nil, invalidParam("body", "body is at most 64 KiB")
	}
	if p.Base != "" {
		if err := worktree.ValidBranch(p.Base); err != nil {
			return nil, invalidParam("base", err.Error())
		}
	}
	o, verr := d.shipResolveOutbound(cs, p)
	if verr != nil {
		return nil, verr
	}
	o.base = shipPRBase(p.Base, o.repo.recorded, o.push.Remote)
	ctx, cancel := context.WithTimeout(d.ctx, shipGhTimeout)
	if verr := shipGhReady(ctx, o.repo.root); verr != nil {
		cancel()
		return nil, verr
	}
	if pr, err := ghpr.View(ctx, o.repo.root, o.branch); err == nil && pr.State == ghpr.StateOpen {
		o.existing = &pr
	}
	cancel()
	approvedBy, verr := d.shipOutboundGate(cs, "ship-pr", "tuios ship pr", o, p)
	if verr != nil {
		return nil, verr
	}
	if verr := d.shipPush(o); verr != nil {
		return nil, verr
	}
	ctx, cancel = context.WithTimeout(d.ctx, shipGhTimeout)
	defer cancel()
	created := false
	if o.existing == nil {
		_, err := ghpr.Create(ctx, o.repo.root, ghpr.CreateOptions{Head: o.branch, Base: o.base, Title: p.Title, Body: p.Body, Draft: p.Draft})
		if err != nil {
			return nil, hintedVerbError(ErrVerbCommandFailed, "the branch was pushed, and gh could not open the pull request: "+err.Error(), &VerbHint{
				Detail: "The branch is on " + o.push.Remote + ". Read gh's message, fix what it names, and call again. The push is not repeated when nothing changed.",
			})
		}
		created = true
		LogBasic("Opened a pull request for %s", o.branch)
	}
	var pr *ghpr.PR
	if got, err := ghpr.View(ctx, o.repo.root, o.branch); err == nil {
		got.CheckedAt = time.Now().UnixNano()
		pr = &got
		d.recordPR(o.sess, o.repo.root, pr)
	}
	res := map[string]any{
		"type":     "ship_pr",
		"session":  o.sess.Name(),
		"window":   o.target.ID,
		"worktree": o.repo.root,
		"branch":   o.branch,
		"commit":   o.commit,
		"remote":   o.push.Remote,
		"created":  created,
		"pr":       pr,
	}
	if approvedBy != "" {
		res["approved_by"] = approvedBy
	}
	return res, nil
}

// verbShipStatus answers ship-status.
func (d *Daemon) verbShipStatus(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Refresh bool   `json:"refresh"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, target, repo, verr := d.shipTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	ctx, cancel := context.WithTimeout(d.ctx, shipGhTimeout)
	defer cancel()
	branch, _ := worktree.CurrentBranch(repo.root)
	_, ghErr := ghpr.Path()
	var pr *ghpr.PR
	if wt := sess.Worktree(); wt != nil && canonRoot(wt.Path) == repo.root && wt.PR != nil && wt.PR.Branch == branch {
		pr = wt.PR
	}
	if p.Refresh && branch != "" {
		if ghErr != nil {
			return nil, hintedVerbError(ErrVerbGhUnavailable, "the gh CLI is not installed, and tuios reads pull requests only through it", &VerbHint{
				Detail: "Nothing was read. Install gh from https://cli.github.com and run gh auth login.",
			})
		}
		got, err := d.prs.view(ctx, repo.root, branch)
		switch {
		case errors.Is(err, ghpr.ErrNoPR):
			pr = nil
			d.recordPR(sess, repo.root, nil)
		case err != nil:
			return nil, hintedVerbError(ErrVerbCommandFailed, "gh could not read the pull request: "+err.Error(), &VerbHint{
				Detail: "Nothing was changed. Read gh's message. Run gh auth login when it says you are not logged in.",
			})
		default:
			pr = &got
			d.recordPR(sess, repo.root, pr)
		}
	}
	return map[string]any{
		"type":     "ship_status",
		"session":  sess.Name(),
		"window":   target.ID,
		"worktree": repo.root,
		"branch":   branch,
		"gh":       ghErr == nil,
		"pr":       pr,
	}, nil
}
