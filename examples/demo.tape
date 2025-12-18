# TUIOS Enhanced Demo Tape Script
# This script demonstrates the enhanced tape scripting language with explicit mode switching
# and comprehensive window/workspace management

# Disable animations for consistent, reproducible playback
DisableAnimations

# Start in window management mode (default)
WindowManagementMode
Sleep 300ms

# Create first window
NewWindow
Sleep 500ms

# Switch to terminal mode and type commands
TerminalMode
Type "echo 'Window 1 - Terminal Emulation Demo'"
Enter
Sleep 1s

Type "ls -la"
Enter
Sleep 1s

# Exit terminal mode back to window management
WindowManagementMode
Sleep 300ms

# Create second window
NewWindow
Sleep 500ms

# Switch to terminal mode
TerminalMode
Type "echo 'Window 2 - Workspace Demo'"
Enter
Sleep 1s

Type "pwd"
Enter
Sleep 500ms

# Exit to window management mode
WindowManagementMode
Sleep 300ms

# Enable tiling mode to organize windows
EnableTiling
Sleep 500ms

# Navigate between windows in tiling mode
NextWindow
Sleep 500ms

PrevWindow
Sleep 500ms

# Switch to workspace 2
SwitchWorkspace 2
Sleep 500ms

# Create window in workspace 2 with tiling
NewWindow
Sleep 500ms

TerminalMode
Type "echo 'Workspace 2 - Tiled Window'"
Enter
Sleep 1s

WindowManagementMode
Sleep 300ms

# Move window back to workspace 1 and follow it
MoveAndFollowWorkspace 1
Sleep 1s

# Test minimize and restore
MinimizeWindow
Sleep 500ms

RestoreWindow
Sleep 500ms

# Final navigation and cleanup
NextWindow
Sleep 500ms

# Exit gracefully
Sleep 1s

# Re-enable animations
EnableAnimations
