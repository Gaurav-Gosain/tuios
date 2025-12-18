# TUIOS Advanced Demo Script
# Demonstrates advanced features: mouse interactions, loops, variables, and assertions

# Disable animations for consistent playback
DisableAnimations

# Initialize workspace
WindowManagementMode
Sleep 500ms

# Create multiple windows in a row
NewWindow
Sleep 300ms
NewWindow
Sleep 300ms
NewWindow
Sleep 300ms

# Enable tiling to organize them
EnableTiling
Sleep 500ms

# Cycle through all windows
NextWindow
Sleep 250ms
NextWindow
Sleep 250ms
NextWindow
Sleep 250ms

# Test workspace organization
SwitchWorkspace 2
Sleep 400ms

# Create project workspace
NewWindow
Sleep 300ms
TerminalMode
Type "echo 'Project Workspace - Tab 2'"
Enter
Sleep 800ms

# Run a simulated build process
Type "echo 'Building project...'"
Enter
Sleep 500ms
Type "for i in {1..5}; do echo 'Step $i'; sleep 0.1; done"
Enter
Sleep 1s

# Switch back to main workspace
WindowManagementMode
SwitchWorkspace 1
Sleep 500ms

# Test snapping functionality
SnapLeft
Sleep 400ms
NextWindow
Sleep 200ms
SnapRight
Sleep 400ms

# Create a monitoring window
NewWindow
Sleep 300ms
TerminalMode
Type "top -b -n 1 | head -20"
Enter
Sleep 1s

# Return to window management and organize
WindowManagementMode
Sleep 300ms

# Test fullscreen
SnapFullscreen
Sleep 500ms

# Minimize inactive windows
NextWindow
Sleep 200ms
MinimizeWindow
Sleep 300ms

NextWindow
Sleep 200ms
MinimizeWindow
Sleep 300ms

# Restore them
RestoreWindow
Sleep 300ms
PrevWindow
Sleep 200ms
RestoreWindow
Sleep 300ms

# Switch to workspace 3 and create dev environment
SwitchWorkspace 3
Sleep 400ms

NewWindow
Sleep 300ms
TerminalMode
Type "echo 'Development Environment'"
Enter
Sleep 500ms
Type "echo 'Ready for coding!'"
Enter
Sleep 800ms

# Final workspace tour
WindowManagementMode
SwitchWorkspace 1
Sleep 400ms
SwitchWorkspace 2
Sleep 400ms
SwitchWorkspace 3
Sleep 400ms
SwitchWorkspace 1
Sleep 600ms

# Cleanup and end
DisableTiling
Sleep 500ms

# Re-enable animations
EnableAnimations
