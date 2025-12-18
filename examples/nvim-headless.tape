# TUIOS Enhanced Demo Tape Script
# This script demonstrates the enhanced scripting language with explicit mode switching
# and full window management capabilities

# Disable animations for consistent playback
DisableAnimations

# Start in window management mode (default)
WindowManagementMode
EnableTiling
NewWindow

# Switch to terminal mode to type commands
TerminalMode
Type "nvim test.py"
Enter
Sleep 1s

Type "i"
Sleep 500ms

Type@1s "print(\"hello, world\")"
Enter
Sleep 1s

Escape
Ctrl+s
Type "ZZ"
Sleep 1s

Type "cat test.py"
Enter

# Re-enable animations
EnableAnimations
