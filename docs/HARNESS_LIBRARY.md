# tuios.dev/harness

`tuios.dev/harness` is the agent CLI detection tuios uses to name the agent
in a pane and read its state. It is a Go module of its own in the `harness/`
folder, with no tuios import, so any Go program that runs agents in
terminals can use it: a multiplexer, an orchestrator or a dashboard.

The package README, [harness/README.md](../harness/README.md), has the
install line, an example that names an agent and classifies its screen, and
a sample manifest. The API is in the godoc comments, on
[pkg.go.dev/tuios.dev/harness](https://pkg.go.dev/tuios.dev/harness).
[AGENT_STATE.md](AGENT_STATE.md) describes the manifest format in full.

## What is in the library and what stays in tuios

The library reads. tuios acts.

| In `tuios.dev/harness` | In `internal/harness` |
|------------------------|-----------------------|
| The manifest format and its checks, every section | Where a person's manifests live (`UserDir`, `TUIOS_HARNESS_DIR`) |
| The 26 bundled manifests, packed by `go generate` | The prompt with its numbered options, and the keys a reply presses (`Prompt`, `Resolve`) |
| `Identify` from a process name, argv and executable | The resume command (`ResumeArgv`, `KeptFlags`, `RiskyFlags`) |
| `Classify` and `Explain` for the screen, title, OSC progress and notifications | The launch flags for a person's choice (`LaunchArgs`) |
| The prompt line, its kind, the rule message, `waits_on`, the input box draft | How a prompt is typed (`InputProfile`) |
| Where a harness keeps its transcripts | The permission ladder per harness (`PermissionOf`, `PermissionArgs`) |
| `herdrconv`, which drafts a manifest from herdr's | The pane label from a title, and the limit text |

`internal/harness.Registry` embeds the library's `Registry` and adds the
policy methods, and the package re-exports the library names tuios uses, so
the rest of tuios imports one package. What tuios does with a state beyond
that (the risk rules, the approval gate, the hooks, the Inbox) is in
`internal/risk`, `internal/session` and `internal/integration`, as before.

## Changing the library

The rules for every library in this repository apply: see "Go libraries in
this repository" in AGENTS.md. In short:

- The workspace (`go.work`) builds tuios against the `harness/` folder, so a
  change to a manifest or to the library and to tuios lands in one pull
  request.
- After editing a bundled manifest, run `go generate ./harness`. The test
  `TestBundledArchiveMatchesFiles` fails while the archive is stale.
- `go.mod` pins the library at a commit on main. After the library commit
  lands, move the pin with `GOWORK=off go get tuios.dev/harness@<commit>`
  and `scripts/library-pins.sh sync`, and put the new Nix `vendorHash` in
  `tuios.nix`. A manifest change is a library change, so it moves the pin
  too.
- Test it alone, as an importer gets it:
  `cd harness && GOWORK=off go test ./...`. The screen fixtures under
  `harness/testdata/screens` hold every bundled screen rule to a captured or
  derived screen.
- `BenchmarkClassify` is the check for the daemon's screen path, which runs
  Classify on every settle.
