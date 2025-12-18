# TUIOS Enhanced Demo Tape Script
# This script demonstrates the enhanced scripting language with explicit mode switching
# and full window management capabilities

# Disable animations for consistent playback
DisableAnimations

# Start in window management mode (default)
WindowManagementMode
Sleep 500ms

# Create first window
NewWindow
Sleep 500ms

# Switch to terminal mode to type commands
TerminalMode
Type "echo 'Window 1 - Terminal Mode Demo'"
Enter
Sleep 1s

Type "pwd"
Enter
Sleep 500ms

Type "ls -la"
Enter
Sleep 1s

# Exit terminal mode and go back to window management
WindowManagementMode
Sleep 300ms

# Enable tiling mode
EnableTiling
Sleep 500ms

# Create a second window
NewWindow
Sleep 500ms

# Switch to terminal mode in the new window
TerminalMode
Type "echo 'Window 2 - Second Terminal'"
Enter
Sleep 1s

# Exit terminal mode
WindowManagementMode
Sleep 300ms

# Navigate between windows
NextWindow
Sleep 300ms

PrevWindow
Sleep 300ms

# Switch to workspace 2
SwitchWorkspace 2
Sleep 500ms

# Create a window in workspace 2
NewWindow
Sleep 500ms

TerminalMode
Type "echo 'This is workspace 2'"
Enter
Sleep 1s

# Exit to window management mode
WindowManagementMode
Sleep 300ms

# Move current window back to workspace 1 and follow it
MoveAndFollowWorkspace 1
Sleep 1s

# Disable tiling mode
DisableTiling
Sleep 500ms

# Toggle tiling back on
ToggleTiling
Sleep 500ms

# Snap window to the left
SnapLeft
Sleep 500ms

# Snap window to the right
SnapRight
Sleep 500ms

# Snap window to fullscreen
SnapFullscreen
Sleep 500ms

# Focus the next window
NextWindow
Sleep 300ms

# Minimize the focused window
MinimizeWindow
Sleep 500ms

# Restore it
RestoreWindow
Sleep 500ms

# Test keyboard shortcuts (Ctrl+b for leader key)
Ctrl+b
Sleep 200ms

# Exit with Escape
Escape
Sleep 2s

# Re-enable animations
EnableAnimations
