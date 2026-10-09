# Repeatable sidebar shell sections (implementation plan)

This document records the contract for the two-edge sidebar work. It is a design
for an incremental implementation, not a description of a shipped feature.

## Existing rail compatibility

The existing `[appearance.sidebar]` rail, its `position`, `width`, `sections`,
scroll/cursor state, and its `[appearance.sidebar.custom]` command keep their
current meanings. In particular `custom` is still bounded command output,
not a PTY. Old configuration must not create a second edge or a shell.

A sidebar shell is a **repeatable section** in an edge's ordered `sections`
list, like `spacer`, not a separate view that replaces the rail. For example,
`sections = "sessions,shell,spacer,shell,custom"` contains two different live
shells alongside existing sections. Both edges can independently repeat
`shell`; entries never share a PTY. A hidden shell keeps running until its
client exits or the user closes it. Configured shell commands must be read
from the config file, not from a settable option callable by an untrusted pane.

Both edges must support independently configured rails. The legacy rail
retains its current position and settings; the other edge needs its own
layout, cache, hit geometry, scroll/cursor, width, and custom command
identity. They can read the same session tree, but must not share mutable
presentation state. Duplicating a section's name on two edges must not
trigger an action on the wrong edge or run a command twice by accident.

## Terminal ownership and geometry

Each `shell` occurrence owns a PTY-backed terminal; its vertical section can
be sized independently without changing the main pane layout or the other
edge. The two edges' widths must be considered together before allocating
either: enforce a minimum main-pane width against `left + right`, and hide or
contract the optional edge before shrinking the existing rail. Shells hidden
by layout or workspace changes keep running without reserving columns. The
edge-facing resize and context menu must know which edge and section instance
the pointer is in; a second edge cannot use the existing singleton
`SidebarHits` and `SidebarEdge` fields.

Terminal PTYs are owned by each client-side terminal-area model, not injected
into the daemon's main-window list or tiling tree. Two clients attached to the
same session have separate sidebar shells. This matches the requested
lifetime: the processes survive view changes and workspace switches, but end
when their client exits. Closing a shell or ending the client must clean up its
PTY. Resizing a section must update its PTY's cell dimensions. Input, output
notifications, mouse tracking, paste, and focus must route through the same
terminal primitives as normal local panes, with an explicit active edge and
section instance so no bytes leak into a main pane or the other edge.

## Workspace pins

An unpinned shell follows the user through workspace switches. Pinning binds
it to a workspace number: it is visible on that workspace and remains alive
but hidden on the others. Each shell instance has its own pin. A right-click
on a shell section offers pin/unpin and workspace selection; the same
operations must be available from the keyboard. Moving focus to a sidebar
shell must not itself change its workspace association. If an edge has no shells visible on a
workspace, its other sections stay visible; the pinned shell's slot need not
reserve blank lines on that workspace.

Pinning to individual main terminals (focus-follow) is outside the initial
scope: it is a distinct opt-in visibility rule, not another interpretation of
workspace pinning.

## Delivery order

1. Land independent two-edge rail configuration, rendering, state, geometry,
   and input without changing the default single-rail experience. Cover
   custom sections on both edges, narrow screens, and left/right mouse routing
   with end-to-end checks.
2. Add repeatable `shell` sections with independent PTYs and vertical sizing,
   with a repeatable end-to-end artifact showing multiple shells in one rail,
   simultaneous shells on both edges, and preserved rail/custom behavior.
3. Add workspace pin/follow visibility and right-click/keyboard parity.

Use a separate branch and pull request per stage, based on the preceding
stage only until that stage merges. A pull request must not claim to implement
later stages because its design mentions them.
