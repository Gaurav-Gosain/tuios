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

// String returns a debug string representation
func (p *Player) String() string {
	return fmt.Sprintf(
		"Player{index=%d/%d, paused=%v, finished=%v}",
		p.index, len(p.commands), p.paused, p.finished,
	)
}
