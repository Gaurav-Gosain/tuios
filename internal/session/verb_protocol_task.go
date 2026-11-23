package session

import (
	"slices"
)

// taskVerbs are the registry entries of the task verbs (verb_task.go).
func taskVerbs() map[string]verbEntry {
	id := verbParam{Name: "id", Type: "string", Required: true}
	humanNonce := verbParam{Name: "human_nonce", Type: "string"}
	// The fields of a card's row, which every verb that changes one returns.
	row := []verbParam{
		{Name: "id", Type: "string"},
		{Name: "session", Type: "string"},
		{Name: "title", Type: "string"},
		{Name: "preview", Type: "string"},
		{Name: "stage", Type: "string", Accepted: TaskStages},
		{Name: "stage_at", Type: "int"},
		{Name: "holder", Type: "object"},
		{Name: "by", Type: "string"},
		{Name: "at", Type: "int"},
		{Name: "blocked", Type: "object"},
		{Name: "fan", Type: "object"},
		{Name: "attachment_count", Type: "int"},
	}
	host := verbParam{Name: "host", Type: "string"}
	remote := []verbParam{
		{Name: "remote_window", Type: "string"},
		{Name: "remote_name", Type: "string"},
		{Name: "remote_harness", Type: "string"},
	}
	attachment := verbParam{Name: "attachment", Type: "object"}
	attachName := verbParam{Name: "name", Type: "string", Required: true}
	return map[string]verbEntry{
		"attach-task": {
			scope: scopeTask,
			link:  needWrite,
			params: append([]verbParam{
				sessionParam, id,
				{Name: "name", Type: "string"},
				{Name: "content", Type: "string"},
				{Name: "files", Type: "[]object"},
				{Name: "link", Type: "string"},
				{Name: "note", Type: "string"},
				{Name: "replace", Type: "bool", Default: "false"},
				{Name: "screenshot", Type: "object"},
				{Name: "capture", Type: "object"},
				{Name: "host", Type: "string"},
				humanNonce,
			}, remote...),
			returns: []verbParam{
				attachment,
				{Name: "attachments", Type: "[]object"},
				{Name: "unchanged", Type: "[]string"},
				{Name: "task", Type: "object"},
			},
			handler: (*Daemon).verbAttachTask,
		},
		"detach-task": {
			scope:   scopeTask,
			link:    needWrite,
			params:  append([]verbParam{sessionParam, id, attachName, humanNonce}, remote...),
			returns: row,
			handler: (*Daemon).verbDetachTask,
		},
		"get-task-attachment": {
			scope:  scopeRead,
			link:   needList,
			params: []verbParam{sessionParam, id, attachName},
			returns: []verbParam{
				attachment,
				{Name: "content", Type: "string"},
				{Name: "pr", Type: "object"},
				{Name: "pr_error", Type: "string"},
			},
			handler: (*Daemon).verbGetTaskAttachment,
		},
		"add-task": {
			scope: scopeTask,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				{Name: "title", Type: "string", Required: true},
				{Name: "prompt", Type: "string"},
				{Name: "stage", Type: "string", Accepted: TaskStages, Default: TaskStageTodo},
				{Name: "project", Type: "string"},
				{Name: "harness", Type: "string"},
				{Name: "ask_first", Type: "bool", Default: "false"},
				{Name: "machine", Type: "string"},
				humanNonce,
			},
			returns: row,
			handler: (*Daemon).verbAddTask,
		},
		"list-tasks": {
			// The task cards (task.go). The listing and one card read the session;
			// every change is the task grant's.
			scope: scopeRead,
			// The task cards. The listing carries titles and holders, which list
			// reaches; a card's prompt is what a pane will be told to do, so a
			// change needs write.
			link: needList,
			params: []verbParam{
				sessionParam,
				{Name: "stage", Type: "string", Accepted: TaskStages},
				{Name: "holder", Type: "string"},
				{Name: "open", Type: "bool", Default: "false"},
				{Name: "project", Type: "string"},
				{Name: "hosts", Type: "bool", Default: "false"},
				{Name: "all_sessions", Type: "bool", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string"},
				{Name: "all_sessions", Type: "bool"},
				{Name: "tasks", Type: "[]object"},
			},
			handler: (*Daemon).verbListTasks,
		},
		"get-task": {
			scope:  scopeRead,
			link:   needList,
			params: []verbParam{sessionParam, id},
			returns: slices.Concat(row, []verbParam{
				{Name: "prompt", Type: "string"},
				{Name: "attachments", Type: "[]object"},
				{Name: "history", Type: "[]object"},
			}),
			handler: (*Daemon).verbGetTask,
		},
		"claim-task": {
			scope: scopeTask,
			link:  needWrite,
			params: append([]verbParam{
				sessionParam,
				{Name: "id", Type: "string"},
				{Name: "next", Type: "bool", Default: "false"},
				{Name: "window", Type: "string"},
				humanNonce,
				host,
			}, remote...),
			returns: row,
			handler: (*Daemon).verbClaimTask,
		},
		"finish-task": {
			scope:   scopeTask,
			link:    needWrite,
			params:  append([]verbParam{sessionParam, id, humanNonce, host}, remote...),
			returns: row,
			handler: (*Daemon).verbFinishTask,
		},
		"set-task-stage": {
			scope: scopeTask,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				id,
				{Name: "stage", Type: "string", Required: true, Accepted: TaskStages},
				humanNonce,
			},
			returns: row,
			handler: (*Daemon).verbSetTaskStage,
		},
		"block-task": {
			scope: scopeTask,
			link:  needWrite,
			params: []verbParam{
				sessionParam,
				id,
				{Name: "reason", Type: "string"},
				{Name: "clear", Type: "bool", Default: "false"},
				humanNonce,
			},
			returns: row,
			handler: (*Daemon).verbBlockTask,
		},
		"release-task": {
			scope:   scopeTask,
			link:    needWrite,
			params:  append([]verbParam{sessionParam, id, humanNonce, host}, remote...),
			returns: row,
			handler: (*Daemon).verbReleaseTask,
		},
		"remove-task": {
			scope:  scopeTask,
			link:   needWrite,
			params: []verbParam{sessionParam, id, humanNonce},
			returns: []verbParam{
				{Name: "id", Type: "string"},
			},
			handler: (*Daemon).verbRemoveTask,
		},
	}
}
