# tuios.dev/acp

`tuios.dev/acp` is the Agent Client Protocol client that tuios uses for its
protocol panes (`tuios start-agent --protocol acp`). It is a Go module of its
own in the `acp/` folder, with no tuios import, so any Go program can use it
to drive a coding agent such as opencode or Gemini CLI.

The package README, [acp/README.md](../acp/README.md), has the install line,
the versions it speaks, and an example that starts an agent and runs one
prompt. The API is in the godoc comments, on
[pkg.go.dev/tuios.dev/acp](https://pkg.go.dev/tuios.dev/acp).

## What is in the library and what stays in tuios

| In `tuios.dev/acp` | In `internal/agentproto` |
|--------------------|--------------------------|
| The JSON-RPC 2.0 peer (`Conn`, `Request`, `RPCError`) | The pane program (`agent-proto`) and its transcript |
| Version negotiation (`Connect`, `VersionError`) | The agent state and the Inbox reports |
| Sessions: new, resume, list, prompt, cancel, close | The keys a person answers a permission with |
| Every `session/update` kind as a Go type | The Codex app-server client, which uses `acp.Conn` |
| Permission requests and their withdrawal | Starting the agent through `childproc`, with no controlling terminal (`agentproto.Command`) |
| `Process`, a stdio transport with `os/exec`, and `Pipe` | `[agents] acp_version` and `--acp-version` |

tuios does not use `acp.Process`. A daemon-reachable package starts a process
only through `internal/childproc` (see the Source checks in AGENTS.md), and a
protocol pane's agent must not get the pane's terminal, so tuios keeps its own
transport.

## Versions

Version 1 is the default. Version 2 is a draft of the spec, and the spec asks
clients not to ask for it by default until it is final. `acp.DefaultVersion`
is 1, `acp.LatestVersion` is 2, and `agentproto.DefaultACPVersion` follows
`acp.DefaultVersion`. When version 2 is final, the default moves in the
library, and tuios follows.

## Changing the library

The rules for every library in this repository apply: see "Go libraries in
this repository" in AGENTS.md. In short:

- The workspace (`go.work`) builds tuios against the `acp/` folder, so a
  change to the library and to `internal/agentproto` lands in one pull
  request.
- `go.mod` pins the library at a commit on main. After the library commit
  lands, move the pin with `GOWORK=off go get tuios.dev/acp@<commit>` and
  `scripts/library-pins.sh sync`, and put the new Nix `vendorHash` in
  `tuios.nix`.
- Test it alone, as an importer gets it:
  `cd acp && GOWORK=off go test ./...`.
- The golden files in `acp/testdata/golden` hold the exact wire JSON of both
  versions. `go test -run TestGolden -update ./...` in `acp/` rewrites them
  after an intended change of the wire.
