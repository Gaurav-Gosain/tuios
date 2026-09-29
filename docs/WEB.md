# Web Terminal

The `tuios-web` guide lives on the docs site: https://tuios.dev/docs/web

It covers install, TLS (`--auto-tls` and the `cert` subcommand), touch support, read-only mode, and every flag. The serving machinery is the [sip library](https://github.com/Gaurav-Gosain/sip).

When the session stops, the daemon stops, or the link to a remote host closes,
the browser shows a last message that says which one happened and when to
connect again. This needs sip v0.8.2 or newer, which tuios takes after v0.8.0.
Older builds could close the tab before the message arrived.

## Who can connect

Every browser that opens tuios-web gets a shell on this machine. The session
switcher reaches every session. Set a password to control who connects.

```bash
# A new password at each start. tuios-web prints it.
tuios-web --host 0.0.0.0 --auto-tls --random-password

# A password in a file that only you can read
(umask 077; head -c 18 /dev/urandom | base64 > ~/.config/tuios/web-password)
tuios-web --host 0.0.0.0 --auto-tls --password-file ~/.config/tuios/web-password

# A password in the environment
TUIOS_WEB_PASSWORD=... tuios-web --host 0.0.0.0 --auto-tls
```

- The browser asks for a user name and a password. The user name is `tuios`.
  Use `--user` to change it.
- There is no flag that takes the password itself. Other users can see command
  line arguments in `ps`.
- tuios-web removes `TUIOS_WEB_PASSWORD` from its environment before it starts
  panes.
- A host other than `localhost` needs a password. TLS encrypts the connection,
  but it does not check who connects. Use `--no-auth` only on a network you
  trust.
- On `localhost`, the password is optional. tuios-web accepts a session only
  when the Host header names this machine. This stops DNS rebinding. Use
  `--allow-host <name>` to add a name, for example for a reverse proxy.
- With a password, sessions use WebSocket. A WebTransport connection carries
  no password, so tuios-web refuses it and the browser falls back to
  WebSocket.
