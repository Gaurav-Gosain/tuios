package tape

import (
	"fmt"
	"time"
)

// Player manages script playback
type Player struct {
	commands     []Command
	index        int           // Current command index
	paused       bool          // Whether playback is paused
	finished     bool          // Whether all commands have been played
	currentDelay time.Duration // Remaining delay before next command
}

// NewPlayer creates a new script player from a list of commands
func NewPlayer(commands []Command) *Player {
	return &Player{
		commands: commands,
		index:    0,
		paused:   false,
		finished: false,
	}
}

// NextCommand returns the next command to execute without advancing the player state
// Used for pre-planning
func (p *Player) NextCommand() *Command {
	if p.index >= len(p.commands) {
		return nil
	}
	return &p.commands[p.index]
}

// Advance moves to the next command
func (p *Player) Advance() {
	if p.index < len(p.commands) {
		p.index++
	}
	if p.index >= len(p.commands) {
		p.finished = true
	}
}

// IsFinished returns true if all commands have been executed
func (p *Player) IsFinished() bool {
	return p.finished
}

// IsPaused returns true if playback is paused
func (p *Player) IsPaused() bool {
	return p.paused
}

// SetPaused sets the paused state
func (p *Player) SetPaused(paused bool) {
	p.paused = paused
}

// Reset resets the player to the beginning
func (p *Player) Reset() {
	p.index = 0
	p.paused = false
	p.finished = false
	p.currentDelay = 0
}

// CurrentIndex returns the current command index
func (p *Player) CurrentIndex() int {
	return p.index
}

// TotalCommands returns the total number of commands
func (p *Player) TotalCommands() int {
	return len(p.commands)
}

// Progress returns a value between 0 and 100 representing playback progress
func (p *Player) Progress() int {
	if len(p.commands) == 0 {
		return 100
	}
	return (p.index * 100) / len(p.commands)
}

// CommandStr returns a string representation of the current command for display
func (p *Player) CommandStr() string {
	if p.index >= len(p.commands) {
		return "Script finished"
	}
	cmd := p.commands[p.index]
	return cmd.String()
}

// ScriptMsg is a custom Bubble Tea message for script commands
type ScriptMsg struct {
	Command  *Command
	Delay    time.Duration
	Finished bool
}

// TypedScriptMsg represents a Type command that needs to be sent character by character
type TypedScriptMsg struct {
	Text string
	Char int // Which character in the text to send
}

// KeyPressScriptMsg represents a key press command
type KeyPressScriptMsg struct {
	Key string // The key to press (e.g., "enter", "ctrl+b")
}

// ActionScriptMsg represents a tuios action command
type ActionScriptMsg struct {
	Action string   // The action name
	Args   []string // Optional arguments
}

// TickMsg is sent periodically to check for next script command execution
type TickMsg time.Time

// ScriptPlaybackMsg contains state information about script playback
type ScriptPlaybackMsg struct {
	State    string // "playing", "paused", "finished"
	Index    int    // Current command index
	Total    int    // Total commands
	Progress int    // 0-100
	Current  string // Current command display string
}

// CommandToMsg converts a Command to appropriate Bubble Tea message(s)
// This is used by the player to generate messages for the app.Update() handler
func (p *Player) CommandToMsg(cmd *Command) any {
	if cmd == nil {
		return nil
	}

	switch cmd.Type {
	case CommandTypeType:
		if len(cmd.Args) > 0 {
			return TypedScriptMsg{
				Text: cmd.Args[0],
				Char: 0,
			}
		}

	case CommandTypeSleep:
		if len(cmd.Args) > 0 {
			return ScriptMsg{
				Command: cmd,
				Delay:   cmd.Delay,
			}
		}

	case CommandTypeEnter:
		return KeyPressScriptMsg{Key: "enter"}

	case CommandTypeSpace:
		return KeyPressScriptMsg{Key: "space"}

	case CommandTypeBackspace:
		return KeyPressScriptMsg{Key: "backspace"}

	case CommandTypeDelete:
		return KeyPressScriptMsg{Key: "delete"}

	case CommandTypeTab:
		return KeyPressScriptMsg{Key: "tab"}

	case CommandTypeEscape:
		return KeyPressScriptMsg{Key: "escape"}

	case CommandTypeUp:
		return KeyPressScriptMsg{Key: "up"}

	case CommandTypeDown:
		return KeyPressScriptMsg{Key: "down"}

	case CommandTypeLeft:
		return KeyPressScriptMsg{Key: "left"}

	case CommandTypeRight:
		return KeyPressScriptMsg{Key: "right"}

	case CommandTypeHome:
		return KeyPressScriptMsg{Key: "home"}

	case CommandTypeEnd:
		return KeyPressScriptMsg{Key: "end"}

	case CommandTypeKeyCombo:
		if len(cmd.Args) > 0 {
			return KeyPressScriptMsg{Key: cmd.Args[0]}
		}

	case CommandTypeNewWindow:
		return ActionScriptMsg{Action: "new_window"}

	case CommandTypeCloseWindow:
		return ActionScriptMsg{Action: "close_window"}

	case CommandTypeSplit:
		return ActionScriptMsg{Action: "split"}

	case CommandTypeFocus:
		return ActionScriptMsg{
			Action: "focus",
			Args:   cmd.Args,
		}

	case CommandTypeToggleTiling:
		return ActionScriptMsg{Action: "toggle_tiling"}

	case CommandTypeSwitchWS:
		return ActionScriptMsg{
			Action: "switch_workspace",
			Args:   cmd.Args,
		}

	case CommandTypeMoveToWS:
		return ActionScriptMsg{
			Action: "move_to_workspace",
			Args:   cmd.Args,
		}

	case CommandTypeWait:
		// Wait command - could be enhanced for pattern matching
		if len(cmd.Args) > 0 {
			return ScriptMsg{
				Command: cmd,
				Delay:   time.Second, // Default 1 second wait
			}
		}

	case CommandTypeSet, CommandTypeOutput, CommandTypeSource:
		// Configuration commands - handle specially if needed
		return ScriptMsg{Command: cmd}
	}

	return nil
}

// PlaybackStatus returns current playback status
func (p *Player) PlaybackStatus() ScriptPlaybackMsg {
	state := "playing"
	if p.paused {
		state = "paused"
	} else if p.finished {
		state = "finished"
	}

	return ScriptPlaybackMsg{
		State:    state,
		Index:    p.index,
		Total:    len(p.commands),
		Progress: p.Progress(),
		Current:  p.CommandStr(),
	}
}

// String returns a debug string representation
func (p *Player) String() string {
	return fmt.Sprintf(
		"Player{index=%d/%d, paused=%v, finished=%v}",
		p.index, len(p.commands), p.paused, p.finished,
	)
}
