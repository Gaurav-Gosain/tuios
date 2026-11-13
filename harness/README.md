# harness

[![Go Reference](https://pkg.go.dev/badge/tuios.dev/harness.svg)](https://pkg.go.dev/tuios.dev/harness)

`tuios.dev/harness` recognises coding-agent CLIs and reads their state. It
is the detection core of [tuios](https://github.com/Gaurav-Gosain/tuios),
published for any Go program that runs agents in terminals: a multiplexer,
an orchestrator, a dashboard.

- **Which agent is this process?** From the process name, argv and
  executable path, including an agent started through node, bun, python or
  npx.
- **What is it doing?** working, idle, needs_input or limit_reached, from the
  last lines of the screen, the window title, the OSC 9;4 progress report or
  a desktop notification.
- **Why?** Every rule reports what it read and which strings were missing,
  so a wrong answer can be traced to one line of a manifest.

The rules are data: one TOML manifest per agent. 26 are bundled (Claude Code,
Codex, Gemini CLI, opencode, Copilot, Cursor, Amp, Crush, Goose, Kimi, Qwen,
Pi and more), and a person can drop a file in a folder to add or replace one
without a rebuild.

The package takes plain values and returns plain values. It reads no
terminal and starts no process. Its requirements are go-toml and
`charmbracelet/x/ansi` (for the cell width of wide characters).

## Install

```sh
go get tuios.dev/harness
```

The go command reads the import path from tuios.dev. Go 1.25 or later.

## Example

```go
reg, errs := harness.Load() // the bundled manifests; pass folders to add your own
if len(errs) > 0 {
	log.Print(errs) // one bad file costs only itself
}

id, ok := reg.Identify("claude", []string{"claude", "--model", "opus"}, "/usr/local/bin/claude")
// id = "claude-code", ok = true

screen := []string{ // the last non-empty lines of the pane
	"Bash(go test ./...)",
	"Do you want to proceed?",
	"❯ 1. Yes",
	"  2. Yes, and don't ask again for similar commands",
	"  3. No, and tell Claude what to do differently (esc)",
}
state, rule, ok := reg.Classify(id, screen)
// state = "needs_input"
fmt.Println(reg.RuleKind(id, rule))           // approval
fmt.Println(reg.RulePrompt(id, rule, screen)) // Do you want to proceed?
```

`Registry.ScreenLines(id)` says how many lines from the bottom a harness's
rules read. `Registry.Explain` gives the per-rule report behind a Classify
answer.

## A manifest

```toml
schema_version = 1
id = "my-agent"
display_name = "My Agent"

[detect]
comm = ["my-agent"]          # the process name
argv_path = ["@me/my-agent"] # an npm package run by node

[screen]
enabled = true
lines = 8

[[screen.rule]]
state = "needs_input"
message = "waiting for approval"
all = ["Allow this command?"]
any = ["(y/n)", "[Y/n]"]

[[screen.rule]]
state = "working"
regex = ['^[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] ']
```

The bundled manifests in `manifests/` are the full reference. The doc
comment of each type under `Manifest` describes its section. Rules that
claim idle must read the input box (`region = "prompt_box"`), and a bare
process name under five letters needs `[detect.require]`, so a manifest
cannot label an unrelated program as an agent.

`herdrconv` converts a herdr detection manifest into a draft:

```sh
go run tuios.dev/harness/herdrconv path/to/agent.toml > draft.toml
```

## Version

Version 0. The manifest format has its own version (`schema_version = 1`),
and a new rule kind is added without a new version. The Go API may still
change before v1.

## License

MIT. The manifests derived from herdr carry its Apache-2.0 notice in
`manifests/LICENSE-herdr`.
