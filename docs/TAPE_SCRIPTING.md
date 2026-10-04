# Tape Scripting

The tape DSL reference lives on the docs site: https://tuios.dev/docs/tape-scripting

It documents every keyword the parser accepts, timing, `tuios tape play`, `validate`, and `exec`. Recording happens in-app on `Ctrl+B T r`; see also [PROJECT_TAPES.md](PROJECT_TAPES.md) for `.tuios.tape` autorun and its trust boundary.

In short:

- `Action <name>` runs any keybinding action by the name `tuios keybinds list` prints, such as `Action toggle_spotlight`.
- `Press "ctrl+b ?"` presses keys through tuios's own key handling: the leader, prefixes, copy mode and dialogs.
- `WaitFor <condition> [timeout]` holds playback until a condition holds, and `Expect <condition>` checks one now. The conditions are `text "re" [in "pane"]`, `pane "name"`, `gone "name"`, `focus "name"`, `panes N`, `agent "state" [in "pane"]`, `workspace N` and `mode terminal|window`.
- `Run "cmd"` types a command line and presses Enter. `Source "file.tape"` includes another tape.
- The first command that fails stops the tape. `tuios tape exec` waits for the tape to end and exits non-zero with the file, line and column.

[examples/actions_and_waits.tape](../examples/actions_and_waits.tape) uses each of them.
