# Binary Space Partitioning Tiling

TUIOS implements automatic window tiling using Binary Space Partitioning (BSP), an algorithm that recursively divides screen space to fit windows efficiently. Unlike rigid grid-based tiling, BSP adapts to any number of windows and allows fine-grained control over splits.

> **Note:** Throughout this document, `Ctrl+B` refers to the default leader key. This is configurable via the `leader_key` option in your config file.

## Table of Contents

- [Overview](#overview)
- [Basic Tiling](#basic-tiling)
- [BSP Concepts](#bsp-concepts)
- [Manual Split Control](#manual-split-control)
- [Preselection](#preselection)
- [Window Swapping](#window-swapping)
- [Resizing in BSP Mode](#resizing-in-bsp-mode)
- [Advanced Operations](#advanced-operations)
- [Practical Workflows](#practical-workflows)
- [Technical Implementation](#technical-implementation)

## Overview

BSP tiling automatically arranges windows in a tree structure where each node represents either a window or a split (horizontal or vertical). When you create a new window, TUIOS intelligently decides where to place it based on the current layout.

Key advantages over grid tiling:

- Works with any number of windows (not limited to 2, 4, 6, etc.)
- Each window can have a different size
- Full control over split direction at any level
- Persistent per-workspace configuration

## Basic Tiling

### Enable Tiling

In Window Management Mode, press:

```
t                    # Toggle tiling on/off
Ctrl+B Space         # Alternative: prefix + Space
```

Or use prefix commands:

```
Ctrl+B t t           # Via tiling prefix menu
```

The status bar shows "TILING" when enabled.

### Auto-Tiling Behavior

When tiling is enabled, TUIOS automatically positions new windows:

1. First window: Takes full screen
2. Second window: Splits vertically (side-by-side)
3. Third window: Splits horizontally (top/bottom on right side)
4. Fourth+ windows: Spiral pattern (alternating V/H splits)

This spiral layout balances screen space naturally as you add windows.

### Disable Tiling

Press `t` again to disable tiling. Windows remain in their current positions but can be dragged freely.

## BSP Concepts

### The Split Tree

Internally, TUIOS maintains a binary tree for each workspace:

```
Root Split (Vertical)
├── Window 1 (left half)
└── Split (Horizontal)
    ├── Window 2 (top-right quarter)
    └── Window 3 (bottom-right quarter)
```

Each split has a direction (vertical or horizontal) and a ratio determining how space is divided.

### Split Direction Indicators

The dock shows the next split direction when tiling is active:

- `V` - Next window splits vertically (left/right)
- `H` - Next window splits horizontally (top/bottom)

This helps you predict where the next window will appear.

### Per-Workspace State

Each workspace maintains its own BSP tree. Switching workspaces preserves the tiling configuration on both sides.

## Manual Split Control

Rather than relying on automatic placement, you can manually split the focused window.

### Create Splits

In Window Management Mode with a window focused:

```
Ctrl+B -             # Split horizontally (current window → top/bottom)
Ctrl+B | or \        # Split vertically (current window → left/right)
```

This divides the focused window's space in half, placing the focused window in one half and preparing space for a new window in the other half.

**Example workflow:**

```
# Create first window
n

# Split it vertically
Ctrl+B |

# Create new window (appears in right half)
n

# Focus left window and split horizontally
Tab
Ctrl+B -

# Create new window (appears below left window)
n
```

Result: Three windows in an L-shaped layout.

### Rotate Split Direction

To change an existing split's direction:

```
Ctrl+B R             # Rotate split at focused window
```

This toggles the split containing the focused window between vertical and horizontal. Useful for reorganizing layouts without recreating windows.

**Example:**

Start with side-by-side windows (vertical split):

```
┌─────┬─────┐
│  A  │  B  │
└─────┴─────┘
```

Focus either window and press `Ctrl+B R`:

```
┌───────────┐
│     A     │
├───────────┤
│     B     │
└───────────┘
```

Now they're stacked (horizontal split).

## Preselection

Preselection lets you control where the next window spawns relative to the focused window. Think of it as pre-deciding the split direction before creating a window.

### Preselection Commands

In Window Management Mode:

```
Ctrl+B Shift+H       # Next window appears left of focused
Ctrl+B Shift+L       # Next window appears right of focused  
Ctrl+B Shift+K       # Next window appears above focused
Ctrl+B Shift+J       # Next window appears below focused
```

After preselecting, create a window normally with `n`. The preselection is consumed and resets.

### Preselection Workflow

**Example: Creating a sidebar layout**

```
# Start with one window
n

# Preselect left
Ctrl+B Shift+H

# Create narrow sidebar on left
n
# (resize the split to make sidebar narrow)
<

# Focus main window and preselect below
Tab
Ctrl+B Shift+J

# Create terminal below main area
n
```

Result: Sidebar on left, main area on right with terminal below it.

### Preselection vs Auto Placement

Without preselection, TUIOS uses its default spiral algorithm. With preselection, you override this for precise control.

Preselection is particularly useful for:

- Creating asymmetric layouts
- Building UI patterns (sidebar + main + footer)
- Inserting windows into specific locations in complex layouts

## Window Swapping

Once windows are tiled, you can rearrange them without changing the underlying split structure.

### Swap Commands

In Window Management Mode:

```
Shift+H or Ctrl+Left      # Swap with window to the left
Shift+L or Ctrl+Right     # Swap with window to the right
Shift+K or Ctrl+Up        # Swap with window above
Shift+J or Ctrl+Down      # Swap with window below
```

Swapping exchanges two windows' positions in the BSP tree while preserving their sizes.

### Swap vs. Focus

Note the difference:

- `h`/`j`/`k`/`l` or arrow keys: Move focus between windows
- `Shift+H`/`J`/`K`/`L` or `Ctrl+arrows`: Swap windows

### Drag to Swap

Mouse users can drag windows in tiling mode. Dragging a window onto another swaps them.

## Resizing in BSP Mode

BSP allows resizing along any split boundary.

### Master Ratio Adjustment

The master ratio controls the size of the main window area (typically the leftmost window):

```
> or Shift+.             # Grow master area (from right edge)
< or Shift+,             # Shrink master area (from right edge)

. (period)               # Grow master area (from left edge)
, (comma)                # Shrink master area (from left edge)
```

Adjustment increment: 5% per keypress.

### Height Adjustment

Control the vertical split ratio of the focused window:

```
} or Shift+]             # Grow height (from bottom edge)
{ or Shift+[             # Shrink height (from bottom edge)

] (right bracket)        # Grow height (from top edge)
[ (left bracket)         # Shrink height (from top edge)
```

### Edge-Based Resizing

The distinction between left/right and top/bottom edge resizing matters:

**From right edge (`<` / `>`):**
- Moves the right boundary of the master area
- Affects right-side windows

**From left edge (`,` / `.`):**
- Moves the left boundary of the master area  
- Affects left-side windows

This allows fine control in complex layouts where multiple splits exist.

### Resize Behavior

Resizing modifies the split ratio at the nearest split node. TUIOS walks up the tree to find the appropriate split to adjust.

For example, if you have:

```
┌────┬────┐
│ A  │ B  │
├────┼────┤
│ C  │ D  │
└────┴────┘
```

With window B focused, pressing `>` grows the right column (B and D together) at the expense of the left column (A and C).

## Advanced Operations

### Equalize Splits

To reset all splits to equal proportions:

```
Ctrl+B =                 # Equalize all splits
```

This sets all split ratios to 50/50 throughout the entire tree, giving all windows equal space based on their position in the tree.

Useful when you've made many resize adjustments and want to start fresh with balanced spacing.

### Manual Layout Persistence

When you manually create splits and preselect positions, TUIOS marks the workspace as having a "custom layout". This layout persists even when you close windows.

To fully reset a workspace's layout:

1. Close all windows
2. Toggle tiling off and on
3. Create new windows

### Mixing Tiling and Floating

You can disable tiling to temporarily drag windows freely, then re-enable tiling. Windows return to their tiled positions.

This is useful for:

- Quickly maximizing a window for focus (press `f`)
- Moving a window to a second monitor temporarily
- Overlapping windows for comparison

## Practical Workflows

### Workflow 1: Development Environment

Classic three-pane layout (editor + terminal + docs).

```
# Start tiling
t

# Create editor window
n
i
vim main.go
Enter
Ctrl+B d

# Preselect right for terminal
Ctrl+B Shift+L
n
i
go run .
Ctrl+B d

# Focus terminal and preselect below for logs
Tab
Ctrl+B Shift+J
n
i
tail -f logs/app.log
```

Result:

```
┌──────────┬──────────┐
│          │ Terminal │
│  Editor  ├──────────┤
│          │   Logs   │
└──────────┴──────────┘
```

### Workflow 2: Wide Monitor Layout

For ultrawide monitors, create a three-column layout.

```
t
n                        # First window (full)
Ctrl+B |                 # Split vertically
n                        # Second window (right)
Tab                      # Focus first (left)
Ctrl+B |                 # Split left side vertically
n                        # Third window (middle)

# Adjust widths for 1:2:1 ratio
Tab Tab                  # Focus rightmost
<<                       # Shrink right column
Tab Tab                  # Focus leftmost  
..                       # Shrink left column
```

Result: Left sidebar, main area, right sidebar.

### Workflow 3: Dashboard Grid

Four equal quadrants.

```
t
n
n
n
n
Ctrl+B =                 # Equalize all splits
```

Result:

```
┌─────┬─────┐
│  1  │  2  │
├─────┼─────┤
│  3  │  4  │
└─────┴─────┘
```

### Workflow 4: Presenter Layout

Main presentation area with hidden notes.

```
t
n                        # Main window
Ctrl+B Shift+J           # Preselect below
n                        # Notes window
{{{{{                    # Shrink notes to minimal height
```

Result: Full screen presentation with a thin strip of notes at bottom.

### Workflow 5: CI/CD Monitor

Vertically stacked log viewers.

```
t
n                        # First log
Ctrl+B -                 # Split horizontally
n                        # Second log
Ctrl+B -                 # Split horizontally again
n                        # Third log
Ctrl+B =                 # Equal height
```

Result: Three equal horizontal strips.

## Technical Implementation

### BSP Tree Structure

Each workspace maintains a `BSPTree` with nodes of two types:

- **Leaf nodes**: Contain a window ID
- **Internal nodes**: Contain split direction (H/V), ratio (0.0-1.0), and two child nodes

### Automatic Scheme

Default scheme: `layout.AutoScheme` set to spiral pattern.

When auto-tiling:

1. First window → root leaf
2. Second window → root becomes vertical split, two leaves
3. Third+ windows → finds the largest window, splits it (alternating directions)

### Split Ratio Storage

Each internal node stores a ratio (default 0.5). This represents the fraction of space given to the left/top child. Ratio range: 0.3 to 0.7 (enforced to prevent unusable layouts).

### Window-to-Node Mapping

Windows are assigned stable integer IDs for BSP tracking (independent of UUID). This mapping persists across window focus changes and allows the BSP tree to track windows even as they're rearranged.

### Tree Modification Operations

When you perform operations:

- **Split**: Replaces leaf node with internal node containing two new leaves
- **Swap**: Exchanges leaf nodes at two tree positions  
- **Rotate**: Changes internal node's direction flag
- **Equalize**: Walks tree, sets all ratios to 0.5
- **Resize**: Finds nearest parent split in the resize direction, adjusts ratio

### Layout Application

After tree modifications, TUIOS walks the tree recursively, calculating rectangles:

1. Start with workspace dimensions
2. At each split node, divide rectangle by ratio and direction
3. At each leaf node, assign rectangle to corresponding window
4. Apply all window geometry in a single batch

This prevents flicker and ensures atomic layout updates.

### State Persistence

Each workspace stores:

- `WorkspaceTrees[workspaceNum]` - BSP tree structure
- `WorkspaceMasterRatio[workspaceNum]` - Master ratio for resizing
- `WorkspaceHasCustom[workspaceNum]` - Whether user made manual splits

When you switch workspaces, trees are preserved. When daemon mode is active, BSP state is serialized and restored across sessions.

## Troubleshooting

### Windows Not Tiling

**Check tiling is enabled:** Status bar should show "TILING". Press `t` to toggle.

**Check workspace:** Each workspace has independent tiling state. Switching workspaces may land you on a workspace with tiling disabled.

**Too many windows:** BSP handles any number of windows, but with 10+ windows, individual window size becomes impractical. Consider using multiple workspaces.

### Unexpected Layout After Resize

**Reset to balanced layout:**
```
Ctrl+B =                 # Equalize all splits
```

**Verify focused window:** Resize operations affect the split containing the focused window. Make sure the correct window is focused.

### Swap Not Working

**Check direction:** Swap commands are directional. If there's no window in the specified direction, nothing happens.

**Try mouse drag:** Mouse dragging always swaps if you drag one window onto another.

### Preselection Not Applied

**Preselection is one-time:** After creating a window, preselection resets. You need to preselect again for the next window.

**Cancel preselection:** Press `Esc` in Window Management Mode to cancel a preselection without creating a window.

### Split Direction Confusion

**Check dock indicator:** The dock shows `V` or `H` for the next split direction when tiling is active.

**Visualize the tree:** Run `Ctrl+B D l` to see debug logs, which include BSP tree structure.

## Related Documentation

- [Keybindings Reference](KEYBINDINGS.md) - Complete keybinding list for tiling
- [Configuration Guide](CONFIGURATION.md) - No BSP-specific configuration currently, but keybindings are customizable
- [Architecture Guide](ARCHITECTURE.md) - Technical details on BSP implementation (see `internal/layout/bsp.go`)

## Comparison with Other Tiling Algorithms

### BSP vs. Grid

**Grid tiling (not used in TUIOS):**

- Fixed positions (1, 2, 3, 4... windows fit in grid cells)
- Wasted space with odd numbers of windows
- Predictable but inflexible

**BSP tiling (TUIOS):**

- Recursive splits adapt to any number
- No wasted space
- Full control over layout

### BSP vs. Manual Tiling

Some window managers require you to manually tile every window. TUIOS offers both:

- Auto-tiling for quick layouts
- Manual splits and preselection for precise control

You can mix approaches: start with auto-tiling, then use manual splits to refine.

## Future Enhancements

Potential future additions to BSP tiling:

- Named layouts (save/restore BSP configurations)
- Per-workspace split defaults
- Visual split indicators in the terminal
- Ratio presets (e.g., 30/70 split for sidebar layouts)

Check the [GitHub issues](https://github.com/Gaurav-Gosain/tuios/issues) for tracking and discussion.
