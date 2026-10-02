package session

// ErrVerbHostUnreachable reports a host that is configured and not answering.
// It is final: the caller reports it, and it does not retry into another name.
const ErrVerbHostUnreachable = "host_unreachable"

// ErrVerbUnknownHost reports a host name that is not in the configured table.
// Also final, and its message names every configured host.
const ErrVerbUnknownHost = "unknown_host"

// ErrVerbHostRefused reports a host whose link is up and cannot take another
// connection. Its remedy is to close one, which is why it does not share a
// code with host_unreachable, whose remedy is to fix the link.
const ErrVerbHostRefused = "host_refused"

// Error codes the worktree verbs raise, on top of the shared ones.
const (
	// ErrVerbNotWorktree reports a session that is not in a git worktree, so
	// there is nothing to remove or diff.
	ErrVerbNotWorktree = "not_worktree"
	// ErrVerbWorktreeDirty reports a removal refused because the worktree holds
	// uncommitted changes and the caller did not say what to do with them.
	// Nothing was removed. The remedy is stash or force, and the hint says so.
	ErrVerbWorktreeDirty = "worktree_dirty"
	// ErrVerbGitFailed reports a git command that failed, with git's own
	// message. It is final: the repository is as it was.
	ErrVerbGitFailed = "git_failed"
)

// ErrVerbRepoNotFound reports a repo_url that matches no checkout on this
// machine, and no clone was asked for. Its remedy is clone, a repos_root, or
// a directory, which is why it does not share a code with git_failed.
const ErrVerbRepoNotFound = "repo_not_found"

// Error codes the review verbs raise, on top of the shared ones.
const (
	// ErrVerbNotRepo reports a pane or session with no git repository under
	// it, so there is nothing to review. Nothing was read.
	ErrVerbNotRepo = "not_repo"
	// ErrVerbNoNotes reports a send-review that found no unsent notes to
	// send. Nothing was typed.
	ErrVerbNoNotes = "no_notes"
)

// ErrVerbQueueFull reports a pane whose delivery queue holds as many entries
// as [agents.queue] max allows. Nothing was queued.
const ErrVerbQueueFull = "queue_full"

// ErrVerbRiskUnacknowledged reports an allow for an approval that matched a
// risk rule, sent without risk_ack naming exactly the rules it matched.
// Nothing was answered.
const ErrVerbRiskUnacknowledged = "risk_unacknowledged"

// ErrVerbUnknownPane reports a pane id that this daemon is not running. It is
// its own code because the remedy differs from a bad parameter: the pane was
// real and is gone, so the caller should drop it rather than correct it.
const ErrVerbUnknownPane = "unknown_pane"
