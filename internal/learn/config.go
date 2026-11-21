package learn

import "github.com/Gaurav-Gosain/tuios/internal/config"

// Config is the configuration the tour runs with: the shipped defaults, so
// the tour shows the screen a person sees after installing tuios. The
// lessons are worded for that screen: the dock and the window titles at the
// top, the rail on the right. Each lesson starts with the reset command,
// which closes the rail, so a step that opens it opens it.
func Config() *config.UserConfig {
	cfg := config.DefaultConfig()
	cfg.Notifications.Agent.Notify = new(false)
	return cfg
}
