# Sidebar terminal areas (implementation plan)

This document records the contract for the two-edge sidebar work. It is a design
for an incremental implementation, not a description of a shipped feature.

## Existing rail compatibility

The existing `[appearance.sidebar]` rail, its `position`, `width`, `sections`,
scroll/cursor state, and its `[appearance.sidebar.custom]` command keep their
current meanings. In particular `custom` is still bounded command output,
not a PTY. Old configuration must not create a second edge or a shell.

A sidebar terminal is a separate *view* of an edge, not a section within a
rail. Each edge independently selects its rail or terminal view (or is hidden).
The rail and its custom command remain available after switching back. A
terminal process is not stopped when its view is hidden. Terminal areas on both
edges may be shown simultaneously, including while the existing rail occupies
the other edge. Configured commands must be read from the config file, not from
a settable option callable by an untrusted pane.

The first implementation should keep the existing rail on at most one edge;
rendering two independent copies of it is a separate decision. If a future
version supports two rails, each needs its own layout, cache, hit geometry,
scroll/cursor, width, and custom command identity. They can read the same
session tree, but must not share mutable presentation state. Duplicating a
section's name on two edges must not trigger an action on the wrong edge or
run a command twice by accident.

## Terminal ownership and geometry

Each edge owns a vertical stack of PTY-backed terminal shells, split and
resized independently of the other edge and the main pane layout. The two
edges' widths must be considered together before allocating either: enforce
a minimum main-pane width against `left + right`, and hide or contract the
optional edge before shrinking the existing rail. A hidden terminal view may
continue running without reserving columns. The edge-facing resize and the
context menu must know which edge the pointer is in; a second edge cannot use
the existing singleton `SidebarHits` and `SidebarEdge` fields.

Terminal PTYs are owned by the client-side terminal-area model, not injected
into the daemon's main-window list or tiling tree. This matches the requested
lifetime: the processes survive view changes and workspace switches, not a
TUIOS session restart. Closing a shell or ending the client must clean up its
PTY. Resizing a stack must update its PTYs' cell dimensions. Input, output
notifications, mouse tracking, paste, and focus must route through the same
terminal primitives as normal local panes, with an explicit active edge/split
so no bytes leak into a main pane or the other edge.

## Workspace pins

An unpinned shell follows the user through workspace switches. Pinning binds
it to a workspace number: it is visible on that workspace and remains alive
but hidden on the others. Each split has its own pin. A right-click on a shell
split offers pin/unpin and workspace selection; the same operations must be
available from the keyboard. Moving focus to a sidebar shell must not itself
change its workspace association. If an edge has no shells visible on a
workspace, the area can show its rail if one is configured there, otherwise
collapse without leaving a blank reserved band.

Pinning to individual main terminals (focus-follow) is outside the initial
scope: it is a distinct opt-in visibility rule, not another interpretation of
workspace pinning.

## Delivery order

1. Land the two-edge geometry/input foundation without changing the default
   single-rail experience. Cover both narrow screens and left/right mouse
   routing with end-to-end checks.
2. Add independent terminal areas and vertical splits, with a repeatable
   end-to-end artifact showing simultaneous left and right shells and preserved
   rail/custom behavior.
3. Add workspace pin/follow visibility and right-click/keyboard parity.

Use a separate branch and pull request per stage, based on the preceding
stage only until that stage merges. A pull request must not claim to implement
later stages because its design mentions them.
