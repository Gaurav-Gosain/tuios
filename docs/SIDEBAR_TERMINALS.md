# Session-backed terminal panes in both sidebar rails

This document records the design implemented after the independent two-edge
rail foundation. A rail can either show its existing sections or display one
assigned **local daemon session**. It does not spawn client-owned shell
sections. See [CONFIGURATION.md](CONFIGURATION.md#show-a-daemon-session-in-each-rail)
for the user-facing setup.

## Compatibility and assignment

The original `[appearance.sidebar]` rail, `position`, `width`, `sections`,
scroll state, and `[appearance.sidebar.custom]` command retain their meanings.
`custom` is bounded command output, not a PTY. The opposite edge has its own
`enabled`, `width`, `sections`, and `custom` configuration and independent
render/cache/hit state; leaving its session name empty shows ordinary sections.
An existing single-rail configuration must not create a second rail or pane.

`[appearance.sidebar.left].session` and
`[appearance.sidebar.right].session` assign existing daemon sessions by name.
The assignments are independent of each other and of the central attachment;
connecting a rail must never replace the center's window list, focus, or
workspace. Settings exposes an independent session name, width, enabled state,
and workspace visibility for each edge. A session-backed edge displays its
panes in place of its ordinary sections, not alongside those sections; its
custom-section command does not run while the daemon pane is shown.

## Panes, geometry, and input

The visible, non-minimized panes on the assigned session's current workspace
stack vertically inside that rail. They are **ordinary daemon panes**: the
session owns their PTYs, output, focus, and process lifetime. The rail viewer
subscribes to each visible PTY, restores its snapshot, and announces each
pane's actual content dimensions. Each pane has its own framed border,
terminal input, cursor, mouse coordinates, and paste destination. With a rail
pane focused, the normal pane-create and split chords create panes in that
rail's daemon session; next/previous and close likewise target it. Every
pane keeps its normal title bar (including its configured title position),
showing its own pane name rather than the assigned session's ID. Clicking a
title focuses that pane. Its close and minimize buttons act on that daemon
pane, and zoom temporarily expands it within the rail; the assigned session
name remains in Settings. A narrow rail stacks even a sideways split
vertically rather than squeezing panes next
to each other. If the rail cannot give all panes at least six rows, it shows a
contiguous group containing the active pane; selecting another pane brings it
into view.

Both edge widths are considered before main-pane allocation. On a narrow host
screen the optional edge contracts or hides before the original edge, leaving
minimum space for the center. A rail attachment does not participate in daemon
size negotiation and never switches the center client's session. Rail clients
are passive for daemon window-manager command routing, not view-only: they
still subscribe and accept input. With no ordinary viewer, the session keeps
its last size.
The center and two rail streams and render invalidations are separate; one
rail's output or click must not leak into another pane.

A shell can leave leading empty rows in its own terminal grid, especially
after a resize. The rail displays these rows as they are, cropping the shared
daemon grid without resizing its PTY or shifting cursor and mouse coordinates.

## Visibility and lifecycle

`session_workspace = 0` (the default) follows central workspace changes;
`1`–`9` shows that edge only on the chosen central workspace. This is an
edge-level visibility rule, not a change to the assigned session's own
workspace. Hiding the edge or detaching the client closes only its viewer
connection and preserves the daemon session and programs. A missing or
recreated assigned session is retried without reconnecting the center.
Restarting the **daemon** is different: running pane processes end; saved
layouts can be restored with fresh shells. Right-click currently opens
Sidebar Settings; a separate pane-picker menu is not implemented.

## Delivery and verification

The first stage is the independent two-edge rail foundation (PR #600). This
session-backed stage replaces the earlier proposal for repeatable client-owned
`shell` sections: one assignment per edge, with as many normal daemon panes as
that session has. Real-client end-to-end tests cover two independent rail
sessions beside an unchanged center, stacked splits and per-pane input, active
pane navigation and overflow, mouse tracking, guest sizing, workspace
visibility, detach/reattach, missing-session retry, frame alignment, and
non-destructive display of leading guest whitespace. A live daemon must not be restarted
for a preview without explicit approval: its running programs would end.
