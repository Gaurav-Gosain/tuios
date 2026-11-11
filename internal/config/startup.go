package config

import (
	"strconv"

	"github.com/adrg/xdg"
)

// EffectiveDefault returns the value an option has when the user has not set
// it, for the config that applies on this machine.
//
// For most options this is Option.Default. The two [startup] booleans differ:
// they ship on, but a config file without a [startup] table reads them as
// false, so an existing install keeps its floating, standalone session. Only
// a machine with no config file at all gets the shipped true.
func EffectiveDefault(opt Option) string {
	if opt.Path != "startup.tiled" && opt.Path != "startup.daemon" {
		return opt.Default
	}
	path, err := xdg.SearchConfigFile("tuios/config.toml")
	if err != nil {
		return opt.Default
	}
	data, err := ReadConfigFile(path)
	if err != nil {
		return opt.Default
	}
	return effectiveStartupDefault(opt, data)
}

// effectiveStartupDefault resolves a [startup] boolean against the bytes of a
// config file that exists.
func effectiveStartupDefault(opt Option, data []byte) string {
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return opt.Default
	}
	if opt.Path == "startup.tiled" {
		return strconv.FormatBool(cfg.Startup.Tiled)
	}
	return strconv.FormatBool(cfg.Startup.Daemon)
}

// StartupLayoutWarnings returns a warning when a config file sets [startup]
// layout while tiled is off. A new session then floats and the layout is not
// used, with nothing on screen to say why: tiled is off when it is written
// false, and also when an existing config file leaves it out (see
// EffectiveDefault). lc may be nil.
func StartupLayoutWarnings(lc *LayeredConfig, cfg *UserConfig) []string {
	if lc == nil || cfg == nil || cfg.Startup.Tiled {
		return nil
	}
	holders := lc.Holders([]string{"startup", "layout"})
	if len(holders) == 0 {
		return nil
	}
	file := lc.DisplayPath(holders[len(holders)-1].Path)
	return []string{file + ": [startup] layout is set, but [startup] tiled is off, so new sessions float and do not use the layout. Set tiled = true to use the layout."}
}

// KeyWarnings is every warning about the keys of a loaded config file: tables
// tuios does not have, action names no action has, a [startup] layout that is
// not used, and keys tuios
// cannot read and drops. Reloading the file and applying it through the daemon
// both report them, so a person reads the same list from either.
func KeyWarnings(lc *LayeredConfig, cfg *UserConfig) []string {
	out := UnknownKeybindTables(lc)
	out = append(out, UnknownKeybindActions(lc)...)
	out = append(out, StartupLayoutWarnings(lc, cfg)...)
	return append(out, DroppedWarnings(DropUnreadableKeys(cfg, lc))...)
}
