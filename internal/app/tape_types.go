package app

import "time"

// TapeManagerMode represents the current mode of the tape manager
type TapeManagerMode int

const (
	// TapeManagerList shows the list of tape files
	TapeManagerList TapeManagerMode = iota
	// TapeManagerRecording is recording a new tape
	TapeManagerRecording
	// TapeManagerPlaying is playing back a tape
	TapeManagerPlaying
	// TapeManagerConfirmDelete asks for deletion confirmation
	TapeManagerConfirmDelete
	// TapeManagerNaming is entering a name for a new tape
	TapeManagerNaming
)

// TapeFile represents a tape file with metadata
type TapeFile struct {
	Name     string    // Display name (without extension)
	Path     string    // Full path to the file
	Size     int64     // File size in bytes
	Modified time.Time // Last modification time
}

// TapeManagerState holds the state for the tape manager UI
type TapeManagerState struct {
	Mode           TapeManagerMode
	Files          []TapeFile
	SelectedIndex  int
	ScrollOffset   int
	NameBuffer     string // Buffer for naming new tapes
	DeleteConfirm  bool   // Whether delete is confirmed
	ErrorMessage   string // Error message to display
	SuccessMessage string // Success message to display
	MessageTime    time.Time
}
