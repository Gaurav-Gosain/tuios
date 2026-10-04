// Package herdrplugin reads herdr plugins and runs their commands for tuios.
//
// A herdr plugin (github.com/herdrdev/herdr) is a folder with a
// herdr-plugin.toml manifest. The manifest names commands, each an argv
// array: [[startup]] commands that run once when the server starts,
// [[actions]] a person runs from a palette or the CLI, [[events]] commands
// that run when a pane, tab or workspace changes, [[panes]] that open as a
// popup or a pane, and [[link_handlers]] that run an action on a clicked
// URL. The format followed is herdr 0.9.3's (src/app/api/plugins in herdr's
// source).
//
// This package holds the parts that need no daemon:
//
//   - Load parses and validates one manifest the way herdr does, with
//     herdr's error codes, so a manifest herdr refuses is refused here too.
//   - Discover lists the plugins on this machine: herdr's own registry and
//     managed checkouts (read only), the tuios plugins folder, and the
//     folders config.toml names. Listing never runs plugin code.
//   - Runner runs a command for a plugin: a detached process with no
//     terminal, its output kept in a bounded log, a bound on concurrent
//     runs, and a timeout that kills the process group.
//
// Trust is the caller's. Nothing here decides whether a plugin may run: the
// daemon (internal/session) runs only the plugins config.toml enables, and
// refuses every change to that list from a pane.
package herdrplugin
