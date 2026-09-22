# Claude Code agent-state integration

The Claude Code reporter is built into tuios. Install it with:

```sh
tuios integration install claude-code
tuios integration status claude-code   # installed, current (v1)
```

That writes one managed hook entry per event into `~/.claude/settings.json` (or
`$CLAUDE_CONFIG_DIR/settings.json`), each running
`tuios agent-hook claude-code --integration 1`. Your own settings and hooks are
kept where they are, the file is replaced atomically, and the previous copy is
kept as `settings.json.tuios.bak`. `tuios integration uninstall claude-code`
removes exactly the entries tuios wrote.

The event map, the pane lookup and the nested-session guard are documented in
[Agent state](../../docs/AGENT_STATE.md#harness-integrations).

## The old shim

`tuios-agent-state.sh` in this directory used to be the integration: a shell
script that parsed the payload with python3 and mapped every `Notification` to
`needs_input`. It is now a two-line wrapper that runs `tuios agent-hook
claude-code`, so settings that still point at it keep working and get the
corrected map.

Do not wire both. With the shim and an installed integration on the same events,
every event is reported twice. `tuios integration status` and `tuios doctor
agents` say so when they find the shim in `settings.json`. Remove the shim's
entries and keep the installed ones.

## Verifying by hand

You do not need Claude Code to check the wiring. From inside a tuios pane:

```sh
echo '{"hook_event_name":"PermissionRequest","session_id":"s1","tool_name":"Bash","tool_input":{"command":"make"}}' \
  | tuios agent-hook claude-code --explain
tuios get-agent-state --json     # needs_input, blocked_by approval, agent_session_id s1
echo '{"hook_event_name":"Stop","session_id":"s1"}' | tuios agent-hook claude-code
tuios get-agent-state            # done
tuios set-agent-state none       # clear it
```
