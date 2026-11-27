# Run a crew: keep its state in tuios

A lead that runs several workers keeps a work list, rules about the machine,
and conversations. Keep them in tuios, not only in your context. The daemon
keeps them for every pane, they outlive a context compaction, and a worker
that starts later sees them. `tuios --skill lead` has how to start, watch and
unstick a worker. This topic has the state of the crew.

| What | Keep it in | Full topic |
| --- | --- | --- |
| The work and who has it | Task cards | `tuios --skill tasks` |
| A rule about the machine, such as a quiet window | A hold | below |
| One message to many workers | A selector | `tuios --skill fleet` |
| A conversation with one worker | A mail thread | `tuios --skill mail` |

## The work list is task cards

Add one card for each unit of work, with the brief as its prompt:

```sh
tuios task add -s crew -p 'Read ~/notes/briefs/holds.md and do it.' holds verbs
tuios task ls -s crew --open
```

Tell each worker in its brief to take a card and finish it:

```sh
tuios task claim --next
tuios task done t1
```

`tuios task ls -s crew` shows each card's stage and the pane that holds it.
A card is let go when its pane closes, so a worker that went away does not
keep its work. Hear each change with
`tuios subscribe --types task-changed,task-finished`.

## Shared machine state is a hold

A hold is a named lock that every session of the daemon sees. Take one for a
quiet window, a shared tool that must not restart, or a pull request that a
landing train owns:

```sh
tuios hold take quiet-window --reason 'timing gates run' --ttl 4h
tuios hold ls
tuios hold release quiet-window
```

Tell each worker in its brief to check before a build, and to wait while the
hold is held:

```sh
tuios hold check quiet-window || tuios wait-for hold-released quiet-window --timeout 14400000
```

A worker that starts during the window sees the hold too. When you release
it, every waiting worker goes on, and you mail no one. A hold also ends when
its ttl ends or when your pane closes. `tuios list-agents` prints the holds
under its table. A check of a held hold fails with `held` and names the
holder and the reason.

## Tell many workers at once

A selector names every agent pane that fits it. The first send lists the
panes and sends nothing. Send again with the token it gives:

```sh
tuios list-agents --select 'session:crew'
tuios send-agent-message --select 'session:crew' 'main moved, rebase before you push'
tuios send-agent-message --select 'session:crew' --confirm 3f9a0c2b7d41e865 'main moved, rebase before you push'
```

The token names exactly the listed panes. A pane that joins or leaves in
between makes the send fail again with a new list.

## Wait for the whole crew

```sh
tuios wait-for agent-state --select 'session:crew' --until idle,done --every --timeout 3600000
```

`--every` waits until each matched pane is in one of the `--until` states.
Without it, the first pane to reach one ends the wait.

## Keep a conversation in a thread

Reply with `--reply-to`, so the answer joins the thread of the question:

```sh
tuios send-agent-message -s crew -w api-docs --from "$TUIOS_PANE_ID" --reply-to 12 'yes, push it'
tuios read-agent-messages -s crew --thread 12
tuios wait-for agent-message -w "$TUIOS_PANE_ID" --thread 12 --timeout 600000
```

Mail is not a record. A session keeps a limited number of messages, and it
drops older ones. Put a decision that must last in a file, and name the file
in the mail.

## After a context compaction

Read the state of the crew again from tuios:

```sh
tuios task ls -s crew
tuios hold ls
tuios list-agents --all-sessions
tuios read-agent-messages -w "$TUIOS_PANE_ID" --unread
```
