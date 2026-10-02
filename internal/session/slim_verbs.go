package session

// slimDroppedVerbs names every verb tuios-slim leaves out: the ones
// addFeatureVerbs registers in the full build. A slim daemon answers each
// with unknown_verb and a message that says the verb is not in tuios-slim
// (missingVerbError). The full build does not use the list.
// TestSlimDroppedVerbsAreFeatureVerbs holds it equal to what addFeatureVerbs
// adds, so the two cannot drift.
var slimDroppedVerbs = []string{
	"agent-activity", "answer-ask", "ask-agent", "ask-human", "bundle-worktree",
	"cancel-queued", "close-pane", "compare-fan", "dismiss-attention",
	"explain-agent-detect", "explain-agent-screen", "fan", "get-agent-state",
	"get-approval", "keep-fan", "link-peer", "list-agents", "list-attention",
	"list-host-agents", "list-host-sessions", "list-hosts", "list-queued",
	"list-worktrees", "mark-attention", "new-worktree", "open-host-connection",
	"open-pane", "pane-agent", "pane-calls", "pane-cwd", "paste-pane-image",
	"peek-prompt", "queue-prompt", "read-agent-messages",
	"release-agent-message", "remove-worktree", "reply-approval",
	"report-agent-activity", "request-approval", "resize-pane", "resolve-pane",
	"respond", "resume-agent", "review-diff", "review-note", "run",
	"screenshot", "send-agent-message", "send-review", "set-agent-meta",
	"set-agent-session", "set-agent-state", "start-agent", "stash-get",
	"stash-list", "stash-put", "subscribe", "unsubscribe", "verify-fan",
}
