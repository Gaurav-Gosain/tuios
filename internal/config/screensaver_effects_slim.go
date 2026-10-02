//go:build slim

package config

// screensaverEffectNames is the effect engine's list, written out, because
// tuios-slim has no screen saver and does not link the engine. A config file
// that names an effect still loads. TestSlimScreensaverEffectNames holds the
// list equal to the engine's.
func screensaverEffectNames() []string { return slimScreensaverEffectNames }
