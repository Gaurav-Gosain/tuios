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

# A fixed password in a file that only you can read (mode 600 or 400)
mkdir -p ~/.config/tuios
(umask 077; head -c 18 /dev/urandom | base64 > ~/.config/tuios/web-password)
tuios-web --host 0.0.0.0 --auto-tls --password-file ~/.config/tuios/web-password
```

- The browser asks for a user name and a password. The user name is `tuios`.
  Use `--user` to change it.
- There is no flag that takes the password itself. Other users can see command
  line arguments in `ps`.
- The password file must belong to you. Its mode must be 600 or 400.
  tuios-web does not check the file permissions on Windows.
- tuios-web also reads the password from `TUIOS_WEB_PASSWORD`. It does not
  pass this variable to panes. But your own processes can still read it from
  `/proc`. Use `--password-file` instead.
- A host other than `localhost` needs a password. TLS encrypts the connection,
  but it does not check who connects. Use `--no-auth` only on a network you
  trust.
- On `localhost`, the password is optional. With no password, other users on
  this machine can connect, and tuios-web prints one line at start to say so.
  Use `--random-password` to stop this.
- On `localhost`, tuios-web accepts a session only when the Host header names
  this machine. This stops DNS rebinding.
- Only a page from tuios-web itself can show tuios-web in a frame. Other sites
  cannot put it in a frame.

### Behind a reverse proxy

A reverse proxy on this machine sends its own host name. Use `--allow-host` to
accept that name. Give the name with no port.

```bash
tuios-web --allow-host term.example.com --password-file ~/.config/tuios/web-password
```

- A proxied setup needs a password. The proxy lets the network in, so
  `--allow-host` does not start without a password or `--no-auth`.
- `--allow-host` works only with a loopback `--host`.
- With a password, sessions use WebSocket. A WebTransport connection carries
  no password, so tuios-web refuses it and the browser falls back to
  WebSocket.

## Window size limit

A browser window can be 1200 columns wide and 500 rows high at most. It can
also have 250000 cells at most. Each cell costs tuios-web memory. A window at
the limit costs up to about 325 MB. Several windows at the limit can still use
gigabytes together.

- tuios-web cuts a window that is too large down to the limit. The browser
  shows the smaller size.
- A window with too many cells keeps its columns and loses rows. A window that
  is 1200 columns wide gets 208 rows.

## Open the Inbox from a notification

A push notification from the `[notify]` table links to the Inbox item when
`notify.web_url` is the address of tuios-web. See
[Push notifications to your phone](CONFIGURATION.md#push-notifications-to-your-phone).

The link is `web_url/inbox?item=ID`. tuios-web answers it with a cookie that
names the item, and sends the browser to the page. When the page connects,
tuios opens the Inbox with the cursor on that item. Answer the item there as
at any other client.

- The link needs the same password as the page.
- The cookie holds only the item number. It expires after 60 seconds.
- An item that closed before you open the link leaves the cursor on the
  first item.
- Behind a reverse proxy, set `web_url` to the address with the proxy's path.
  The link sends the browser back to that path.
- With `--no-auth` and TLS, a browser can connect over WebTransport. Some
  browsers do not send the cookie there. Then the Inbox does not open by
  itself.
