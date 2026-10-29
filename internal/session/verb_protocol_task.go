package session

import "slices"

// taskVerbs are the registry entries of the task verbs (verb_task.go).
func taskVerbs() map[string]verbEntry {
	id := verbParam{Name: "id", Type: "string", Required: true, Description: "The card, such as t1."}
	humanNonce := verbParam{Name: "human_nonce", Type: "string", Description: "The attached client's nonce, to act as the person (by human). One that does not verify is refused with not_human."}
	rowFields := "id, session, title, preview (the first line of the prompt, at most 80 characters), stage, stage_at (Unix nanoseconds), holder (window, name, harness, state, held_at, read from the pane now; null when no pane holds it), by (human, shell, the adding pane's window id, link:HOST or plugin:ID), at, blocked (reason, by, at; null when not blocked) and fan (fan, fan_id) for a card added in a fan."
	// The fields of a card's row, which every verb that changes one returns.
	row := []verbParam{
		{Name: "id", Type: "string", Description: "The card."},
		{Name: "session", Type: "string", Description: "Its session."},
		{Name: "title", Type: "string", Description: "Its title."},
		{Name: "preview", Type: "string", Description: "The first line of its prompt, at most 80 characters."},
		{Name: "stage", Type: "string", Description: "Its stage.", Accepted: TaskStages},
		{Name: "stage_at", Type: "int", Description: "When it entered the stage, Unix nanoseconds."},
		{Name: "holder", Type: "object", Description: "The pane that holds it: window, name, harness, state (read from the pane now) and held_at. Null when no pane holds it."},
		{Name: "by", Type: "string", Description: "Who added it: human, shell, the adding pane's window id, link:HOST or plugin:ID."},
		{Name: "at", Type: "int", Description: "When it was added, Unix nanoseconds."},
		{Name: "blocked", Type: "object", Description: "reason, by and at when the card is blocked, else null."},
		{Name: "fan", Type: "object", Description: "fan and fan_id, for a card added in a fan's session."},
	}
	ownership := " A pane changes only the card it holds unless it holds admin; the person, a shell, a link and a plugin change any card."
	return map[string]verbEntry{
		"add-task": {
			description: "Add a task card to a session: one unit of work, with a title, the prompt for the pane that takes it, and a stage. The card is kept by the daemon and saved with the session, so it survives the pane that added it and a restart. Who added it (by) comes from the connection.",
			params: []verbParam{
				sessionParam,
				{Name: "title", Type: "string", Required: true, Description: "One line that says what the card is, at most 200 bytes. Control characters are dropped."},
				{Name: "prompt", Type: "string", Description: "What the pane that takes the card is to do, at most 16 KiB."},
				{Name: "stage", Type: "string", Description: "The stage it starts in.", Accepted: TaskStages, Default: TaskStageTodo},
				humanNonce,
			},
			returns: row,
			examples: []string{
				`{"id":1,"verb":"add-task","params":{"session":"work","title":"make the backoff jitter configurable","prompt":"Read api/retry.go and add a jitter option, with a test."}}`,
			},
			handler: (*Daemon).verbAddTask,
		},
		"list-tasks": {
			description: "List a session's task cards, oldest first.",
			params: []verbParam{
				sessionParam,
				{Name: "stage", Type: "string", Description: "Only the cards in this stage.", Accepted: TaskStages},
				{Name: "holder", Type: "string", Description: "Only the cards this window holds, by id or name."},
				{Name: "open", Type: "bool", Description: "Leave out the cards that are done.", Default: "false"},
				{Name: "all_sessions", Type: "bool", Description: "List the cards of every session on the daemon, in session name order. Takes no session or holder.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session listed. Absent with all_sessions."},
				{Name: "all_sessions", Type: "bool", Description: "True when every session was listed. Each row names its session."},
				{Name: "tasks", Type: "[]object", Description: "One row per card: " + rowFields},
			},
			examples: []string{
				`{"id":1,"verb":"list-tasks","params":{"session":"work","open":true}}`,
				`{"id":1,"verb":"list-tasks","params":{"all_sessions":true}}`,
			},
			handler: (*Daemon).verbListTasks,
		},
		"get-task": {
			description: "Read one task card whole: its row, its prompt and its history.",
			params:      []verbParam{sessionParam, id},
			returns: slices.Concat(row, []verbParam{
				{Name: "prompt", Type: "string", Description: "The prompt, as added."},
				{Name: "history", Type: "[]object", Description: "Every change, oldest first, at most 100 (the first is kept): action (added, claimed, finished, staged, blocked, unblocked, released), by, at, stage, holder and reason. A release the daemon made has by tuios and reason pane closed, agent left or restart. A stage the daemon moved on its own ([tasks] auto_stage) has by tuios and a reason naming the fact: plan held for approval, agent working, fan check started, fan check failed in every attempt, or the pull request opened, merged or closed."},
			}),
			examples: []string{
				`{"id":1,"verb":"get-task","params":{"session":"work","id":"t1"}}`,
			},
			handler: (*Daemon).verbGetTask,
		},
		"claim-task": {
			description: "Take a task card for a pane: the card id, or with next the oldest card no pane holds that is not blocked or done, and that no fan of another session works on (fan with task). A card in todo or plan moves to doing. The pick and the take are one step, so two panes claiming next at once take two cards. A pane claims only for itself unless it holds admin; outside a pane, name the window. The card is released when its pane closes, its agent leaves the pane, or the daemon restarts.",
			params: []verbParam{
				sessionParam,
				{Name: "id", Type: "string", Description: "The card to take. Not with next."},
				{Name: "next", Type: "bool", Description: "Take the oldest open card no pane holds. Not with id. With none to take, the call fails with no_task.", Default: "false"},
				{Name: "window", Type: "string", Description: "The pane that takes the card, by id or name. From a pane, omit it to claim for that pane. Required outside a pane."},
				humanNonce,
			},
			returns: row,
			examples: []string{
				`{"id":1,"verb":"claim-task","params":{"session":"work","next":true,"window":"build"}}`,
				`{"id":1,"verb":"claim-task","params":{"session":"work","id":"t1","window":"build"}}`,
			},
			handler: (*Daemon).verbClaimTask,
		},
		"finish-task": {
			description: "Mark a task card done. Its holder lets it go and its block, if any, is lifted. Finishing a card that is done already changes nothing." + ownership,
			params:      []verbParam{sessionParam, id, humanNonce},
			returns:     row,
			examples: []string{
				`{"id":1,"verb":"finish-task","params":{"session":"work","id":"t1"}}`,
			},
			handler: (*Daemon).verbFinishTask,
		},
		"set-task-stage": {
			description: "Move a task card to a stage by hand. The daemon also moves cards on its own ([tasks] auto_stage), only from the stages each fact moves a card from, so a card put in another stage by hand stays there. A move to done is a finish: the holder lets the card go, its block is lifted, and task-finished is sent. A move to the stage the card is in changes nothing." + ownership,
			params: []verbParam{
				sessionParam,
				id,
				{Name: "stage", Type: "string", Required: true, Description: "The stage to move the card to.", Accepted: TaskStages},
				humanNonce,
			},
			returns: row,
			examples: []string{
				`{"id":1,"verb":"set-task-stage","params":{"session":"work","id":"t1","stage":"review"}}`,
			},
			handler: (*Daemon).verbSetTaskStage,
		},
		"block-task": {
			description: "Mark a task card stuck, saying what it waits on, or with clear lift the mark. A blocked card keeps its holder and its stage, and claim-task next skips it. To let a card go, use release-task." + ownership,
			params: []verbParam{
				sessionParam,
				id,
				{Name: "reason", Type: "string", Description: "What the card waits on, at most 500 bytes. Required without clear."},
				{Name: "clear", Type: "bool", Description: "Lift the block. Not with reason.", Default: "false"},
				humanNonce,
			},
			returns: row,
			examples: []string{
				`{"id":1,"verb":"block-task","params":{"session":"work","id":"t1","reason":"waiting on the staging API key"}}`,
				`{"id":1,"verb":"block-task","params":{"session":"work","id":"t1","clear":true}}`,
			},
			handler: (*Daemon).verbBlockTask,
		},
		"release-task": {
			description: "Let a task card go without finishing it. It keeps its stage, and another pane can claim it." + ownership,
			params:      []verbParam{sessionParam, id, humanNonce},
			returns:     row,
			examples: []string{
				`{"id":1,"verb":"release-task","params":{"session":"work","id":"t1"}}`,
			},
			handler: (*Daemon).verbReleaseTask,
		},
		"remove-task": {
			description: "Remove a task card and its history. A pane needs the admin grant; the person and a shell remove any card.",
			params:      []verbParam{sessionParam, id, humanNonce},
			returns: []verbParam{
				{Name: "id", Type: "string", Description: "The card removed."},
			},
			examples: []string{
				`{"id":1,"verb":"remove-task","params":{"session":"work","id":"t1"}}`,
			},
			handler: (*Daemon).verbRemoveTask,
		},
	}
}
