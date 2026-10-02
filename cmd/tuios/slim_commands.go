package main

// slimDroppedCommands names every top-level command tuios-slim leaves out.
// The slim build adds a hidden stub under each name that prints one line
// saying so, in place of cobra's "unknown command". The full build does not
// use the list. TestSlimDroppedCommandsMatchFeatures holds it equal to what
// addFeatureCommands adds, so the two cannot drift.
var slimDroppedCommands = []string{
	"agent-hook", "agent-log", "agent-proto", "agent-statusline",
	"ask-agent", "ask-human", "doctor",
	"explain-agent-detect", "explain-agent-screen",
	"fan", "get-agent-state", "hosts", "integration",
	"list-agents", "list-attention", "mcp", "notification", "pane",
	"peek-prompt", "queue", "read-agent-messages", "respond",
	"resume-agent", "review", "run", "screenshot", "send-agent-message",
	"set-agent-meta", "set-agent-session", "set-agent-state",
	"ssh", "start-agent", "stash", "stdio-proxy", "subscribe",
	"tape", "tmux", "tmux-pane", "tmux-shim", "update", "worktree",
}
