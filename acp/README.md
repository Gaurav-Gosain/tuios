# acp

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/acp.svg)](https://pkg.go.dev/tuios.dev/acp)

`tuios.dev/acp` is a Go client for the
[Agent Client Protocol](https://agentclientprotocol.com) (ACP). An editor or a
terminal uses the protocol to drive a coding agent: it starts the agent,
opens a session, sends prompts and reads what the agent does as typed
updates. The messages are JSON-RPC 2.0, one per line, on the agent's stdin and
stdout.

The package has the parts a client needs:

- The JSON-RPC peer (`Conn`), with requests in both directions and
  `$/cancel_request`.
- Version negotiation (`Connect`). An agent asked for version 2 may answer 1.
  An agent that refuses 2 is started again through the transport and asked
  for 1. When no version fits, the error names the versions of both sides.
- Sessions: new, resume, list, prompt, cancel and close.
- Every `session/update` kind as a Go type, in one model for both versions.
  An unknown kind arrives as `*UnknownUpdate` with its raw JSON.
- Permission requests, answered with `Choose` or `Cancel`, and withdrawn by
  the agent with `$/cancel_request`.
- The stdio transport (`Process`) and a transport over a pair of streams
  (`Pipe`).

The package uses only the standard library. tuios uses it for its protocol
panes (`tuios start-agent --protocol acp`).

## Versions

| Version | Status in the spec | In this package |
|---------|--------------------|-----------------|
| 1 | stable | spoken, the default |
| 2 | draft | spoken when `ClientOptions.MaxVersion` is 2 |

The spec asks clients not to ask for version 2 by default while it is a
draft, so `DefaultVersion` is 1. `LatestVersion` is 2. The version 2 schema
can still change, and this package follows the draft of 2026-11-13.

Updates follow the version 2 model, which can say more. A version 1 update
arrives as the same type, with the fields version 1 cannot state left at
their zero value. In version 2 a prompt's answer only says the prompt was
taken; `Session.Prompt` waits for the `state_update` to idle that ends the
turn, so it returns at the end of the turn in both versions.

The client offers the agent no file system and no terminal, so a conforming
agent does its own reads, writes and commands. The agent side of the protocol
is not part of the package.

## Install

```sh
go get tuios.dev/acp
```

The go command reads the import path from tuios.dev. The package needs Go
1.25 or later.

## Run one prompt

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"tuios.dev/acp"
)

func main() {
	ctx := context.Background()
	agent := &acp.Process{Path: "opencode", Args: []string{"acp"}, Stderr: os.Stderr}
	c, err := acp.Connect(ctx, agent, acp.ClientOptions{
		Info: acp.Implementation{Name: "example", Version: "0.1.0"},
		OnUpdate: func(u acp.Update) {
			if m, ok := u.(*acp.MessageUpdate); ok && m.Role == acp.RoleAgent && m.Op == acp.MessageAppend {
				fmt.Print(m.Text())
			}
		},
		// Dismiss every permission request: the agent does nothing it
		// has to ask for.
		OnPermission: func(p *acp.PermissionRequest) { p.Cancel() },
	})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	cwd, _ := os.Getwd()
	s, err := c.NewSession(ctx, cwd)
	if err != nil {
		log.Fatal(err)
	}
	end, err := s.Prompt(ctx, acp.Text("Summarise the TODO comments in this folder."))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nstopped:", end.Reason)
}
```

Set `MaxVersion: 2` in the options to ask for version 2 first.

`OnUpdate` and `OnPermission` run on the connection's read goroutine, in the
order the agent sent the messages, and must not block. Answer a permission
request from any goroutine.

## Text from the agent

The package passes the agent's text through as it came. Text can hold
terminal escape sequences, so a program that draws it in a terminal must
remove them first.

## Tests

The golden tests under `testdata/golden` hold the exact JSON the client
writes and reads for a turn, a permission, a cancel, sessions and the version
negotiation, for both versions. Run them with:

```sh
go test ./...
```

`go test -run TestGolden -update ./...` rewrites the files after an intended
change of the wire.

## Licence

MIT. See [LICENSE](LICENSE).
