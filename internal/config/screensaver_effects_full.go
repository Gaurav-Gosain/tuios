//go:build !slim

package config

import tfx "github.com/Gaurav-Gosain/tuiffects"

// screensaverEffectNames is every effect the engine has registered. Deriving
// it from the engine is what stops the list here and the list there drifting
// apart.
func screensaverEffectNames() []string { return tfx.Names() }
