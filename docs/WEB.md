# Web Terminal

The `tuios-web` guide lives on the docs site: https://tuios.dev/docs/web

It covers install, TLS (`--auto-tls` and the `cert` subcommand), touch support, read-only mode, and every flag. The serving machinery is the [sip library](https://github.com/Gaurav-Gosain/sip).

When the session stops, the daemon stops, or the link to a remote host closes,
the browser shows a last message that says which one happened and when to
connect again. This needs sip v0.8.2 or newer, which tuios takes after v0.8.0.
Older builds could close the tab before the message arrived.
