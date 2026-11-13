// Package agentproto runs a coding agent headless, over a structured protocol,
// and shows it in a tuios pane.
//
// An agent TUI in a pane is read by its screen, its hooks and its transcript
// file. Some agents also speak a protocol meant for exactly this: a client
// starts the agent with pipes, sends it prompts, and gets its turns, tool calls,
// diffs and permission requests as messages. This package is such a client for
// two of them:
//
//   - ACP, the Agent Client Protocol (agentclientprotocol.com), versions 1 and
//     2, which Zed and others use and which many agents implement (opencode
//     acp, Gemini CLI, adapters for Claude Code and Codex). The protocol itself
//     is the library tuios.dev/acp, in the acp/ folder of this repository;
//     this package maps what it delivers to the pane.
//   - The Codex app-server protocol (codex app-server), its v2 thread and turn
//     methods.
//
// Both are JSON-RPC 2.0 over the agent's stdin and stdout, one message per
// line. The Codex app-server leaves out the "jsonrpc" member, and reads a
// message without it.
//
// The client offers the agent nothing beyond the conversation: it advertises no
// file system and no terminal capability, so the agent cannot ask tuios to read
// a file, write one or run a command, and a request for anything the client
// does not handle is answered with method not found. What the agent does, it
// does itself, under its own sandbox and approval rules, exactly as it would in
// its own TUI.
//
// The Session in session.go is the pane program: it renders the conversation as
// a transcript, reads prompts from the keyboard, and reports the agent's state
// and permission requests to the daemon through a Reporter.
package agentproto
