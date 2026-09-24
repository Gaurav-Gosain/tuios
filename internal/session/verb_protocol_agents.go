package session

// The verbs of the agent work: reviewing what an agent changed and sending it
// notes, comparing the attempts of a fan, the Inbox's snooze and undo, an
// agent's activity, the delivery queue, and reading a held approval whole.
//
// They are registered here, in one place, together with their scope
// (conn_scope.go), their link capability (link_policy.go) and, for the two that
// type into a pane, their place in typingVerbs (pane_grants.go), so the tests
// that hold those tables to the registry pass from the start and no later
// change has to touch them. The handlers live in the files named beside each
// verb. Until the work behind a verb has landed, its handler checks its
// parameters and answers internal with "not built yet" (notBuilt).

// Accepted values of the parameters below, named once so list-verbs and the
// handlers read the same list.
var (
	// reviewNoteActions are what review-note does.
	reviewNoteActions = []string{"add", "edit", "remove", "list", "clear"}
	// reviewNoteSides are the sides of a diff a note can sit on.
	reviewNoteSides = []string{"new", "old"}
	// markAttentionActions are what mark-attention does to an item.
	markAttentionActions = []string{"snooze", "wake", "unread", "restore"}
	// approvalKinds are the kinds of hold request-approval takes.
	approvalKinds = []string{AttentionApproval, AttentionPlan}
	// activityEvents are the entries set-agent-state's activity reports.
	activityEvents = []string{"prompt", "tool", "tool_done", "tool_failed", "turn_end"}
)

// notBuilt is the answer of a verb, or of a parameter of an older verb, whose
// work has not landed in this build. Nothing was done.
func notBuilt(what string) *verbError {
	return hintedVerbError(ErrVerbInternal, what+" is not built yet in this daemon", &VerbHint{
		Verb:   "list-verbs",
		Detail: "The verb is registered so its scope and link policy are fixed, and its handler has not landed. Nothing was done.",
	})
}

// agentWorkVerbs are the registry entries of the verbs above.
func agentWorkVerbs() map[string]verbEntry {
	humanNonce := verbParam{Name: "human_nonce", Type: "string", Description: "The nonce from the attach reply of a client attached now. The TUI sends its own. Without a live one the call is not the person's."}
	return map[string]verbEntry{
		// verb_review.go
		"review-diff": {
			description: "Read what the agent in a pane changed: the diff of its worktree against the base it was made from, or for a plain repository against the upstream merge base, untracked files included and ignored files left out. Nothing in the repository, its index or its working tree is changed. The text is the repository's, so it is marked untrusted.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "base", Type: "string", Description: "The ref to diff against. Omit for the worktree's base, else the upstream merge base, else HEAD (uncommitted changes only)."},
				{Name: "against", Type: "string", Description: "A fan sibling session to diff against instead of a base: the two attempts compared with each other."},
				{Name: "uncommitted", Type: "bool", Description: "Only what is not committed yet, against HEAD.", Default: "false"},
				{Name: "paths", Type: "[]string", Description: "Only these paths, relative to the repository root. Omit for every changed file."},
				{Name: "context", Type: "int", Description: "Lines of context around each change, 0 to 20.", Default: "3"},
			},
			returns: []verbParam{
				{Name: "repo_root", Type: "string", Description: "The repository's root."},
				{Name: "worktree", Type: "string", Description: "The worktree the diff was read from."},
				{Name: "base", Type: "string", Description: "The base as named, empty with against."},
				{Name: "base_sha", Type: "string", Description: "The commit the base resolved to."},
				{Name: "tree_sha", Type: "string", Description: "The tree the working state was written to, for a later diff of the same state."},
				{Name: "against", Type: "string", Description: "The sibling session, when one was given."},
				{Name: "files", Type: "[]object", Description: "One entry per changed file: path, old_path, status (A, M, D, R or U), added, removed, binary, truncated, and hunks, each with header, old_start, old_lines, new_start, new_lines and lines of op, old, new and text."},
				{Name: "totals", Type: "object", Description: "files, added and removed over the whole diff."},
				{Name: "truncated", Type: "bool", Description: "The diff passed a cap (400 files, 2 MiB, 5000 lines in a file) and the files past it carry counts only."},
				{Name: "untrusted", Type: "bool", Description: "Always true: the text is the repository's, not the daemon's."},
			},
			examples: []string{
				`{"id":1,"verb":"review-diff","params":{"session":"api-fan-retry-2"}}`,
				`{"id":1,"verb":"review-diff","params":{"session":"work","window":"build","uncommitted":true}}`,
			},
			handler: (*Daemon).verbReviewDiff,
		},
		"review-note": {
			description: "Keep the review notes on a pane's changes: add one on a line or a hunk, edit, remove, list or clear them. Notes are held by the daemon per worktree, so every client and the CLI see the same ones, and follow their line as the diff moves. A note is the person's only with a live human_nonce.",
			params: []verbParam{
				{Name: "action", Type: "string", Required: true, Description: "What to do.", Accepted: reviewNoteActions},
				sessionParam,
				windowParam,
				{Name: "path", Type: "string", Description: "The file the note is on, relative to the repository root. Needed to add."},
				{Name: "line", Type: "int", Description: "The line the note is on, on side. Omit with hunk for a note on the whole hunk."},
				{Name: "side", Type: "string", Description: "Which side of the diff line counts on.", Accepted: reviewNoteSides, Default: "new"},
				{Name: "quote", Type: "string", Description: "The text of the line, which finds the line again when the diff moves."},
				{Name: "hunk", Type: "string", Description: "The hunk header the note is on."},
				{Name: "text", Type: "string", Description: "The note, at most 1000 bytes. Needed to add and edit."},
				{Name: "id", Type: "string", Description: "The note to edit or remove."},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "notes", Type: "[]object", Description: "The pane's notes after the call: id, path, side, line, quote, hunk_header, text, by, at, sent_at and outdated."},
			},
			examples: []string{
				`{"id":1,"verb":"review-note","params":{"action":"add","session":"work","window":"build","path":"api/retry.go","line":42,"quote":"if err == nil {","text":"log the attempt number here too"}}`,
				`{"id":1,"verb":"review-note","params":{"action":"list","session":"work","window":"build"}}`,
			},
			handler: (*Daemon).verbReviewNote,
		},
		"send-review": {
			description: "Send a pane's unsent review notes to its agent as one message, through the delivery queue: typed now when the agent is at rest, else when it next comes to rest. The message says it is from the person only with a live human_nonce. It types into the pane, so a pane may send only to a pane that holds nothing it does not.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "ids", Type: "[]string", Description: "Only these notes. Omit for every unsent one."},
				{Name: "now", Type: "bool", Description: "Type it now or not at all: refuse with agent_blocked or not_ready instead of queueing.", Default: "false"},
				humanNonce,
			},
			returns: []verbParam{
				{Name: "notes", Type: "int", Description: "How many notes the message carries."},
				{Name: "queued_id", Type: "string", Description: "The queue entry, which cancel-queued takes."},
				{Name: "delivered", Type: "bool", Description: "Whether it was typed already."},
				{Name: "position", Type: "int", Description: "Its place in the pane's queue, 1 for next."},
			},
			examples: []string{
				`{"id":1,"verb":"send-review","params":{"session":"work","window":"build","human_nonce":"<from the attach reply>"}}`,
			},
			handler: (*Daemon).verbSendReview,
		},

		// verb_fan_compare.go
		"compare-fan": {
			description: "Compare the attempts of a fan: one row per sibling session with its branch, agent, state, changed files against the base, the last verify-fan check and the last command its shells finished.",
			params: []verbParam{
				{Name: "session", Type: "string", Description: "Any session of the fan. Omit for the most recently active session."},
				{Name: "changes", Type: "bool", Description: "Count the changed files and lines of each attempt, one git call per sibling.", Default: "true"},
			},
			returns: []verbParam{
				{Name: "group", Type: "string", Description: "The fan's group."},
				{Name: "repo", Type: "string", Description: "The repository the fan is of."},
				{Name: "base", Type: "string", Description: "The base the attempts were made from."},
				{Name: "rows", Type: "[]object", Description: "One per sibling: session, branch, agent, harness, state, files, added, removed, ahead, dirty, prompt_status, verify and last_command (cmdline, exit, at)."},
			},
			examples: []string{
				`{"id":1,"verb":"compare-fan","params":{"session":"api-fan-retry-1"}}`,
			},
			handler: (*Daemon).verbCompareFan,
		},
		"verify-fan": {
			description: "Run one check in every attempt of a fan: a window named verify in each sibling session runs the command with sh -c and records whether it passed. The window holds no grants, so the check cannot call tuios, and it closes when the check passes. The command is always the caller's; none is read from the repository.",
			params: []verbParam{
				{Name: "session", Type: "string", Description: "Any session of the fan. Omit for the most recently active session."},
				{Name: "command", Type: "string", Required: true, Description: "The command to run, as a shell line."},
				{Name: "timeout_ms", Type: "int", Description: "How long a check may run before it counts as failed, in milliseconds. Omit for no limit."},
			},
			returns: []verbParam{
				{Name: "sessions", Type: "[]string", Description: "The sessions a check was started in. Results reach clients as the verify field of each session's worktree."},
			},
			examples: []string{
				`{"id":1,"verb":"verify-fan","params":{"session":"api-fan-retry-1","command":"go test ./..."}}`,
			},
			handler: (*Daemon).verbVerifyFan,
		},
		"keep-fan": {
			description: "Keep one attempt of a fan and remove the others: each sibling's worktree and session go, the way remove-worktree removes them. A sibling with uncommitted changes is refused unless stash or force says what to do with them.",
			params: []verbParam{
				{Name: "session", Type: "string", Required: true, Description: "The attempt to keep."},
				{Name: "stash", Type: "bool", Description: "Stash a sibling's uncommitted changes before removing it.", Default: "false"},
				{Name: "force", Type: "bool", Description: "Discard a sibling's uncommitted changes.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "kept", Type: "string", Description: "The session kept."},
				{Name: "removed", Type: "[]object", Description: "One per sibling: session, removed, and a note when it was not."},
			},
			examples: []string{
				`{"id":1,"verb":"keep-fan","params":{"session":"api-fan-retry-1"}}`,
			},
			handler: (*Daemon).verbKeepFan,
		},

		// attention_lifecycle.go
		"mark-attention": {
			description: "Snooze an Inbox item, wake it, mark a finished pane unread, or restore an item closed a moment ago, for the person. Only a client attached right now can, with the nonce from its attach reply: an agent cannot hide or reorder what the person reads.",
			params: []verbParam{
				{Name: "id", Type: "string", Description: "The item. Or name it with session, window and kind."},
				sessionParam,
				windowParam,
				{Name: "kind", Type: "string", Description: "The item's kind, with session and window.", Accepted: AttentionKindNames},
				{Name: "action", Type: "string", Required: true, Description: "snooze hides it until a time or until its fact changes, wake shows it again, unread reopens a finished pane's item, restore reopens an item dismissed or snoozed in the last 10 seconds.", Accepted: markAttentionActions},
				{Name: "until", Type: "int", Description: "With snooze: when it opens again, in unix milliseconds."},
				{Name: "for_ms", Type: "int", Description: "With snooze: how long, in milliseconds."},
				{Name: "until_change", Type: "bool", Description: "With snooze: until the pane's state changes.", Default: "false"},
				{Name: "human_nonce", Type: "string", Required: true, Description: "The nonce the daemon issued in an attach reply, for a client attached now over the same kind of connection. The TUI sends its own."},
			},
			returns: []verbParam{
				{Name: "id", Type: "string", Description: "The item."},
				{Name: "action", Type: "string", Description: "What was done."},
				{Name: "snoozed_until", Type: "int", Description: "With snooze: when it opens again, in unix nanoseconds, -1 for until it changes."},
			},
			examples: []string{
				`{"id":1,"verb":"mark-attention","params":{"id":"17","action":"snooze","for_ms":900000,"human_nonce":"<from the attach reply>"}}`,
			},
			handler: (*Daemon).verbMarkAttention,
		},

		// agent_activity.go
		"agent-activity": {
			description: "Read what the agent in a pane has been doing, from the ring of hook events the daemon keeps for it: prompts, tool calls and their results, finished turns, and the commands its shell ran, the newest 256 of them. A pane gets a ring on the first hook report that carries activity, and loses it when it closes; the ring is daemon memory only. With recap, a summary of it since a time. The text is the agent's, cleaned and masked, and marked untrusted.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "since", Type: "int", Description: "Only entries after this time, in unix nanoseconds."},
				{Name: "since_seq", Type: "int", Description: "Only entries after this seq."},
				{Name: "limit", Type: "int", Description: "At most this many entries, newest last, up to 256.", Default: "64"},
				{Name: "recap", Type: "bool", Description: "Also summarise the entries: turns, files, commands, the last test run and what the agent last said.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "entries", Type: "[]object", Description: "Oldest first: seq, at (unix nanoseconds), kind (prompt, tool, tool_done, tool_failed, turn_end, command or state), tool, target, files, ok, exit and text per entry. Empty for a pane with no ring."},
				{Name: "window", Type: "string", Description: "The window id the entries are of."},
				{Name: "last_seq", Type: "int", Description: "The seq of the newest entry the ring holds."},
				{Name: "recap", Type: "object", Description: "With recap, over every entry after since and since_seq whatever the limit: since (where it starts), turns, files (the first 20), files_total, commands, tests (the newest test run: cmdline, ok or null when unknown, at), last_said and state (the pane's state now)."},
				{Name: "untrusted", Type: "bool", Description: "Always true: the text is the agent's."},
			},
			examples: []string{
				`{"id":1,"verb":"agent-activity","params":{"session":"work","window":"build","recap":true}}`,
			},
			handler: (*Daemon).verbAgentActivity,
		},

		// verb_queue.go
		"queue-prompt": {
			description: "Queue a message for the agent in a pane, typed as a prompt once the agent has been at rest for a second, and never over a prompt it is waiting on. One entry is typed per rest, and the prompt gate waits for the agent to show it took it; an entry it did not take is marked stalled, never typed again, and opens an Inbox question. A queue holds at most [agents.queue] max messages, and dies with the daemon, the pane, or the agent leaving the pane. It types into the pane, so a pane may queue only for a pane that holds nothing it does not, and its entry is checked against its grants again when it is typed. Who queued it (by) comes from the connection: human only with a live human_nonce, the pane's id for a pane, link:HOST over a link, shell otherwise.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "The agent's window, by id or name. Omit to target the focused window. It must run an agent tuios knows of; human is refused with no_keyboard."},
				{Name: "text", Type: "string", Required: true, Description: "The message, at most 16 KiB."},
				{Name: "human_nonce", Type: "string", Description: "The attached client's nonce, to queue as the person (by human). One that does not verify is refused with not_human."},
				{Name: "from", Type: "string", Description: "The window the message is from. From a pane, its own; omit it there. human needs human_nonce."},
			},
			returns: []verbParam{
				{Name: "id", Type: "string", Description: "The entry, which cancel-queued takes."},
				{Name: "position", Type: "int", Description: "Its place in the queue, 1 for next."},
				{Name: "queued", Type: "int", Description: "How many entries the queue holds now."},
				{Name: "delivering", Type: "bool", Description: "True when it is next and the agent is at rest now, so it is typed within about a second rather than after the agent's turn."},
			},
			examples: []string{
				`{"id":1,"verb":"queue-prompt","params":{"session":"work","window":"build","text":"make the backoff jitter configurable"}}`,
			},
			handler: (*Daemon).verbQueuePrompt,
		},
		"list-queued": {
			description: "List the messages waiting in a pane's delivery queue, or in every pane of the session.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window id or name. Omit to list every pane of the session."},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session listed."},
				{Name: "entries", Type: "[]object", Description: "id, session, window, name, at (Unix nanoseconds), by (human, shell, the queueing pane's window id, or link:HOST), from when given, preview (the first line, at most 80 characters) and state (waiting, delivering or stalled) per entry, next first within each pane."},
			},
			examples: []string{
				`{"id":1,"verb":"list-queued","params":{"session":"work","window":"build"}}`,
			},
			handler: (*Daemon).verbListQueued,
		},
		"cancel-queued": {
			description: "Drop messages from a pane's delivery queue before they are typed. The person, with a live human_nonce, may drop any entry; a pane only the entries it queued; a linked machine only the entries it queued; a caller outside every pane every entry but the person's. An entry being typed cannot be dropped. Dropping a stalled entry closes its Inbox question; the entries behind it are typed at the pane's next rest, one reached after the stalled entry was typed. A linked machine that gave no name drops only what it queued on the same connection.",
			params: []verbParam{
				sessionParam,
				{Name: "window", Type: "string", Description: "Window id or name. With all, omit it to target the focused window. With id, omit it to find the entry in any pane of the session."},
				{Name: "id", Type: "string", Description: "The entry to drop."},
				{Name: "all", Type: "bool", Description: "Drop every entry of the window the caller may drop.", Default: "false"},
				{Name: "human_nonce", Type: "string", Description: "The attached client's nonce, to drop as the person. One that does not verify is refused with not_human."},
			},
			returns: []verbParam{
				{Name: "cancelled", Type: "[]string", Description: "The entries dropped."},
				{Name: "queued", Type: "int", Description: "How many entries the pane's queue holds now."},
			},
			examples: []string{
				`{"id":1,"verb":"cancel-queued","params":{"session":"work","window":"build","all":true}}`,
				`{"id":1,"verb":"cancel-queued","params":{"session":"work","id":"q3"}}`,
			},
			handler: (*Daemon).verbCancelQueued,
		},

		// verb_approval_ext.go
		"get-approval": {
			description: "Read a held approval or plan whole: the summary, the tool and its target, the answers it takes, the plan text of a plan, and the risk rules its command matched with why. Served only while the hook holds it. The text is the agent's, so it is marked untrusted.",
			params: []verbParam{
				{Name: "request_id", Type: "string", Required: true, Description: "The request_id of the Inbox item."},
				{Name: "session", Type: "string", Description: "The session the approval is in. When given, an approval in another session is not found. From a pane without the admin grant it is the pane's own unless named."},
			},
			returns: []verbParam{
				{Name: "request_id", Type: "string", Description: "The hold."},
				{Name: "kind", Type: "string", Description: "approval or plan.", Accepted: approvalKinds},
				{Name: "summary", Type: "string", Description: "The line the person answers from."},
				{Name: "tool", Type: "string", Description: "The tool the call is for, when the hook named it."},
				{Name: "target", Type: "string", Description: "What the tool acts on: the command, or the path."},
				{Name: "options", Type: "[]string", Description: "The answers it takes."},
				{Name: "always_scope", Type: "[]string", Description: "What always allows from now on."},
				{Name: "plan", Type: "string", Description: "A plan's text, at most 32 KiB."},
				{Name: "plan_sha", Type: "string", Description: "The digest reply-approval takes for a plan."},
				{Name: "risk", Type: "[]object", Description: "The risk rules matched, each with rule and why."},
				{Name: "deny_message", Type: "bool", Description: "Whether a deny may carry a reason."},
				{Name: "untrusted", Type: "bool", Description: "Always true: the text is the agent's."},
			},
			examples: []string{
				`{"id":1,"verb":"get-approval","params":{"request_id":"9f86d081884c7d65"}}`,
			},
			handler: (*Daemon).verbGetApproval,
		},
	}
}
