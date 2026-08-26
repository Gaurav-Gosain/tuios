package config

import (
	"slices"

	"github.com/Gaurav-Gosain/tuios/pkg/tfx"
)

// ScreensaverConfig is the [screensaver] section: whether the screen animates
// itself after a spell of quiet, how long that spell is, and which effect
// runs.
//
// Enabled is a pointer so that turning it off in the settings page survives a
// reload rather than reading as "unset" and snapping back on.
type ScreensaverConfig struct {
	Enabled     *bool  `toml:"enabled"`      // run a screen saver at all (default: false)
	IdleMinutes int    `toml:"idle_minutes"` // quiet time before it starts (default: 10)
	Effect      string `toml:"effect"`       // random, or one effect by name (default: random)
	WhileBusy   *bool  `toml:"while_busy"`   // start even when a pane is working (default: false)
}

// Screensaver defaults, one source for DefaultConfig and the accessors.
const (
	ScreensaverDefaultIdleMinutes = 10
	ScreensaverMinIdleMinutes     = 1
	ScreensaverMaxIdleMinutes     = 240
	// ScreensaverRandomEffect picks a different effect each time it starts.
	ScreensaverRandomEffect = "random"
)

// ScreensaverEffects is what the effect option accepts: the random choice plus
// every effect the engine has registered. Deriving it from the engine is what
// stops the list here and the list there drifting apart.
var ScreensaverEffects = append([]string{ScreensaverRandomEffect}, tfx.Names()...)

// defaultScreensaverConfig returns the section DefaultConfig carries.
func defaultScreensaverConfig() ScreensaverConfig {
	return ScreensaverConfig{Effect: ScreensaverRandomEffect}
}

// fillMissingScreensaver fills empty strings with defaults. Pointer fields stay
// nil on purpose: the accessors below treat nil as "the default", so an absent
// key needs no repair.
func fillMissingScreensaver(cfg, defaultCfg *UserConfig) {
	s, d := &cfg.Screensaver, &defaultCfg.Screensaver
	if s.Effect == "" {
		s.Effect = d.Effect
	}
}

// IsEnabled reports whether a screen saver should ever start.
func (s ScreensaverConfig) IsEnabled() bool { return s.Enabled != nil && *s.Enabled }

// IdleDelayMinutes is the effective quiet time before the saver starts.
func (s ScreensaverConfig) IdleDelayMinutes() int {
	if s.IdleMinutes <= 0 {
		return ScreensaverDefaultIdleMinutes
	}
	return clampRange(s.IdleMinutes, ScreensaverMinIdleMinutes, ScreensaverMaxIdleMinutes)
}

// EffectName is the effect to run, or the random marker. An unknown name falls
// back to random rather than refusing to run.
func (s ScreensaverConfig) EffectName() string {
	if s.Effect == "" || !slices.Contains(ScreensaverEffects, s.Effect) {
		return ScreensaverRandomEffect
	}
	return s.Effect
}

// RunsWhileBusy reports whether the saver may cover a pane that is working.
// The default is no: a saver that hides a running build is a bug.
func (s ScreensaverConfig) RunsWhileBusy() bool { return s.WhileBusy != nil && *s.WhileBusy }
