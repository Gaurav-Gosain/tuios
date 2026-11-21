package main

import (
	"fmt"
	"runtime/debug"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"tuios.dev/vt"
)

func versionReport() string {
	rev, when, dirty := vcsBuildInfo()
	return formatVersion(version, commit, date, builtBy, rev, when, dirty)
}

func formatVersion(v, commit, date, builtBy, rev, when string, dirty bool) string {
	if commit == "none" && rev != "" {
		commit = rev
		if dirty {
			commit += "-dirty"
		}
	}
	if date == "unknown" && when != "" {
		date = when
	}
	report := fmt.Sprintf("%s [%s backend]\nCommit: %s\nBuilt: %s\nBy: %s", v, vt.Backend, commit, date, builtBy)
	if lib := emulatorLibrary(); lib != "" {
		report += "\nLibrary: " + lib
	}
	return report
}

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

func applyBuildIdentity() {
	s := app.SetBuildIdentity(release.Stamps{Version: version, Commit: commit, Date: date, BuiltBy: builtBy})
	version, commit, date, builtBy = s.Version, s.Commit, s.Date, s.BuiltBy
}
