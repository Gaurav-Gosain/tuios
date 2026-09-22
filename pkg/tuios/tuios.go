// Package tuios provides a reusable terminal window manager that can be
// embedded in other Bubble Tea applications or used as a standalone TUI.
//
// TUIOS (Terminal UI Operating System) is a terminal-based window manager
// that provides vim-like modal interface, workspace support, mouse interaction,
// and BSP tiling.
//
// # Basic Usage
//
// Create a new TUIOS instance with default options:
//
//	model := tuios.New()
//	p := tea.NewProgram(model)
//	if _, err := p.Run(); err != nil {
//		log.Fatal(err)
//	}
//
// # Custom Configuration
//
// Use options to customize TUIOS behavior:
//
//	model := tuios.New(
//		tuios.WithTheme("dracula"),
//		tuios.WithShowKeys(true),
//		tuios.WithAnimations(false),
//		tuios.WithWorkspaces(9),
//	)
//
// # Using with sip (Web Terminal)
//
// TUIOS can be served through the browser using the sip library. Build the
// program yourself, with sip's options first and ProgramOptions after them:
// both carry a tea.WithFilter and the last one set wins, so sip's Serve, which
// appends its own options after yours, would drop the mouse motion filter.
//
//	server := sip.NewServer(sip.DefaultConfig())
//	server.ServeWithProgram(ctx, func(sess sip.Session) *tea.Program {
//		pty := sess.Pty()
//		model := tuios.New(tuios.WithSize(pty.Width, pty.Height))
//		return tea.NewProgram(model, append(sip.MakeOptions(sess), tuios.ProgramOptions()...)...)
//	})
//
// A model built this way is a local client as far as tuios knows: there is no
// option yet that marks it as a browser. See docs/LIBRARY.md for the SSH
// recipe and what WithSSHMode does and does not set.
package tuios

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
)

// Model is the main TUIOS model that implements tea.Model.
// It wraps the internal OS struct and provides a clean public API.
type Model = app.OS

// Mode represents the current interaction mode of TUIOS.
type Mode = app.Mode

// Mode constants
const (
	// WindowManagementMode allows window manipulation and navigation.
	WindowManagementMode = app.WindowManagementMode
	// TerminalMode passes input directly to the focused terminal.
	TerminalMode = app.TerminalMode
)

// Options configures a TUIOS instance.
type Options struct {
	// Theme is the color theme name (e.g., "dracula", "nord", "tokyonight").
	// Leave empty to use standard terminal colors.
	Theme string

	// ShowKeys enables the showkeys overlay to display pressed keys.
	ShowKeys bool

	// Animations enables/disables window animations.
	// When disabled, windows snap instantly to positions.
	Animations bool

	// ASCIIOnly uses ASCII characters instead of Nerd Font icons.
	ASCIIOnly bool

	// Workspaces is the number of workspaces (1-9). Default is 9.
	Workspaces int

	// BorderStyle sets the window border style.
	// Valid values: "rounded", "normal", "thick", "double", "hidden", "block", "ascii"
	BorderStyle string

	// DockbarPosition sets where the dockbar appears.
	// Valid values: "bottom", "top", "hidden"
	DockbarPosition string

	// HideWindowButtons hides the minimize/maximize/close buttons.
	HideWindowButtons bool

	// WindowButtonPosition selects which end of the title bar the window
	// controls sit on: "right" or "left". Empty means the config's value.
	WindowButtonPosition string

	// WindowButtonStyle selects how the window controls are drawn: "pill" or
	// "dots". Empty keeps the configured default.
	WindowButtonStyle string

	// ScrollbackLines is the number of lines in scrollback buffer.
	// Default is 10000, min 100, max 1000000.
	ScrollbackLines int

	// Width is the initial width (set automatically if 0).
	Width int

	// Height is the initial height (set automatically if 0).
	Height int

	// SSHMode indicates if running over SSH.
	SSHMode bool

	// UserConfig is a custom user configuration. If nil, defaults are used.
	UserConfig *config.UserConfig
}

// Option is a functional option for configuring TUIOS.
type Option func(*Options)

// WithTheme sets the color theme.
func WithTheme(name string) Option {
	return func(o *Options) {
		o.Theme = name
	}
}

// WithShowKeys enables the showkeys overlay.
func WithShowKeys(enabled bool) Option {
	return func(o *Options) {
		o.ShowKeys = enabled
	}
}

// WithAnimations enables or disables window animations.
func WithAnimations(enabled bool) Option {
	return func(o *Options) {
		o.Animations = enabled
	}
}

// WithASCIIOnly enables ASCII-only mode (no Nerd Font icons).
func WithASCIIOnly(enabled bool) Option {
	return func(o *Options) {
		o.ASCIIOnly = enabled
	}
}

// WithWorkspaces sets the number of workspaces (1-9).
func WithWorkspaces(n int) Option {
	return func(o *Options) {
		if n < 1 {
			n = 1
		} else if n > 9 {
			n = 9
		}
		o.Workspaces = n
	}
}

// WithBorderStyle sets the window border style.
func WithBorderStyle(style string) Option {
	return func(o *Options) {
		o.BorderStyle = style
	}
}

// WithDockbarPosition sets the dockbar position.
func WithDockbarPosition(position string) Option {
	return func(o *Options) {
		o.DockbarPosition = position
	}
}

// WithHideWindowButtons hides window control buttons.
func WithHideWindowButtons(hide bool) Option {
	return func(o *Options) {
		o.HideWindowButtons = hide
	}
}

// WithWindowButtonStyle selects how the window controls are drawn: "pill"
// (glyphs on a filled pill) or "dots" (macOS traffic lights).
func WithWindowButtonStyle(style string) Option {
	return func(o *Options) {
		o.WindowButtonStyle = style
	}
}

// WithWindowButtonPosition selects which end of the title bar the window
// controls sit on: "left" (the default, the way macOS does it) or "right".
func WithWindowButtonPosition(position string) Option {
	return func(o *Options) {
		o.WindowButtonPosition = position
	}
}

// WithScrollbackLines sets the scrollback buffer size.
func WithScrollbackLines(lines int) Option {
	return func(o *Options) {
		if lines < 100 {
			lines = 100
		} else if lines > 1000000 {
			lines = 1000000
		}
		o.ScrollbackLines = lines
	}
}

// WithSize sets the initial terminal size.
func WithSize(width, height int) Option {
	return func(o *Options) {
		o.Width = width
		o.Height = height
	}
}

// WithSSHMode enables SSH mode.
func WithSSHMode(enabled bool) Option {
	return func(o *Options) {
		o.SSHMode = enabled
	}
}

// WithUserConfig sets a custom user configuration.
func WithUserConfig(cfg *config.UserConfig) Option {
	return func(o *Options) {
		o.UserConfig = cfg
	}
}

// DefaultOptions returns the default options.
func DefaultOptions() Options {
	return Options{
		Animations:      true,
		Workspaces:      9,
		ScrollbackLines: 10000,
	}
}

// New creates a new TUIOS model with the given options.
// This is the main entry point for using TUIOS as a library.
func New(opts ...Option) *Model {
	options := DefaultOptions()
	for _, opt := range opts {
		opt(&options)
	}

	return newModel(options)
}

// PTY is anything that can report a terminal size, for NewForPTY.
//
// It takes methods, not fields, so a struct with Width and Height fields, such
// as sip.Pty, does not satisfy it. For those, pass the size with WithSize.
type PTY interface {
	Width() int
	Height() int
}

// NewForPTY creates a new TUIOS model for a PTY session with the given options.
func NewForPTY(pty PTY, opts ...Option) *Model {
	options := DefaultOptions()
	for _, opt := range opts {
		opt(&options)
	}
	options.Width = pty.Width()
	options.Height = pty.Height()

	return newModel(options)
}

// newModel creates the internal model with applied options.
func newModel(options Options) *Model {
	// Set up input handler
	app.SetInputHandler(input.HandleInput)

	// Load or create user config
	var userConfig *config.UserConfig
	if options.UserConfig != nil {
		userConfig = options.UserConfig
	} else {
		var err error
		userConfig, err = config.LoadUserConfig()
		if err != nil {
			userConfig = config.DefaultConfig()
		}
	}

	// LoadUserConfig no longer applies the appearance globals itself, so apply
	// them here exactly once, and pass the config into NewOS so it does not
	// re-load and re-apply.
	//
	// This runs BEFORE the embed options below, not after: ApplyAppearanceConfig
	// now covers border style, dock position, the hide toggles, scrollback and
	// the theme, so applying the options first would let the user's config file
	// silently overrule what the embedder asked for. Options are the outer
	// layer here, exactly as CLI flags are in cmd/tuios.
	config.ApplyAppearanceConfig(userConfig, &config.Global)

	// The embed options, layered the way every other entrypoint layers its
	// CLI flags. A zero value leaves the config's value standing.
	config.ApplyOverrides(config.Overrides{
		ASCIIOnly:            options.ASCIIOnly,
		BorderStyle:          options.BorderStyle,
		DockbarPosition:      options.DockbarPosition,
		HideWindowButtons:    options.HideWindowButtons,
		WindowButtonStyle:    options.WindowButtonStyle,
		WindowButtonPosition: options.WindowButtonPosition,
		ScrollbackLines:      options.ScrollbackLines,
		NoAnimations:         !options.Animations,
		ThemeName:            options.Theme,
	}, &config.Global)

	// Create keybind registry
	keybindRegistry := config.NewKeybindRegistry(userConfig)

	// Create the model using the factory function. ClientSSH implies
	// IsSSHMode, so the kind is all SSHMode has to set.
	kind := app.ClientLocal
	if options.SSHMode {
		kind = app.ClientSSH
	}
	return app.NewOS(app.OSOptions{
		Client:          kind,
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		ShowKeys:        options.ShowKeys,
		NumWorkspaces:   options.Workspaces,
		Width:           options.Width,
		Height:          options.Height,
	})
}

// ProgramOptions returns the tea.ProgramOption values every tuios client runs
// with: the frame rate cap, no signal handler, and the mouse motion filter.
// The caller owns the process signals. Use these when creating a tea.Program:
//
//	model := tuios.New()
//	p := tea.NewProgram(model, tuios.ProgramOptions()...)
func ProgramOptions() []tea.ProgramOption {
	return app.ProgramOptions()
}

// FilterMouseMotion is the tea.WithFilter function ProgramOptions installs. It
// drops the mouse motion nothing on screen reacts to. It is exported for a
// caller that composes its own option list.
func FilterMouseMotion(model tea.Model, msg tea.Msg) tea.Msg {
	return app.FilterMouseMotion(model, msg)
}

// Config re-exports the config package for customization.
// This allows users to access configuration types without importing internal packages.
var Config = struct {
	// LoadUserConfig loads the user's configuration file.
	LoadUserConfig func() (*config.UserConfig, error)
	// DefaultConfig returns the default configuration.
	DefaultConfig func() *config.UserConfig
	// GetConfigPath returns the path to the configuration file.
	GetConfigPath func() (string, error)
}{
	LoadUserConfig: config.LoadUserConfig,
	DefaultConfig:  config.DefaultConfig,
	GetConfigPath:  config.GetConfigPath,
}
