package session

import (
	"slices"
)

// The verbs of the agent work: reviewing what an agent changed and sending it
// notes, comparing the attempts of a fan, the Inbox's snooze and undo, an
// agent's activity, the delivery queue, and reading a held approval whole.
//
// They are registered here, in one place, together with their scope
// (conn_scope.go), their link capability (link_policy.go) and, for the two that
// type into a pane, their place in typingVerbs (pane_grants.go), so the tests
// that hold those tables to the registry pass from the start and no later
// change has to touch them. The handlers live in the files named beside each
// verb.

// Accepted values of the parameters below, named once so list-verbs and the
// handlers read the same list.
var (
	// reviewNoteActions are what review-note does.
	reviewNoteActions = []string{"add", "edit", "remove", "list", "clear"}
	// reviewIndexModes are the index diffs review-diff reads.
	reviewIndexModes = []string{"unstaged", "staged"}
	// reviewStageActions are what review-stage does.
	reviewStageActions = []string{"stage", "unstage", "undo"}
	// reviewNoteSides are the sides of a diff a note can sit on.
	reviewNoteSides = []string{"new", "old"}
	// markAttentionActions are what mark-attention does to an item.
	markAttentionActions = []string{"snooze", "wake", "unread", "restore", "settle", "unsettle"}
	// approvalKinds are the kinds of hold request-approval takes.
	approvalKinds = []string{AttentionApproval, AttentionPlan}
	// activityEvents are the entries set-agent-state's activity reports.
	activityEvents = []string{ActivityPrompt, ActivityTool, ActivityToolDone, ActivityToolFailed, ActivityTurnEnd}
	// reportActivityEvents are the entries report-agent-activity takes: the
	// same, and the events no state report carries, which set-agent-state
	// predates.
	reportActivityEvents = slices.Concat(activityEvents, []string{ActivitySubagentStart, ActivitySubagentStop, ActivitySessionStart, ActivitySubagentUpdate})
)

// agentWorkVerbs are the registry entries of the verbs above.
func agentWorkVerbs() map[string]verbEntry {
	humanNonce := verbParam{Name: "human_nonce", Type: "string"}
	return map[string]verbEntry{
		// verb_agent_hook.go
		"agent-hook-event": {
			// It reports for the caller's own pane, through the same
			// set-agent-state and report-agent-activity calls a hook makes, each
			// checked on this connection.
			scope: scopeSelf,
			link:  needWrite,
			params: []verbParam{
				{Name: "harness", Type: "string", Required: true},
				{Name: "event", Type: "string"},
				{Name: "payload", Type: "string"},
				{Name: "payload_json", Type: "object"},
				{Name: "env", Type: "object"},
				{Name: "explain", Type: "bool"},
				{Name: "forwarder", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "stdout", Type: "string"},
				{Name: "explain", Type: "object", Nullable: true},
			},
			handler: (*Daemon).verbAgentHookEvent,
		},
		"agent-statusline-event": {
			// It writes the caller's own pane's agent metadata, with the same
			// set-agent-meta call tuios agent-statusline makes.
			scope: scopeSelf,
			link:  needWrite,
			params: []verbParam{
				{Name: "harness", Type: "string", Required: true},
				{Name: "payload", Type: "string"},
				{Name: "payload_json", Type: "object"},
				{Name: "env", Type: "object"},
				{Name: "turn_end", Type: "bool"},
				{Name: "explain", Type: "bool"},
				{Name: "forwarder", Type: "bool"},
			},
			returns: []verbParam{
				{Name: "sent", Type: "bool"},
				{Name: "skip", Type: "string"},
				{Name: "error", Type: "string"},
				{Name: "explain", Type: "object", Nullable: true},
			},
			handler: (*Daemon).verbAgentStatusLineEvent,
		},
		// verb_agent_transcript.go
		"agent-transcript": {
			// The conversation is the person's to read. A restricted connection is
			// automation, never the person.
			scope: scopeDeny,
			// The conversation holds the prompts, file contents and command output.
			// Only the person reads it: the handler needs the nonce of a person's
			// attach (human_sender.go). transcripts is in the default policy and
			// respond includes it. See config.LinkAllowTranscripts.
			link: needTranscripts,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Required: true},
				{Name: "human_nonce", Type: "string", Required: true},
				{Name: "after", Type: "string"},
				{Name: "before", Type: "string"},
				{Name: "limit", Type: "int", Default: "200"},
				{Name: "image", Type: "string"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "harness", Type: "string"},
				{Name: "cursor", Type: "string"},
				{Name: "reset", Type: "bool"},
				{Name: "more", Type: "bool"},
				{Name: "older", Type: "string"},
				{Name: "entries", Type: "[]object"},
				{Name: "media_type", Type: "string"},
				{Name: "data", Type: "string"},
				{Name: "untrusted", Type: "bool"},
			},
			handler: (*Daemon).verbAgentTranscript,
		},
		// verb_review.go
		"review-diff": {
			// From the agent review, triage, queue and approval work
			// (verb_protocol_agents.go). Reads of one session and its fan group: a
			// pane's diff, a fan's comparison, an agent's activity, its queue and a
			// held approval. Writes: review notes, the queue, and sending notes,
			// which types. verify-fan starts a window in each attempt, which is a
			// launch. keep-fan removes worktrees and mark-attention is the person's
			// act on the Inbox, so neither is for a restricted caller.
			scope: scopeRead,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "base", Type: "string"},
				{Name: "against", Type: "string"},
				{Name: "uncommitted", Type: "bool", Default: "false"},
				{Name: "paths", Type: "[]string"},
				{Name: "context", Type: "int", Default: "3"},
				{Name: "index", Type: "string", Accepted: reviewIndexModes},
				{Name: "highlight", Type: "bool", Default: "false"},
				{Name: "origins", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "repo_root", Type: "string"},
				{Name: "worktree", Type: "string"},
				{Name: "base", Type: "string"},
				{Name: "base_sha", Type: "string"},
				{Name: "index", Type: "string"},
				{Name: "classes", Type: "[]string"},
				{Name: "uncommitted", Type: "bool"},
				{Name: "tree_sha", Type: "string"},
				{Name: "against", Type: "string"},
				{Name: "against_tree", Type: "string"},
				{Name: "files", Type: "[]object"},
				{Name: "totals", Type: "object"},
				{Name: "truncated", Type: "bool"},
				{Name: "notes", Type: "[]object"},
				{Name: "authors", Type: "[]object"},
				{Name: "untrusted", Type: "bool"},
			},
			handler: (*Daemon).verbReviewDiff,
		},
		// verb_review_stage.go
		"review-stage": {
			// review-stage writes the index of a pane's worktree. It is the person's
			// tool, so a restricted caller has no use for it.
			scope: scopeDeny,
			// review-stage writes the index, as a commit does.
			link: needWrite,
			params: []verbParam{
				{Name: "action", Type: "string", Required: true, Accepted: reviewStageActions},
				sessionParam,
				windowParam,
				{Name: "path", Type: "string"},
				{Name: "hunk", Type: "string"},
				{Name: "lines", Type: "[]int"},
				{Name: "context", Type: "int", Default: "3"},
				{Name: "undo", Type: "object"},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "worktree", Type: "string"},
				{Name: "path", Type: "string"},
				{Name: "action", Type: "string"},
				{Name: "undo", Type: "object"},
			},
			handler: (*Daemon).verbReviewStage,
		},
		// verb_review_revert.go
		"review-revert": {
			// review-revert writes the working tree of a pane's worktree. It is
			// the person's tool, as review-stage is.
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "action", Type: "string", Required: true, Accepted: reviewRevertActions},
				sessionParam,
				windowParam,
				{Name: "path", Type: "string"},
				{Name: "hunk", Type: "string"},
				{Name: "sum", Type: "string"},
				{Name: "base", Type: "string"},
				{Name: "uncommitted", Type: "bool"},
				{Name: "context", Type: "int", Default: "3"},
				{Name: "id", Type: "string"},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "worktree", Type: "string"},
				{Name: "path", Type: "string"},
				{Name: "action", Type: "string"},
				{Name: "id", Type: "string"},
				{Name: "lines", Type: "int"},
			},
			handler: (*Daemon).verbReviewRevert,
		},
		"review-note": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				{Name: "action", Type: "string", Required: true, Accepted: reviewNoteActions},
				sessionParam,
				windowParam,
				{Name: "path", Type: "string"},
				{Name: "line", Type: "int"},
				{Name: "side", Type: "string", Accepted: reviewNoteSides, Default: "new"},
				{Name: "quote", Type: "string"},
				{Name: "hunk", Type: "string"},
				{Name: "text", Type: "string"},
				{Name: "id", Type: "string"},
				{Name: "to", Type: "string"},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "worktree", Type: "string"},
				{Name: "id", Type: "string"},
				{Name: "removed", Type: "int"},
				{Name: "kept", Type: "int"},
				{Name: "notes", Type: "[]object"},
			},
			handler: (*Daemon).verbReviewNote,
		},
		"send-review": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "ids", Type: "[]string"},
				{Name: "to", Type: "string"},
				{Name: "now", Type: "bool", Default: "false"},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "notes", Type: "int"},
				{Name: "ids", Type: "[]string"},
				{Name: "queued_id", Type: "string"},
				{Name: "position", Type: "int"},
				{Name: "queued", Type: "int"},
				{Name: "delivering", Type: "bool"},
				{Name: "withheld", Type: "[]string"},
				{Name: "withheld_reason", Type: "string"},
				{Name: "messages", Type: "[]object"},
			},
			handler: (*Daemon).verbSendReview,
		},

		// verb_fan_compare.go
		"compare-fan": {
			scope: scopeRead,
			// The agent review, triage, queue and approval work. compare-fan,
			// agent-activity, list-queued and get-approval return states, counts and
			// text an agent wrote, which list reaches. review-diff returns file
			// contents, which write already reaches through a shell and list must
			// not, as bundle-worktree does. compare-fan's detail carries changed
			// lines, so its handler asks write for it. verify-fan opens a window and runs a
			// command in it, so it needs open and write. mark-attention is the
			// person's act, like dismiss-attention.
			link: needList,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "changes", Type: "bool", Default: "true"},
				{Name: "detail", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "group", Type: "string"},
				{Name: "repo", Type: "string"},
				{Name: "repo_root", Type: "string"},
				{Name: "base", Type: "string"},
				{Name: "rows", Type: "[]object"},
				{Name: "prompt", Type: "string"},
				{Name: "verdict", Type: "object"},
				{Name: "total", Type: "int"},
			},
			handler: (*Daemon).verbCompareFan,
		},
		"verify-fan": {
			scope: scopeLaunch,
			link:  needOpen | needWrite,
			params: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "command", Type: "string", Required: true},
				{Name: "timeout_ms", Type: "int"},
				{Name: "env", Type: "object"},
			},
			returns: []verbParam{
				{Name: "group", Type: "string"},
				{Name: "command", Type: "string"},
				{Name: "sessions", Type: "[]string"},
				{Name: "skipped", Type: "[]object"},
			},
			handler: (*Daemon).verbVerifyFan,
		},
		"set-fan-verdict": {
			// set-fan-verdict writes a judge's pick on the fan's attempts. It needs
			// the fan grant, and the handler takes it only from the fan's judge pane
			// or from outside every pane.
			scope: scopeLaunch,
			link:  needWrite,
			params: []verbParam{
				{Name: "session", Type: "string", Required: true},
				{Name: "pick", Type: "string", Required: true},
				{Name: "summary", Type: "string", Required: true},
				{Name: "reasons", Type: "[]string"},
				{Name: "notes", Type: "object"},
			},
			returns: []verbParam{
				{Name: "group", Type: "string"},
				{Name: "pick", Type: "string"},
				{Name: "by", Type: "string"},
				{Name: "attempts", Type: "[]string"},
			},
			handler: (*Daemon).verbSetFanVerdict,
		},
		"judge-fan": {
			// judge-fan starts an agent, which is a launch.
			scope: scopeLaunch,
			link:  needOpen | needWrite,
			params: []verbParam{
				{Name: "session", Type: "string", Required: true},
				{Name: "agent", Type: "string"},
				{Name: "env", Type: "object"},
				{Name: "ready_timeout", Type: "int", Default: "120000"},
			},
			returns: []verbParam{
				{Name: "judge_session", Type: "string"},
				{Name: "group", Type: "string"},
				{Name: "window_id", Type: "string"},
			},
			handler: (*Daemon).verbJudgeFan,
		},
		"keep-fan": {
			scope: scopeDeny,
			link:  needWrite,
			params: []verbParam{
				{Name: "session", Type: "string", Required: true},
				{Name: "stash", Type: "bool", Default: "false"},
				{Name: "force", Type: "bool", Default: "false"},
				{Name: "merge", Type: "bool", Default: "false"},
				{Name: "merge_mode", Type: "string", Accepted: shipMergeModes, Default: "merge"},
				{Name: "into", Type: "string"},
				{Name: "group", Type: "string"},
				{Name: "repo_root", Type: "string"},
				{Name: "fan_id", Type: "string"},
				{Name: "siblings", Type: "[]string"},
				{Name: "ignored", Type: "object"},
			},
			returns: []verbParam{
				{Name: "kept", Type: "string"},
				{Name: "branch", Type: "string"},
				{Name: "group", Type: "string"},
				{Name: "removed", Type: "[]object"},
				{Name: "left", Type: "int"},
				{Name: "merge", Type: "object"},
			},
			handler: (*Daemon).verbKeepFan,
		},

		// attention_lifecycle.go
		"mark-attention": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "id", Type: "string"},
				sessionParam,
				windowParam,
				{Name: "kind", Type: "string", Accepted: AttentionKindNames},
				{Name: "action", Type: "string", Required: true, Accepted: markAttentionActions},
				{Name: "until", Type: "int"},
				{Name: "for_ms", Type: "int"},
				{Name: "until_change", Type: "bool", Default: "false"},
				{Name: "human_nonce", Type: "string", Required: true},
			},
			returns: []verbParam{
				{Name: "id", Type: "string"},
				{Name: "action", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "snoozed_until", Type: "int"},
			},
			handler: (*Daemon).verbMarkAttention,
		},

		// agent_activity.go
		"agent-activity": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "since", Type: "int"},
				{Name: "since_seq", Type: "int"},
				{Name: "limit", Type: "int", Default: "64"},
				{Name: "recap", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "entries", Type: "[]object"},
				{Name: "window", Type: "string"},
				{Name: "last_seq", Type: "int"},
				{Name: "recap", Type: "object"},
				{Name: "untrusted", Type: "bool"},
			},
			handler: (*Daemon).verbAgentActivity,
		},

		// verb_queue.go
		"queue-prompt": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "text", Type: "string"},
				{Name: "parts", Type: "[]object"},
				{Name: "human_nonce", Type: "string"},
				{Name: "from", Type: "string"},
				{Name: "client_id", Type: "string"},
				{Name: "host", Type: "string"},
			},
			returns: []verbParam{
				{Name: "id", Type: "string"},
				{Name: "position", Type: "int"},
				{Name: "queued", Type: "int"},
				{Name: "delivering", Type: "bool"},
				{Name: "parts", Type: "int"},
				{Name: "duplicate", Type: "bool"},
			},
			handler: (*Daemon).verbQueuePrompt,
		},
		// reply_history.go
		"reply-history": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "target", Type: "string", Required: true},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "target", Type: "string"},
				{Name: "entries", Type: "[]object"},
			},
			handler: (*Daemon).verbReplyHistory,
		},
		"add-reply-history": {
			scope: scopeDeny,
			link:  needRespond,
			params: []verbParam{
				{Name: "target", Type: "string", Required: true},
				{Name: "parts", Type: "[]object", Required: true},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "dropped", Type: "[]string"},
			},
			handler: (*Daemon).verbAddReplyHistory,
		},
		"list-queued": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "host", Type: "string"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "entries", Type: "[]object"},
			},
			handler: (*Daemon).verbListQueued,
		},
		"cancel-queued": {
			scope: scopeWrite,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string"},
				{Name: "id", Type: "string"},
				{Name: "all", Type: "bool", Default: "false"},
				{Name: "human_nonce", Type: "string"},
				{Name: "host", Type: "string"},
			},
			returns: []verbParam{
				{Name: "cancelled", Type: "[]string"},
				{Name: "queued", Type: "int"},
			},
			handler: (*Daemon).verbCancelQueued,
		},

		// verb_approval_ext.go
		"get-approval": {
			scope: scopeRead,
			link:  needList,
			params: []verbParam{
				{Name: "request_id", Type: "string", Required: true},
				{Name: "session", Type: "string"},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string"},
				{Name: "kind", Type: "string", Accepted: approvalKinds},
				{Name: "session", Type: "string"},
				{Name: "window", Type: "string"},
				{Name: "summary", Type: "string"},
				{Name: "tool", Type: "string"},
				{Name: "target", Type: "string"},
				{Name: "options", Type: "[]string"},
				{Name: "always_scope", Type: "[]string"},
				{Name: "plan", Type: "string"},
				{Name: "plan_sha", Type: "string"},
				{Name: "risk", Type: "[]object"},
				{Name: "deny_message", Type: "bool"},
				{Name: "untrusted", Type: "bool"},
			},
			handler: (*Daemon).verbGetApproval,
		},
	}
}
