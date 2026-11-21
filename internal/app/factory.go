package app

import (
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// OSOptions configures the creation of an OS instance.
type OSOptions struct {
	// Client says where the person looking at the screen is sitting. NewOS
	// derives the per-client flags below from it (see applyClientKind), so an
	// entry point names its kind and does not set them by hand. Zero is a
	// model nobody named, which is what the tests build.
	Client ClientKind

	// ViewOnly marks a client whose input is dropped, such as a tuios-web
	// viewer started with --read-only. served.Attach tells the daemon, which
	// leaves such a client out of the session's size under the largest and
	// latest window_size policies.
	ViewOnly bool

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

	// LearnMode runs the session as the guided tour in the browser build
	// (cmd/tuios-wasm): quitting shows a note instead of ending the program,
	// and actions the demo cannot do say so instead of failing. See
	// learn_mode.go.
	LearnMode bool

	// GuestApps, when set, is what the launcher offers instead of scanning
	// $PATH and .desktop files. The browser build has no filesystem to scan,
	// and lists the fake shell's programs here, which its guest pty runs.
	GuestApps []applist.Entry

	// ConfigReadOnly makes the settings page apply changes to this session only
	// and never write the config file. Set it wherever the person driving the
	// session is not the person whose config file it is: tuios-web serves a
	// network client, and several of them at once, each holding the snapshot it
	// loaded when it connected, so a save would write one client's stale view
	// of the whole file over the operator's and over every other client's.
	ConfigReadOnly bool

	// OpenInboxItem opens the Inbox on this item once attached. tuios-web
	// sets it from a notification's link. See OS.OpenInboxOn.
	OpenInboxItem string

	// ShowKeys enables the key display overlay.
	ShowKeys bool
	// Overrides are this run's CLI flags or embed options over the config
	// file. A config reload lays them over the reloaded file again, so a flag
	// lasts the run.
	Overrides config.Overrides
	// StartInTerminalMode starts the client in terminal mode on the focused
	// pane, whatever [startup] says and whether the session is new or not.
	StartInTerminalMode bool
	// AfterRestart says this client was started by a daemon restart from
	// the app, so the first frame says the session is back. See
	// daemon_restart.go.
	AfterRestart bool
	// AfterUpgrade is the version this client was started to follow a live
	// upgrade to, empty otherwise. See upgrade_follow.go.
	AfterUpgrade string
	// FollowUpgrades says the entry point runs UpgradeExec when the client
	// ends with ExitUpgradeFollowed. Only a terminal client started by
	// cmd/tuios does; the GUI bridge and the servers do not, so they keep
	// their build. See upgrade_follow.go.
	FollowUpgrades bool

	// NumWorkspaces sets the number of workspaces (default: 9).
	NumWorkspaces int

	// Width and Height set the initial terminal size.
	Width  int
	Height int

	// AttachedHost is the host DaemonClient reaches its daemon through, ""
	// for this machine.
	AttachedHost string
	// IsDaemonSession indicates this is a daemon-attached session.
	IsDaemonSession bool

	// DaemonClient is the client for daemon communication (required if IsDaemonSession).
	DaemonClient *session.TUIClient

	// SessionName is the name of the daemon session.
	SessionName string

	// IsSSHMode indicates this is an SSH session.
	IsSSHMode bool

	// SSHIsLoopback marks an SSH session that arrived over loopback (the
	// same machine), so its native clipboard is the operator's. See
	// OS.SSHIsLoopback.
	SSHIsLoopback bool

	// SSHSession is the SSH session reference (nil in local mode).
	SSHSession SSHConn

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

	// BrowserPaste says which keys make the browser paste, so the note that
	// the browser keeps the clipboard can name them. Only tuios-web sets it.
	BrowserPaste BrowserPasteKeys

	// Caps describes the terminal this session draws to. The servers detect it
	// per connection, because one process holds several connections and the
	// terminal that has to render a forwarded image is the one at the far end
	// of this session and not the one at the far end of the last.
	//
	// Nil means "this process's own terminal", which is what a local attach
	// wants and what a server falls back to when it detected nothing.
	Caps *HostCapabilities

	// Settings is the appearance this session starts from, built by
	// config.Resolve from the config file and the run's overrides. Every
	// entrypoint passes it; a server builds one per connection, so a session
	// that connects after an edit follows the file. Nil uses config.Global,
	// the seed tests set before building a model.
	Settings *config.Settings
}

// NewOS creates a new OS instance with the given options.
// This is the preferred way to create an OS instance, ensuring all required
// fields are properly initialized.
func NewOS(opts OSOptions) *OS {
	opts.applyClientKind()
	config.StartMacLayoutDetection()

	numWorkspaces := opts.NumWorkspaces
	if numWorkspaces <= 0 {
		numWorkspaces = 9
	}

	caps := opts.Caps
	if caps == nil {
		caps = GetHostCapabilities()
	}

	seed := config.Global
	if opts.Settings != nil {
		seed = *opts.Settings
	}

	os := &OS{
		FocusedWindow:     -1,
		WindowExitChan:    make(chan string, 10),
		PTYDataChan:       make(chan struct{}, 1),
		StateSyncChan:     make(chan StateSyncMsg, 10),
		ClientEventChan:   make(chan ClientEvent, 10),
		pluginCellsIn:     make(chan session.PluginUIPayload, 1),
		DaemonExitChan:    make(chan tea.Msg, daemonExitQueue),
		RemoteCommandChan: make(chan RemoteCommandMsg, remoteCommandQueue),
		MasterRatio:       seed.MasterRatioFraction(),
		CurrentWorkspace:  1,
		NumWorkspaces:     numWorkspaces,

		WorkspaceFocus:        make(map[int]int),
		FocusHistory:          make(map[int][]string),
		WorkspaceLayouts:      make(map[int][]WindowLayout),
		WorkspaceHasCustom:    make(map[int]bool),
		WorkspaceMasterRatio:  make(map[int]float64),
		WorkspaceStackRatio:   make(map[int]float64),
		WorkspaceMasterSplits: make(map[int]layout.MasterSplits),
		WorkspaceLayoutMode:   make(map[int]string),

		PendingResizes: make(map[string][2]int),

		KeybindRegistry:   opts.KeybindRegistry,
		ConfigReadOnly:    opts.ConfigReadOnly,
		BrowserClient:     opts.BrowserClient,
		LearnMode:         opts.LearnMode,
		ShowKeys:          opts.ShowKeys,
		overrides:         opts.Overrides,
		forceTerminalMode: opts.StartInTerminalMode,
		afterRestart:      opts.AfterRestart,
		followedUpgrade:   opts.AfterUpgrade,
		followUpgrades:    opts.FollowUpgrades,
		RecentKeys:        []KeyEvent{},

		Width:  opts.Width,
		Height: opts.Height,

		Client:          opts.Client,
		IsDaemonSession: opts.IsDaemonSession,
		IsSSHMode:       opts.IsSSHMode,
		SSHSession:      opts.SSHSession,
		SSHIsLoopback:   opts.SSHIsLoopback,
		TouchClient:     opts.TouchClient,
		BrowserPaste:    opts.BrowserPaste,
		RemoteClient:    opts.RemoteClient,
		Caps:            caps,

		Settings: seed,

		DaemonClient: opts.DaemonClient,
		SessionName:  opts.SessionName,
		AttachedHost: opts.AttachedHost,

		SharedBorders:           seed.SharedBorders,
		PaneGap:                 seed.PaneGap,
		ScrollColumnWidth:       seed.ScrollColumnWidth,
		lastConfigSharedBorders: seed.SharedBorders,
		lastConfigPaneGap:       seed.PaneGap,
		lastConfigScrollWidth:   seed.ScrollColumnWidth,
		lastConfigMaster: normalMasterLayout(session.MasterLayoutState{
			Position: seed.MasterPosition, Count: seed.MasterCount, NoGrid: seed.MasterNoGrid,
		}),
	}

	os.loadSidebarState()

	os.pathApps = applist.NewCache()
	os.desktopApps = newDesktopCache()
	os.guestApps = opts.GuestApps
	os.launchHistory = applist.LoadFrecency(applist.DefaultPath())

	os.KittyPassthrough = NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		ForceEnable:  opts.ForceGraphicsEnabled,
		Output:       opts.GraphicsOutput,
		RemoteClient: opts.GraphicsRemoteClient,
		Caps:         caps,
	})
	os.SixelPassthrough = NewSixelPassthroughWithOptions(SixelPassthroughOptions{
		ForceEnable: opts.ForceGraphicsEnabled && !caps.KittyGraphics,
		Output:      opts.GraphicsOutput,
		Caps:        caps,
		Symbols:     symbolKind(seed.ImageSymbols, caps.Term),
	})

	terminal.SetGraphicsCapabilities(
		os.KittyPassthrough != nil && os.KittyPassthrough.IsEnabled(),
		os.SixelPassthrough != nil && os.SixelPassthrough.IsEnabled(),
		caps.KittyAnimation,
	)

	os.HookManager = hooks.NewManager()
	hooks.SetVerbose(verboseLog())
	cfg := opts.UserConfig
	if cfg == nil {
		if loaded, err := config.LoadUserConfig(); err == nil {
			cfg = loaded
		}
	}
	if cfg != nil {
		os.UserConfig = cfg
		os.welcomeAsk = cfg.FirstRun && opts.Client == ClientLocal
		os.ListKeys = config.ListKeymap(cfg)
		os.ConfigWarnings = config.ConfigWarnings(cfg)
		if opts.BrowserClient {
			os.ConfigNotices = append(os.ConfigNotices, browserAlertNotices(cfg)...)
			os.ConfigNotices = append(os.ConfigNotices,
				remoteDockComponentNotice(cfg, "tuios-web")...)
		}
		if opts.IsSSHMode {
			os.ConfigNotices = append(os.ConfigNotices,
				remoteDockComponentNotice(cfg, "over SSH")...)
			os.ConfigNotices = append(os.ConfigNotices, sshAlertNotices(cfg)...)
		}
		if cfg.Debug.ShowKeyEvents {
			os.ShowKeys = true
		}
		if cfg.Spotlight.IsEnabled() {
			os.spotlight.on = true
		}
		os.HookManager.ReplaceFrom(clientHookTable(cfg))
	}

	os.UseBSPLayout = true

	if os.asksHostColors() {
		os.seedHostColors(caps)
	}

	os.PendingClipboardSet = make(chan ClipboardSetMsg, 1)

	if opts.IsDaemonSession {
		os.SubscribedPTYs = make(map[string]bool)
	}

	return os
}
