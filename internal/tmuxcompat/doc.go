// Package tmuxcompat is a tmux compatibility shim: it answers the subset of
// tmux commands that tools driving tmux issue (Claude Code agent teams first
// among them) by calling tuios verbs.
//
// The shim is opt-in. Nothing reaches it until a person runs
// `tuios tmux-shim`, which puts a `tmux` link to the tuios binary first on PATH
// and sets TMUX and TMUX_PANE for one command. A program under it then sees a
// tmux server whose one session is the caller's tuios session.
//
// The mapping:
//
//   - The tmux session is the caller's tuios session. Nothing the shim does can
//     name another one: a target naming another session is "can't find
//     session", as it is for a real tmux server that does not hold it.
//   - A tmux window is a tuios workspace, @N for workspace N.
//   - A tmux pane is a tuios window. Its id is %N, where N is a stable number
//     derived from the tuios window id (PaneNumber), so the same pane has the
//     same id in every call without the shim keeping any state.
//
// The shim grants no authority. It runs as the caller, dials the daemon socket
// the caller could dial with the tuios CLI, and calls verbs that CLI already
// exposes. What it adds is confinement: every target resolves inside the
// caller's own session.
//
// Commands the shim does not know, flags it does not accept, and format
// variables it cannot fill are recorded in a log (see Logger), so the subset
// can follow what the tools really send.
package tmuxcompat
