package app

import (
	"io"
	"strings"

	"charm.land/ssh"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// OSOptions configures the creation of an OS instance.
type OSOptions struct {
	// KeybindRegistry is required for keybinding support.
	KeybindRegistry *config.KeybindRegistry

	// UserConfig is the already-loaded user configuration. When set, NewOS uses
	// it directly instead of re-reading config.toml, so it never re-applies file
	// values over CLI flags (or races other sessions) via a second load. Callers
	// are responsible for applying the appearance globals once (via ApplyOverrides
	// and/or ApplyAppearanceConfig) before constructing the OS.
	UserConfig *config.UserConfig

	// BrowserClient says the far end is a browser tab rather than a terminal.
	// It gates the alert sinks a browser cannot deliver, and the warning that
	// says so. See browser_client.go.
	BrowserClient bool

	// ConfigReadOnly makes the settings page apply changes to this session only
	// and never write the config file. Set it wherever the person driving the
	// session is not the person whose config file it is: tuios-web serves a
	// network client, and several of them at once, each holding the snapshot it
	// loaded when it connected, so a save would write one client's stale view
	// of the whole file over the operator's and over every other client's.
	ConfigReadOnly bool

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

	// ForceGraphicsEnabled skips capability detection for the graphics
	// passthroughs. Use this in web mode where stdin isn't a real TTY so
	// GetHostCapabilities can't detect terminal support, but the browser
	// terminal (xterm.js kitty addon) actually supports the protocol.
	ForceGraphicsEnabled bool

	// GraphicsOutput is the writer that kitty/sixel APC sequences are written
	// to. If nil, the passthroughs fall back to /dev/tty / os.Stdout (the
	// native TTY path). Web mode supplies the sip session's PTY slave and SSH
	// mode supplies the ssh.Session so graphics bytes flow through the same
	// pipe as bubbletea's text output and reach the client's terminal.
	GraphicsOutput io.Writer

	// GraphicsRemoteClient marks the graphics host as a network client (SSH)
	// that does not share the server's filesystem, so file-medium kitty
	// transmissions are re-encoded as direct data. See
	// KittyPassthroughOptions.RemoteClient.
	GraphicsRemoteClient bool

	// RemoteClient marks a client process that is not on the user's machine:
	// the tuios ssh server and tuios-web both run the TUI beside the daemon,
	// with the user at the far end of a network. Anything that would touch the
	// host's own desktop, a clipboard helper or a file viewer, has to know,
	// because doing it here would act on the server's desktop and not on the
	// user's. See screenshot.go.
	RemoteClient bool

	// TouchClient says the pointer driving this session is a finger, which
	// widens the gestures that are aimed at a single cell. Only tuios-web can
	// know this, and only from the browser that connected.
	TouchClient bool
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
		FocusedWindow:   -1,
		WindowExitChan:  make(chan string, 10),
		PTYDataChan:     make(chan struct{}, 1),
		StateSyncChan:   make(chan *session.SessionState, 10),
		ClientEventChan: make(chan ClientEvent, 10),
		// Routed verbs, for the hosts that cannot Send into the program. The
		// local attach client leaves this unused. See dock_remote.go.
		RemoteCommandChan: make(chan RemoteCommandMsg, remoteCommandQueue),
		MasterRatio:       0.5,
		CurrentWorkspace:  1,
		NumWorkspaces:     numWorkspaces,

		// Workspace state maps
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),

		// Resize tracking
		PendingResizes: make(map[string][2]int),

		// Keybindings
		KeybindRegistry:   opts.KeybindRegistry,
		ConfigReadOnly:    opts.ConfigReadOnly,
		BrowserClient:     opts.BrowserClient,
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
		TouchClient:     opts.TouchClient,
		RemoteClient:    opts.RemoteClient,

		// Daemon connection
		DaemonClient: opts.DaemonClient,
		SessionName:  opts.SessionName,

		// Pane geometry inputs start at this client's config and are settled
		// across the session by state sync; see the field comment in os.go.
		SharedBorders:           config.SharedBorders,
		PaneGap:                 config.PaneGap,
		lastConfigSharedBorders: config.SharedBorders,
		lastConfigPaneGap:       config.PaneGap,
	}

	// Sidebar order and expand/collapse state survive restarts; a load failure
	// just means the defaults (creation order, current session expanded).
	os.loadSidebarState()

	// The $PATH cache starts empty and is filled by the first palette open, so
	// startup pays nothing for a launcher the user may never reach for. Launch
	// history is read here because it is one small file and the first open wants
	// it already ranked.
	os.pathApps = applist.NewCache()
	os.desktopApps = newDesktopCache()
	os.launchHistory = applist.LoadFrecency(applist.DefaultPath())

	// Initialize graphics passthrough. The passthroughs decide for themselves
	// whether the host can display anything; a pane's emulator never emulates
	// graphics locally.
	os.KittyPassthrough = NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		ForceEnable:  opts.ForceGraphicsEnabled,
		Output:       opts.GraphicsOutput,
		RemoteClient: opts.GraphicsRemoteClient,
	})
	os.SixelPassthrough = NewSixelPassthroughWithOptions(SixelPassthroughOptions{
		ForceEnable: opts.ForceGraphicsEnabled,
		Output:      opts.GraphicsOutput,
	})

	// Tell the terminal package what tuios can forward, so shells spawned
	// locally advertise a terminal identity their image tools recognise. The
	// passthroughs are the source of truth here: they already fold detection
	// and the force flag together, and a nil passthrough means no forwarding.
	terminal.SetGraphicsCapabilities(
		os.KittyPassthrough != nil && os.KittyPassthrough.IsEnabled(),
		os.SixelPassthrough != nil && os.SixelPassthrough.IsEnabled(),
		GetHostCapabilities().KittyAnimation,
	)

	// Initialize hooks manager and load user-defined hooks from config. Prefer
	// the config the caller already loaded so we never trigger a second load
	// (which used to re-apply appearance globals over CLI flags and, on the
	// per-connection server paths, race other sessions). Loading is now pure and
	// has no package-global side effects, so the fallback is safe too.
	os.HookManager = hooks.NewManager()
	cfg := opts.UserConfig
	if cfg == nil {
		if loaded, err := config.LoadUserConfig(); err == nil {
			cfg = loaded
		}
	}
	if cfg != nil {
		// Hold the loaded config so the in-app settings page can persist live
		// changes back to disk.
		os.UserConfig = cfg
		// Collected here and reported from Init, once there is a TUI to report
		// them in.
		os.ConfigWarnings = config.ConfigWarnings(cfg)
		// A sink the client cannot deliver is a config problem like any other,
		// and it is only knowable here, where the client is known.
		if opts.BrowserClient {
			os.ConfigWarnings = append(os.ConfigWarnings, browserAlertWarnings(cfg)...)
			os.ConfigWarnings = append(os.ConfigWarnings,
				remoteDockComponentWarning(cfg, "tuios-web")...)
		}
		if opts.IsSSHMode {
			os.ConfigWarnings = append(os.ConfigWarnings,
				remoteDockComponentWarning(cfg, "over SSH")...)
		}
		if opts.IsSSHMode {
			os.ConfigWarnings = append(os.ConfigWarnings, sshAlertWarnings(cfg)...)
		}
		if cfg.Debug.ShowKeyEvents {
			os.ShowKeys = true
		}
		if cfg.Hooks != nil {
			os.HookManager.LoadFromConfig(cfg.Hooks)
		}
		// [notifications.agent].command is shorthand for the after-agent-state
		// hook, so it is discoverable beside the toggles that gate it. Registering
		// rather than replacing means a user who wrote both spellings gets both,
		// which is what [hooks] does for two commands on any other event.
		if cmd := strings.TrimSpace(cfg.Notifications.Agent.Command); cmd != "" {
			os.HookManager.Register(hooks.AfterAgentState, cmd)
		}
	}

	// Default to BSP layout mode
	os.UseBSPLayout = true

	// Initialize clipboard channel for OSC 52 propagation
	os.PendingClipboardSet = make(chan string, 1)

	// Initialize PTY subscription tracking for daemon sessions
	if opts.IsDaemonSession {
		os.SubscribedPTYs = make(map[string]bool)
	}

	return os
}
