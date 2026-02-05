package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/ssh"
)

// OSOptions configures the creation of an OS instance.
type OSOptions struct {
	// KeybindRegistry is required for keybinding support.
	KeybindRegistry *config.KeybindRegistry

	// ShowKeys enables the key display overlay.
	ShowKeys bool

	// NumWorkspaces sets the number of workspaces (default: 9).
	NumWorkspaces int

	// Width and Height set the initial terminal size.
	Width  int
	Height int

	// IsDaemonSession indicates this is a daemon-attached session.
	IsDaemonSession bool

	// DaemonClient is the client for daemon communication (required if IsDaemonSession).
	DaemonClient *session.TUIClient

	// SessionName is the name of the daemon session.
	SessionName string

	// IsSSHMode indicates this is an SSH session.
	IsSSHMode bool

	// SSHSession is the SSH session reference (nil in local mode).
	SSHSession ssh.Session

	// EnableGraphicsPassthrough enables Kitty/Sixel graphics passthrough.
	// This should be true for terminal sessions, false for web.
	EnableGraphicsPassthrough bool
}

// NewOS creates a new OS instance with the given options.
// This is the preferred way to create an OS instance, ensuring all required
// fields are properly initialized.
func NewOS(opts OSOptions) *OS {
	numWorkspaces := opts.NumWorkspaces
	if numWorkspaces <= 0 {
		numWorkspaces = 9
	}

	os := &OS{
		// Core state
		FocusedWindow:    -1,
		WindowExitChan:   make(chan string, 10),
		StateSyncChan:    make(chan *session.SessionState, 10),
		ClientEventChan:  make(chan ClientEvent, 10),
		MasterRatio:      0.5,
		CurrentWorkspace: 1,
		NumWorkspaces:    numWorkspaces,

		// Workspace state maps
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),

		// Resize tracking
		PendingResizes: make(map[string][2]int),

		// Keybindings
		KeybindRegistry:   opts.KeybindRegistry,
		ShowKeys:          opts.ShowKeys,
		RecentKeys:        []KeyEvent{},
		KeyHistoryMaxSize: 5,

		// Dimensions
		Width:  opts.Width,
		Height: opts.Height,

		// Mode flags
		IsDaemonSession: opts.IsDaemonSession,
		IsSSHMode:       opts.IsSSHMode,
		SSHSession:      opts.SSHSession,

		// Daemon connection
		DaemonClient: opts.DaemonClient,
		SessionName:  opts.SessionName,
	}

	// Initialize graphics passthrough if enabled
	if opts.EnableGraphicsPassthrough {
		os.KittyRenderer = NewKittyRenderer()
		os.KittyPassthrough = NewKittyPassthrough()
		os.SixelPassthrough = NewSixelPassthrough()
	}

	// Initialize PTY subscription tracking for daemon sessions
	if opts.IsDaemonSession {
		os.SubscribedPTYs = make(map[string]bool)
	}

	return os
}
