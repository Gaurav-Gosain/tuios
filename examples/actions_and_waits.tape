# Build a two-pane layout, run a command in each pane, and check the result.
#
# Run it in a session:   tuios tape exec examples/actions_and_waits.tape
# Or on its own:         tuios tape play examples/actions_and_waits.tape
#
# It waits on what the screen shows, never on a fixed sleep, and it stops at
# the first step that does not hold. tuios tape exec then exits non-zero and
# names the line.

DisableAnimations
EnableTiling

# Name the first pane and run something in it. The marker is computed by the
# shell, so the typed command line cannot satisfy the wait. Only output can.
RenameWindow "editor"
Run "echo ready-$((20+22))"
WaitFor text "ready-42" 15s

# Split the pane. WaitFor holds until the new pane is there.
Split vertical
WaitFor panes 2
RenameWindow "server"
Run "echo serving-$((40+2))"
WaitFor text "serving-42" in "server" 15s

# Any keybinding action runs by the name config.toml binds it to.
Action equalize_splits
FocusWindow editor
Expect focus "editor"

# Press keys through tuios's own key handling: the leader and a prefix key.
# ctrl+b ? opens the key help. Escape closes it.
Press "ctrl+b ?"
Sleep 1s
Press "esc"

Expect panes 2
EnableAnimations
