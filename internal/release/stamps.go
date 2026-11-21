package release

import (
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

// Stamps are the build identity the release ldflags set in a main package:
// main.version, main.commit, main.date and main.builtBy.
type Stamps struct {
	Version, Commit, Date, BuiltBy string
}

var pseudoVersion = regexp.MustCompile(`[-.](\d{14})-([0-9a-f]{12})$`)

// WithModuleVersion fills the placeholder stamps from the module version the
// go command records for `go install github.com/Gaurav-Gosain/tuios/cmd/...@v`.
// Such a build carries no ldflags and, being built from the module zip rather
// than a checkout, no vcs stamps either, so without this it reports "dev".
// It only applies when the main module was downloaded (Main.Sum is set): a
// build in a checkout keeps "dev" or whatever scripts/install.sh passed.
func (s Stamps) WithModuleVersion() Stamps {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Sum == "" {
		return s
	}
	return s.fromModule(info.Main.Version)
}

func (s Stamps) fromModule(mv string) Stamps {
	if s.Version != "dev" || mv == "" || mv == "(devel)" {
		return s
	}
	s.Version = strings.TrimPrefix(mv, "v")
	if m := pseudoVersion.FindStringSubmatch(mv); m != nil {
		if s.Commit == "none" {
			s.Commit = m[2]
		}
		if t, err := time.Parse("20060102150405", m[1]); err == nil && s.Date == "unknown" {
			s.Date = t.UTC().Format(time.RFC3339)
		}
	}
	if s.BuiltBy == "unknown" {
		s.BuiltBy = "go install"
	}
	return s
}
