# TUIOS Vim Workflow Demo
# Demonstrates a realistic development workflow with vim

# Disable animations for consistent playback
DisableAnimations

WindowManagementMode
Sleep 300ms

# Create main editor window
NewWindow
Sleep 400ms

# Enable tiling for side-by-side editing
EnableTiling
Sleep 300ms

TerminalMode
# Start vim and create a simple Go program
Type "vim main.go"
Enter
Sleep 800ms

# Enter insert mode and write code
Type "i"
Sleep 200ms
Type "package main"
Enter
Enter
Type "import \"fmt\""
Enter
Enter
Type "func main() {"
Enter
Tab
Type "fmt.Println(\"Hello from TUIOS!\")"
Enter
Type "}"
Escape
Sleep 300ms

# Save the file
Type ":w"
Enter
Sleep 400ms

# Split window in vim
Type ":vsplit util.go"
Enter
Sleep 500ms

# Write a utility function
Type "i"
Sleep 200ms
Type "package main"
Enter
Enter
Type "func greet(name string) {"
Enter
Tab
Type "fmt.Printf(\"Hello, %s!\\n\", name)"
Enter
Type "}"
Escape
Sleep 300ms

# Save and quit
Type ":wq"
Enter
Sleep 400ms

# Exit to window management mode
WindowManagementMode
Sleep 300ms

# Create a second window for running the program
NewWindow
Sleep 400ms

TerminalMode
# Build and run
Type "go build -o app main.go"
Enter
Sleep 600ms

Type "./app"
Enter
Sleep 500ms

# Create a third window for git operations
WindowManagementMode
Sleep 200ms
NewWindow
Sleep 400ms

TerminalMode
Type "git status"
Enter
Sleep 600ms

Type "git add ."
Enter
Sleep 300ms

Type "git commit -m 'Add main program'"
Enter
Sleep 600ms

# Navigate between windows to review work
WindowManagementMode
Sleep 300ms
PrevWindow
Sleep 300ms
PrevWindow
Sleep 300ms
NextWindow
Sleep 300ms

# Minimize build window
MinimizeWindow
Sleep 300ms

# Focus on editor
NextWindow
Sleep 500ms

# End demo
Sleep 1s

# Re-enable animations
EnableAnimations
