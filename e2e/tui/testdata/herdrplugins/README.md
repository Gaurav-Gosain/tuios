Stand-in herdr plugins for the e2e tests in herdr_plugins_test.go. Each one
writes what it saw into its HERDR_PLUGIN_STATE_DIR, which the test reads.

- hooks: a [[startup]] command that keeps running, and a pane.created hook.
- actions: an action that records its environment and prints a line.
- panes: a popup pane and a split pane that print a marker and wait.
- broken: a manifest herdr refuses, to show the error in tuios plugins list.
- hidden: an action of a plugin that stays off, which the palette must not offer.
