# Claude Code agent-state integration

This shim reports Claude Code's per-turn state into tuios so a session can show,
at a glance, which panes need attention. It is the reference integration for the
tuios agent-state model; the same pattern works for any agent that can run a
command on a lifecycle event.

## What it does

`tuios-agent-state.sh` maps Claude Code lifecycle hooks to tuios agent states:

| Claude Code event               | tuios state   |
| ------------------------------- | ------------- |
| `SessionStart`                  | `working`     |
| `UserPromptSubmit`              | `working`     |
| `Notification`                  | `needs_input` |
| `Stop`                          | `done`        |

Subagent events (`SubagentStop`, and any event carrying an `agent_id`) are
ignored, so a Task-tool subagent finishing cannot reset the pane's displayed
state while the main turn is still running.

The state is set with `tuios set-agent-state`, the same verb any process can call
to report its own state. tuios draws a per-window indicator for it:

| State         | Indicator |
| ------------- | --------- |
| `working`     | `●`       |
| `needs_input` | `▲`       |
| `idle`        | `○`       |
| `done`        | `■`       |
| `errored`     | `×`       |
| `none`        | (nothing) |

An agent that reports nothing still gets a conservative fallback: a pane that
reported `working` but then produces no output for a while is demoted to `idle`
by the daemon. That fallback never overrides an explicit report. See
[Agent state](../../docs/AGENT_STATE.md).

## How it finds tuios

When tuios spawns a pane it exports the environment the shim guards on:

- `TUIOS_ENV=1` marks the process as running under tuios.
- `TUIOS_SOCKET` is the daemon socket path.
- `TUIOS_PANE_ID` is the pane's window id (also exported as `TUIOS_WINDOW_ID`).
- `TUIOS_SESSION` is the session name.

If any of these is missing the shim exits 0 without doing anything, so it is safe
to leave wired up when you run Claude Code outside tuios.

## Requirements

- `tuios` on `PATH` (the shim reports through the `tuios set-agent-state` verb).
- `python3` on `PATH` (used to parse the hook payload and filter subagents).

Both are checked; a missing one is a clean no-op, not an error.

## Wiring

1. Copy the shim somewhere stable, for example:

   ```sh
   mkdir -p ~/.config/tuios/integrations
   cp integrations/claude-code/tuios-agent-state.sh ~/.config/tuios/integrations/
   chmod +x ~/.config/tuios/integrations/tuios-agent-state.sh
   ```

2. Point Claude Code's hooks at it. In your Claude Code settings
   (`~/.claude/settings.json`), add the shim to the `SessionStart`,
   `UserPromptSubmit`, `Notification`, and `Stop` hooks. The shim reads the event
   from the hook payload on stdin, so the same command serves every event:

   ```json
   {
     "hooks": {
       "SessionStart": [
         { "hooks": [{ "type": "command", "command": "~/.config/tuios/integrations/tuios-agent-state.sh" }] }
       ],
       "UserPromptSubmit": [
         { "hooks": [{ "type": "command", "command": "~/.config/tuios/integrations/tuios-agent-state.sh" }] }
       ],
       "Notification": [
         { "hooks": [{ "type": "command", "command": "~/.config/tuios/integrations/tuios-agent-state.sh" }] }
       ],
       "Stop": [
         { "hooks": [{ "type": "command", "command": "~/.config/tuios/integrations/tuios-agent-state.sh" }] }
       ]
     }
   }
   ```

   This is tuios's own shim. It is separately named and does not read, write, or
   depend on any other tool's integration; add it alongside whatever else you
   already have wired up.

3. Run Claude Code inside a tuios pane. The pane's title indicator tracks the
   agent as it works, blocks for input, and finishes.

## Verifying by hand

You do not need Claude Code to check the wiring. From inside a tuios pane:

```sh
tuios set-agent-state working
tuios get-agent-state           # prints: working
tuios set-agent-state needs_input -m "awaiting approval"
tuios set-agent-state none      # clears it
```
