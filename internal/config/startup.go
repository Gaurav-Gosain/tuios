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
