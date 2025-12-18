# TUIOS Demo with WaitUntilRegex
# This script demonstrates the WaitUntilRegex command for conditional execution

# Disable animations for consistent playback
DisableAnimations

WindowManagementMode
NewWindow
Sleep 500ms

TerminalMode
Type "echo 'Starting test'"
Enter

# Wait until the output contains "test" with a 3000ms timeout
WaitUntilRegex "test" 3000

Type "ls -la"
Enter

# Wait for the prompt to return (typical shell prompt pattern)
WaitUntilRegex "\\$" 5000

Type "pwd"
Enter

# Wait for pwd output
WaitUntilRegex "/.*"

Type "exit"
Enter

# Re-enable animations
EnableAnimations
