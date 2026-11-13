// Package acp is a client for the Agent Client Protocol
// (https://agentclientprotocol.com), the protocol an editor or a terminal
// speaks to a coding agent: JSON-RPC 2.0, one message per line, over the
// agent's stdin and stdout.
//
// The package speaks versions 1 and 2. Version 2 is a draft of the spec,
// which asks clients not to ask for it by default until it is final, so a
// client asks for DefaultVersion (1) unless ClientOptions.MaxVersion says 2.
// Connect negotiates: an agent asked for 2 may answer 1, and an agent that
// refuses 2 is started again through the Transport and asked for 1.
//
// Updates arrive in one model for both versions. The types follow version 2,
// which can say more: a version 1 update arrives as the same type with the
// fields version 1 cannot state left at their zero value.
//
// A typical client:
//
//	agent := &acp.Process{Path: "opencode", Args: []string{"acp"}}
//	c, err := acp.Connect(ctx, agent, acp.ClientOptions{
//		Info:     acp.Implementation{Name: "example", Version: "0.1.0"},
//		OnUpdate: show,
//	})
//	s, err := c.NewSession(ctx, cwd)
//	end, err := s.Prompt(ctx, acp.Text("Summarise the TODOs."))
//
// The client offers the agent no file system and no terminal, so a
// conforming agent does its own reads, writes and commands, and asks
// permission through ClientOptions.OnPermission. It does not serve the agent
// side of the protocol.
//
// Conn is the JSON-RPC peer underneath, exported for a protocol of the same
// shape: a newline-delimited JSON-RPC 2.0 peer with requests both ways.
//
// The package uses only the standard library.
package acp
