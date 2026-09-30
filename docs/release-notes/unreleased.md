Changes on main since v0.8.2. This file becomes the notes of the next release.

## Breaking changes and migration

- Every pane now gets herdr's variables (`HERDR_ENV`, `HERDR_SOCKET_PATH`, `HERDR_PANE_ID`, `HERDR_BIN_PATH`). A Crush that you start from a shell prompt now reports its state. With this default, `herdr` refuses to start inside a tuios pane. Set `herdr_protocol = "agents"` in `[agents]` to run herdr nested. Other herdr commands, such as `herdr pane split`, return `unsupported`.

## Agents

- tuios accepts `pane.report_metadata` and `notification.show` from agents that report to herdr, as Crush does from charmbracelet/crush#3541.
- An agent that crashes without a release no longer keeps its state. The pane clears when the agent's process is gone and the pane is back at its shell prompt.
- If you installed herdr's hook scripts and tuios's integration for the same agent, such as Pi or opencode, tuios's integration wins. tuios drops the state reports from herdr's script while tuios's integration holds the pane.
