package learn

import "github.com/Gaurav-Gosain/tuios/internal/config"

// Config is the configuration the tour runs with: the shipped defaults, with
// the looks the lessons were written against pinned in place.
//
// v0.8.0 changed seven defaults: the rail came on, on the right and narrower,
// the dock and the window titles moved to the top, a zoomed pane stopped
// short of the whole screen, a click on a pane began to need a second one to
// start typing, and the scrollbar drew a track. The lessons, their
// screenshots and the steps that point at the dock or ask for a click all
// describe the screen as it was before, so the tour keeps that screen. Each
// value is written out rather than left unset, so a later change to a default
// cannot move the tour without a change here.
func Config() *config.UserConfig {
	cfg := config.DefaultConfig()
	// A browser tab has no desktop notifications. Left on, the default warns
	// about it at startup, which is noise in a tutorial.
	cfg.Notifications.Agent.Notify = new(false)
	config.PinPreV080Appearance(&cfg.Appearance)
	return cfg
}
