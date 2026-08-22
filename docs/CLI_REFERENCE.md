# CLI Reference

This reference lives on the docs site: https://tuios.gaurav.zip/docs/cli-reference

The site page covers every command group: session management (`new`, `attach`, `ls`, `kill-session`, `resurrect`), the daemon (`daemon`, `start-server`, `kill-server`, `logs`), remote control (`send-keys`, `send-text`, `capture-pane`, `new-window`, `split-window`, `wait-for`, and the rest of the verb-backed commands), the agent-state group, `keybinds` (`list`, `list-custom`, `doctor`, `explain`), `tape`, `layout`, `config`, `list-themes`, `import-theme`, and `ssh`.

Two sources that cannot drift from the binary you have:

- `tuios list-verbs` describes the daemon's whole verb protocol, generated from the same tables the validator uses.
- `tuios --skill` prints the agent skill built into the binary; its examples are parsed against the real command tree by a test.
