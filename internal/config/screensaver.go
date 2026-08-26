package config

import (
	"slices"

	tfx "github.com/Gaurav-Gosain/tuiffects"
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

// retiredScreensaverEffects are names that used to be accepted and are not any
// more. A config naming one still loads: the name maps to its replacement
// rather than tripping the enum validator with a warning about a value the
// person never chose and cannot look up.
//
// colorshift was dropped because it made a poor screen saver. It never moved a
// character, so nothing happened on screen except a hue sweep, and being the
// only effect that restyled every cell of every frame it was also the most
// expensive one to draw.
var retiredScreensaverEffects = map[string]string{
	"colorshift": ScreensaverRandomEffect,
}

// defaultScreensaverConfig returns the section DefaultConfig carries.
func defaultScreensaverConfig() ScreensaverConfig {
	return ScreensaverConfig{
		IdleMinutes: ScreensaverDefaultIdleMinutes,
		Effect:      ScreensaverRandomEffect,
	}
}

// fillMissingScreensaver fills empty strings with defaults. Pointer fields stay
// nil on purpose: the accessors below treat nil as "the default", so an absent
// key needs no repair.
func fillMissingScreensaver(cfg, defaultCfg *UserConfig) {
	s, d := &cfg.Screensaver, &defaultCfg.Screensaver
	if s.Effect == "" {
		s.Effect = d.Effect
	}
	if s.IdleMinutes <= 0 {
		s.IdleMinutes = d.IdleMinutes
	}
	if replacement, retired := retiredScreensaverEffects[s.Effect]; retired {
		s.Effect = replacement
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

// EffectName is the effect to run, or the random marker. A retired name maps to
// its replacement and any other unknown name falls back to random, so a config
// written against a different version still starts a saver.
func (s ScreensaverConfig) EffectName() string {
	if replacement, retired := retiredScreensaverEffects[s.Effect]; retired {
		return replacement
	}
	if s.Effect == "" || !slices.Contains(ScreensaverEffects, s.Effect) {
		return ScreensaverRandomEffect
	}
	return s.Effect
}

// RunsWhileBusy reports whether the saver may cover a pane that is working.
// The default is no: a saver that hides a running build is a bug.
func (s ScreensaverConfig) RunsWhileBusy() bool { return s.WhileBusy != nil && *s.WhileBusy }
