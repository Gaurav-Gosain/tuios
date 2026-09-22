package tape

import (
	"fmt"
)

// Player manages script playback. Pausing is tracked by the caller, in
// app.OS.ScriptPaused, not here.
type Player struct {
	commands []Command
	index    int  // Current command index
	finished bool // Whether all commands have been played
}

// NewPlayer creates a new script player from a list of commands
func NewPlayer(commands []Command) *Player {
	return &Player{
		commands: commands,
		index:    0,
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

// Reset resets the player to the beginning
func (p *Player) Reset() {
	p.index = 0
	p.finished = false
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

// String returns a debug string representation
func (p *Player) String() string {
	return fmt.Sprintf(
		"Player{index=%d/%d, finished=%v}",
		p.index, len(p.commands), p.finished,
	)
}
