package main

import (
	"fmt"
	"runtime/debug"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// versionReport is the body of `tuios --version`. It names the emulator
// backend because both builds install as the same `tuios` binary, so a bug
// report that does not say which emulator produced it cannot be placed.
func versionReport() string {
	rev, when, dirty := vcsBuildInfo()
	return formatVersion(version, commit, date, builtBy, rev, when, dirty)
}

// formatVersion fills the placeholders the release ldflags would have set with
// the stamps the go command already embeds, so a local build reports where it
// came from instead of "none"/"unknown". Split out from versionReport so the
// fallback is tested against known inputs rather than against whatever this
// test binary happens to be stamped with.
func formatVersion(v, commit, date, builtBy, rev, when string, dirty bool) string {
	if commit == "none" && rev != "" {
		commit = rev
		if dirty {
			commit += " (dirty)"
		}
	}
	if date == "unknown" && when != "" {
		date = when
	}
	if v == "dev" && dirty {
		v = "dev (dirty)"
	}
	return fmt.Sprintf("%s [%s backend]\nCommit: %s\nBuilt: %s\nBy: %s", v, vt.Backend, commit, date, builtBy)
}

// vcsBuildInfo reads the stamps the go command embeds in any binary built from
// a checkout. Every field comes back empty for -buildvcs=false and for a build
// from an unpacked source tarball.
func vcsBuildInfo() (rev, when string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, when, dirty
}
