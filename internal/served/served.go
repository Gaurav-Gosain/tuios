// Package served builds the model for one session a server hands to a remote
// client: the SSH server in internal/server and the web server in
// cmd/tuios-web.
//
// Both servers used to carry two copies each of the same sequence, one for an
// ephemeral session and one attached to the daemon, and the copies had drifted.
// This is the one copy. It lives apart from internal/server so tuios-web can
// use it without linking wish.
package served

import (
	"fmt"
	"log"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// NewModel builds a session's model from opts, filling in what every served
// session loads for itself: the user's config as the file says now (the
// defaults when it cannot be read), the keybind registry built from it, and
// the appearance seed with the server's flags ov laid over it.
//
// The config is read per session so one that connects after an edit follows
// the file. See config.AppearanceFrom.
func NewModel(opts app.OSOptions, ov config.Overrides) *app.OS {
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config for a served session, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	app.SetInputHandler(input.HandleInput)

	seed := config.AppearanceFrom(userConfig, ov)
	opts.KeybindRegistry = config.NewKeybindRegistry(userConfig)
	opts.UserConfig = userConfig
	opts.Settings = &seed
	return app.NewOS(opts)
}

// Attach connects to this machine's daemon, attaches the session opts names
// (creating it when it does not exist), and builds the model on it.
//
// opts.Width and opts.Height are the client's size. version is what the hello
// reports, and caps is the client terminal the daemon is told about. When
// opts.SessionName is empty, pick chooses the name from the sessions the daemon
// listed at the handshake. opts.IsDaemonSession, DaemonClient and SessionName
// are filled in here.
//
// On an error nothing is left open, and the caller can fall back to an
// ephemeral session.
func Attach(opts app.OSOptions, ov config.Overrides, version string, caps *session.ClientCapabilities, pick func(available []string) string) (*app.OS, error) {
	client := session.NewTUIClient()
	if err := client.ConnectWithCapabilities(version, opts.Width, opts.Height, caps); err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %w", err)
	}

	name := opts.SessionName
	if name == "" {
		name = pick(client.AvailableSessionNames())
	}

	state, err := client.AttachSession(name, true, opts.Width, opts.Height)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to attach to session: %w", err)
	}

	client.StartReadLoop()

	opts.IsDaemonSession = true
	opts.DaemonClient = client
	opts.SessionName = name
	model := NewModel(opts, ov)

	// Everything the daemon sends an attached client, then the windows it
	// handed over. The same two calls every client makes.
	model.WireDaemonClient(client)
	model.RestoreAttachedSession(state)
	return model, nil
}
