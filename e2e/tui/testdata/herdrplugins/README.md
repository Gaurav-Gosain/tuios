Stand-in herdr plugins for the e2e tests in herdr_plugins_test.go. Each one
writes what it saw into its HERDR_PLUGIN_STATE_DIR, which the test reads.

- hooks: a [[startup]] command that keeps running, and a pane.created hook.
- actions: an action that records its environment and prints a line. Its
  manifest starts with a UTF-8 byte order mark and asks for herdr 0.9.5.
  Keep both: the tests show that such a manifest loads.
- panes: a popup pane and a split pane that print a marker and wait. The
  split pane tags the pane it was opened from with a metadata token.
- broken: a manifest herdr refuses, to show the error in tuios plugins list.
- hidden: an action of a plugin that stays off, which the palette must not offer.
  Its hello action has the id of the actions plugin's, so a key with the bare
  id hello must run the enabled plugin's action.
