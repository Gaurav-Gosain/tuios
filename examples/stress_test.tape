# TUIOS Stress Test
# Tests rapid window creation, switching, and management

# Disable animations for consistent playback
DisableAnimations

WindowManagementMode
Sleep 200ms

# Rapid window creation
NewWindow
Sleep 100ms
NewWindow
Sleep 100ms
NewWindow
Sleep 100ms
NewWindow
Sleep 100ms
NewWindow
Sleep 100ms

# Enable tiling
EnableTiling
Sleep 300ms

# Fast navigation
NextWindow
Sleep 50ms
NextWindow
Sleep 50ms
NextWindow
Sleep 50ms
NextWindow
Sleep 50ms
NextWindow
Sleep 50ms

# Rapid workspace switching
SwitchWorkspace 2
Sleep 100ms
SwitchWorkspace 3
Sleep 100ms
SwitchWorkspace 4
Sleep 100ms
SwitchWorkspace 5
Sleep 100ms
SwitchWorkspace 1
Sleep 300ms

# Create more windows
NewWindow
Sleep 100ms
NewWindow
Sleep 100ms

# Quick snapping
SnapLeft
Sleep 150ms
NextWindow
Sleep 100ms
SnapRight
Sleep 150ms
NextWindow
Sleep 100ms
SnapFullscreen
Sleep 150ms

# Fast minimize/restore cycle
MinimizeWindow
Sleep 100ms
RestoreWindow
Sleep 100ms
NextWindow
Sleep 50ms
MinimizeWindow
Sleep 100ms
RestoreWindow
Sleep 100ms

# Workspace migration stress test
MoveAndFollowWorkspace 2
Sleep 150ms
MoveAndFollowWorkspace 3
Sleep 150ms
MoveAndFollowWorkspace 1
Sleep 200ms

# Toggle tiling rapidly
ToggleTiling
Sleep 100ms
ToggleTiling
Sleep 100ms
ToggleTiling
Sleep 100ms
ToggleTiling
Sleep 200ms

# Final cooldown
Sleep 500ms
DisableTiling
Sleep 300ms

# Re-enable animations
EnableAnimations
