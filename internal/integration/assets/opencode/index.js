// installed by tuios
// managed by tuios; `tuios integration install opencode` overwrites this file
// TUIOS_INTEGRATION_ID=opencode
// TUIOS_INTEGRATION_VERSION=__TUIOS_VERSION__

// OpenCode discovers the adjacent tui.js through this server entrypoint.
// Reporting belongs to the terminal client: the shared server may have been
// started outside tuios, or inherited a different pane's environment.
export default {
  id: "tuios-agent-state",
  async server() { return {}; },
  setup() {},
};
