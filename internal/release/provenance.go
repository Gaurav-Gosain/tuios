package release

import (
	"path/filepath"
	"strings"
)

// Origin is how a tuios binary got onto this machine.
type Origin int

const (
	// OriginRelease is a binary from a release archive: the curl one-liner, or
	// the same archive unpacked by hand. This is the only one an update
	// replaces, because replacing it puts back exactly what put it there.
	OriginRelease Origin = iota
	// OriginSourceScript is scripts/install.sh, which builds from a checkout.
	// A release archive is not what that user asked for.
	OriginSourceScript
	// OriginGoInstall is `go install`, whose binary belongs to the Go tool.
	OriginGoInstall
	// OriginNixStore is a path under /nix/store, which is read-only by design.
	OriginNixStore
	// OriginHomebrew is a Homebrew cask or formula.
	OriginHomebrew
	// OriginSystemPackage is a distribution package: the AUR build, or anything
	// else that owns /usr/bin.
	OriginSystemPackage
	// OriginUnknown is everything else, including a container image. Refused,
	// because an update that cannot say what it is about to overwrite should
	// not overwrite it.
	OriginUnknown
)

// Facts are what is known about the running binary. Every field is supplied by
// the caller rather than read here, so the decision can be tested without a
// filesystem.
type Facts struct {
	// Path is the binary's own path with symlinks resolved. Resolved matters:
	// Homebrew puts a symlink in bin and the real file in the Cellar, and only
	// the resolved path says so.
	Path string
	// BuiltBy is the main.builtBy ldflag: "goreleaser", "install.sh", or
	// "unknown".
	BuiltBy string
	// Version is the main.version ldflag: a tag for a release build, "dev" or
	// "dev+<sha>" otherwise.
	Version string
	// ModuleVersion is the module version the go command stamped, empty when
	// there is none. A `go install` build has one and a release build does not
	// need one, so it is only consulted to tell `go install` from an unlabelled
	// binary.
	ModuleVersion string
	// GOOS is the platform, so the Homebrew and Nix rules can be applied where
	// they mean something.
	GOOS string
	// BrewPrefix is $HOMEBREW_PREFIX when it is set. Optional: the Cellar and
	// Caskroom path tests catch a Homebrew install without it.
	BrewPrefix string
}

// Provenance is the answer, with the words to say when it is a refusal.
type Provenance struct {
	Origin Origin
	// Replaceable is whether `tuios update` may write over this binary.
	Replaceable bool
	// What names the installer, for a message: "a Homebrew cask".
	What string
	// Fix is the exact command that does update this binary, or "" when there
	// is no single one.
	Fix string
}

// Detect works out where the running binary came from.
//
// The order is the whole of the logic. Path is consulted before the build
// stamp, because a distribution package is built by goreleaser too: the AUR
// package installs the same release binary into /usr/bin, so its stamp says
// "goreleaser" and only its path says the package manager owns it. Deciding on
// the stamp first would happily overwrite it.
func Detect(f Facts) Provenance {
	p := filepath.ToSlash(f.Path)

	switch {
	case strings.HasPrefix(p, "/nix/store/"):
		return Provenance{
			Origin: OriginNixStore,
			What:   "the Nix store",
			Fix:    "nix profile upgrade tuios",
		}
	case isHomebrewPath(p, f.BrewPrefix):
		fix := "brew upgrade tuios"
		switch {
		case strings.Contains(p, "/Caskroom/tuios-ghostty/"):
			fix = "brew upgrade --cask tuios-ghostty"
		case strings.Contains(p, "/Caskroom/"):
			fix = "brew upgrade --cask tuios"
		}
		return Provenance{
			Origin: OriginHomebrew,
			What:   "Homebrew",
			Fix:    fix,
		}
	case isSystemPath(p):
		return Provenance{
			Origin: OriginSystemPackage,
			What:   "a system package",
			Fix:    "use your package manager, for example: yay -S tuios-bin",
		}
	case f.BuiltBy == "install.sh":
		return Provenance{
			Origin: OriginSourceScript,
			What:   "a build from source by scripts/install.sh",
			Fix:    "git pull && ./scripts/install.sh",
		}
	case f.BuiltBy == "goreleaser":
		return Provenance{
			Origin:      OriginRelease,
			Replaceable: true,
			What:        "a release archive",
		}
	case f.ModuleVersion != "" && f.ModuleVersion != "(devel)":
		return Provenance{
			Origin: OriginGoInstall,
			What:   "go install",
			Fix:    "go install github.com/Gaurav-Gosain/tuios/cmd/tuios@latest",
		}
	}
	return Provenance{
		Origin: OriginUnknown,
		What:   "an unknown build",
		Fix:    "reinstall with: curl -fsSL https://raw.githubusercontent.com/" + Repo + "/main/install.sh | bash",
	}
}

func isHomebrewPath(p, brewPrefix string) bool {
	if strings.Contains(p, "/Cellar/") || strings.Contains(p, "/Caskroom/") {
		return true
	}
	if strings.Contains(p, "/homebrew/Cellar") || strings.Contains(p, "/linuxbrew/") {
		return true
	}
	if brewPrefix != "" {
		prefix := strings.TrimSuffix(filepath.ToSlash(brewPrefix), "/")
		if strings.HasPrefix(p, prefix+"/bin/") || strings.HasPrefix(p, prefix+"/Cellar/") {
			return true
		}
	}
	return false
}

func isSystemPath(p string) bool {
	for _, dir := range []string{"/usr/bin/", "/usr/sbin/", "/bin/", "/sbin/", "/usr/lib/"} {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}

// String is the origin's name, for messages and tests.
func (o Origin) String() string {
	switch o {
	case OriginRelease:
		return "release"
	case OriginSourceScript:
		return "source-script"
	case OriginGoInstall:
		return "go-install"
	case OriginNixStore:
		return "nix"
	case OriginHomebrew:
		return "homebrew"
	case OriginSystemPackage:
		return "system-package"
	}
	return "unknown"
}
