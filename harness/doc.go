// Package harness recognises coding-agent CLIs and reads their state, from
// data: one TOML manifest per agent (Claude Code, Codex, Gemini CLI, opencode
// and 22 more are bundled).
//
// It answers two questions a terminal multiplexer, an orchestrator or a
// dashboard has about a process running in a terminal:
//
//   - Which agent is it? Registry.Identify reads the process name, argv and
//     executable path the kernel reports, including an agent started through
//     node, bun, python or npx.
//   - What is it doing? Registry.Classify reads the last lines of the screen
//     and says working, idle, needs_input or limit_reached, with the rule
//     that decided. Registry.ClassifyTitle, ClassifyOSC and ClassifyNotify do
//     the same for the window title, the OSC 9;4 progress report and a
//     desktop notification.
//
// Every input is a plain value: a process name and argv, the screen as lines
// of text, a title string. The package reads no terminal and starts no
// process.
//
// A rule says why it matched. Registry.Explain reports, for each rule, the
// strings that were missing or present, so a manifest author can see why a
// rule did not fire. The fuzz target holds Classify and Explain to the same
// answer.
//
// A typical caller:
//
//	reg, errs := harness.Load(userDir) // bundled manifests, then the user's
//	id, ok := reg.Identify(comm, argv, exe)
//	state, rule, ok := reg.Classify(id, lastLines)
//	why := reg.RuleMessage(id, rule)
//
// The manifest format is versioned (SchemaVersion). A manifest in a user
// folder replaces the bundled one with the same id. A manifest that fails to
// load is reported in the LoadError list and the others still load. Each
// type under Manifest documents its section of the file.
//
// Screen rules are the last resort for state. They are written against one
// agent's TUI at one version, and a rule that stops matching gives no answer
// rather than a wrong one: an idle rule must prove an input box is on the
// screen, and a short bare process name must be backed by the executable.
// An agent that reports its own state (hooks, OSC 7501) is a better source.
//
// The bundled manifests are packed into manifests.tar.gz by gen.go. Run go
// generate in this folder after editing one.
package harness
