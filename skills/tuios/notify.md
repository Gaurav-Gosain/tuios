# Push notifications for the Inbox

The `[notify]` table in config.toml sends a push notification to the
person's phone when the Inbox gets an item that waits for them. The daemon
sends it, so it works with no client attached. The providers are ntfy,
Pushover and a webhook.

You do not need to send one. A `needs_input` report, an approval the Inbox
holds and an `ask-human` question already become Inbox items, and the person's
`[notify.triggers]` decide which of those reach the phone. Report your state
honestly and the person is told.

```sh
tuios notify test
tuios notify test --json
```

`notify test` sends one test notification through each provider and reports
the result for each. It runs in this process, not in the daemon, so a
`token_env` variable is read from this shell. The output never shows a token
or a full address.

What an agent must know:

- `[notify]` is not in `list-options`, and `set-config` cannot change it.
  Where the Inbox's text goes, and with which credentials, is the person's
  choice. Do not edit the table for them. A new address waits for
  `tuios config apply` from a terminal outside tuios, so an edit from a pane
  sends nothing new.
- A notification is held while the person typed at an attached client in the
  last `quiet_active_seconds` (120). One item sends one notification at most.
- A redirect to another host is refused, because the message and its token
  would go there. Tell the person to set the address the server redirects to.
- With `web_url` set, a notification links to the item in tuios-web.

For a message of your own on the person's phone, the person can add a hook
on `after-agent-state` (`tuios --skill recipes`).
