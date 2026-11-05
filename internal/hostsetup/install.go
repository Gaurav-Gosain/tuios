package hostsetup

import (
	"context"
	"runtime"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/hostsync"
	"github.com/Gaurav-Gosain/tuios/internal/release"
)

// The install sources.
const (
	// InstallCopy sends the tuios this machine runs. It needs the same
	// system and architecture, and it is always the same version.
	InstallCopy = "copy"
	// InstallRelease downloads the release of this version for the far
	// machine's platform here, checks it against the release's checksums,
	// and sends it over ssh. The far machine needs no internet.
	InstallRelease = "release"
	// InstallPkg runs the far machine's package manager.
	InstallPkg = "pkg"
	// InstallScript runs the install script there.
	InstallScript = "script"
	// InstallNone installs nothing.
	InstallNone = "none"
)

// The install script. Its address is the one the docs give.
const installScriptURL = "https://raw.githubusercontent.com/" + release.Repo + "/main/install.sh"

// Command is one command a step runs, and where.
type Command struct {
	// Where is "here", or the name of the far machine.
	Where string `json:"where"`
	// Line is the command as a person reads it.
	Line string `json:"line"`
}

// InstallOption is one way to install tuios on the far machine.
type InstallOption struct {
	ID string `json:"id"`
	// Manager is the package manager, with InstallPkg.
	Manager string `json:"manager,omitempty"`
	// Label and Note are what a list shows.
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	// Commands are what the option runs, in order.
	Commands []Command `json:"commands"`
	// Available says the option can run. WhyNot says why it cannot.
	Available bool   `json:"available"`
	WhyNot    string `json:"why_not,omitempty"`
	// NeedsPerson says the option can ask for a password (sudo), so it runs
	// where the person can type it, never inside tuios.
	NeedsPerson bool `json:"needs_person,omitempty"`
	// SameVersion says the option installs the version this machine runs.
	SameVersion bool `json:"same_version,omitempty"`
	// remote is the command the far side runs for a package manager or the
	// script.
	remote string
}

// recipe is one way to install tuios, as data. In the strings, {name} is
// the far machine, {tag} this version's release tag, {exe} this binary,
// {asset} the release archive, {fetch} curl or wget, and {repo} the
// GitHub repository. need is what the machine must have: a package manager,
// "fetch", "copy" or "release".
type recipe struct {
	id, manager, need, label, note, remote string
	person, same                           bool
}

var recipes = []recipe{
	{id: InstallCopy, need: "copy", label: "Copy this tuios ({tag}, {here}).", note: "The same version as here.", same: true},
	{id: InstallRelease, need: "release", label: "The {tag} release for {platform}.", note: "Downloaded here, checked with its sha256, sent over ssh.", same: true},
	{id: InstallPkg, manager: "yay", need: "yay", remote: "yay -S tuios-bin", note: "The AUR package. It asks for your password on {name}.", person: true},
	{id: InstallPkg, manager: "paru", need: "paru", remote: "paru -S tuios-bin", note: "The AUR package. It asks for your password on {name}.", person: true},
	{id: InstallPkg, manager: "brew", need: "brew", remote: "brew install tuios", note: "Homebrew. Its version can differ from {tag}."},
	{id: InstallPkg, manager: "nix", need: "nix", remote: "nix profile install github:{repo}{nixat}#tuios", note: "Nix builds it on {name}.", same: true},
	{id: InstallPkg, manager: "go", need: "go", remote: "go install github.com/{repo}/cmd/tuios@{at}", note: "Go builds it on {name}. It needs Go 1.26 or newer.", same: true},
	{id: InstallScript, need: "fetch", label: "The install script.", remote: "{fetch} " + installScriptURL + " | bash", note: "It installs the newest release. {name} needs internet."},
}

// installOptions lists every way to install tuios on the machine the probe
// read, in the order a list shows them. The first available one is the
// default. A package manager the machine does not have is left out.
func (s *Setup) installOptions() []InstallOption {
	p := s.probe
	here := runtime.GOOS + "/" + runtime.GOARCH
	_, isRelease := release.ParseVersion(s.opts.Version)
	tag := "v" + strings.TrimPrefix(s.opts.Version, "v")
	asset, assetErr := release.AssetName("tuios", tag, p.OS, p.Arch)
	fetch := map[string]string{"curl": "curl -fsSL", "wget": "wget -qO-"}[p.Fetch]
	at, nixat := "latest", ""
	if isRelease {
		at, nixat = tag, "/"+tag
	}
	r := strings.NewReplacer("{at}", at, "{nixat}", nixat, "{name}", s.name, "{tag}", tag, "{here}", here, "{platform}", p.Platform(), "{repo}", release.Repo, "{fetch}", fetch)
	target := s.name + ":~/.local/bin/tuios"
	var out []InstallOption
	for _, rc := range recipes {
		o := InstallOption{ID: rc.id, Manager: rc.manager, Label: r.Replace(rc.label), Note: r.Replace(rc.note), NeedsPerson: rc.person, SameVersion: rc.same, remote: r.Replace(rc.remote), Available: true}
		if o.Label == "" {
			o.Label = o.remote + "."
		}
		switch rc.need {
		case "copy":
			o.Commands = []Command{{"here", "send " + s.opts.Executable + " to " + target}}
			if p.Platform() != here {
				o.Available, o.WhyNot = false, s.name+" runs "+p.Platform()+". This tuios is "+here+"."
			}
		case "release":
			o.Commands = []Command{{"here", "download " + asset + ", check it against " + release.ChecksumFile + ", send it to " + target}}
			switch {
			case !isRelease:
				o.Available, o.WhyNot = false, "This tuios is a dev build. It has no release."
			case assetErr != nil:
				o.Available, o.WhyNot = false, "tuios has no release for "+p.Uname+"."
			}
		case "fetch":
			if fetch == "" {
				o.Available, o.WhyNot = false, s.name+" has no curl and no wget."
			}
		default:
			if !p.Has(rc.need) {
				continue
			}
		}
		if o.remote != "" {
			o.Commands = []Command{{s.name, o.remote}}
		}
		out = append(out, o)
	}
	return out
}

// PersonStep is a command that must run where the person can answer it: a
// host key to check, a password for ssh-copy-id or sudo. The front end runs
// Argv in a terminal, and the setup tests again when it ends.
type PersonStep struct {
	// Why says what the command is for.
	Why string `json:"why"`
	// Line is the command as a person types it here.
	Line string `json:"line"`
	// Argv is the same command as an argument vector.
	Argv []string `json:"-"`
	// URL is a page to open instead of a command, for a Tailscale sign-in.
	URL string `json:"url,omitempty"`
}

// personSSH is a person step that runs remote on the far machine over an
// interactive ssh, with a terminal, so sudo and a package manager can ask.
func (s *Setup) personSSH(why, remote string) *PersonStep {
	argv := append([]string{"ssh", "-t"}, s.host.SSHOptions...)
	argv = append(argv, s.host.Addr, remote)
	words := append([]string{"ssh", "-t"}, s.host.SSHOptions...)
	words = append(words, s.host.Addr)
	return &PersonStep{
		Why:  why,
		Line: shellJoin(words) + " " + shellQuote(remote),
		Argv: argv,
	}
}

// runRemote runs one package manager or script command on the far machine
// in a login shell, with no terminal, and returns what it printed.
func (s *Setup) runRemote(ctx context.Context, remote string) (string, error) {
	script := `for s in "$SHELL" sh; do if [ -x "$s" ] || command -v "$s" >/dev/null 2>&1; then exec "$s" -l -c "` + remote + `" </dev/null 2>&1; fi; done`
	out, stderr, err := s.runner.Run(ctx, script, nil, nil)
	if err != nil {
		tail := clean(hostsync.LastLine(out))
		if tail == "" {
			tail = said(stderr)
		}
		return out, errfmt.Errorf("%s failed on %s: %s", remote, s.name, tail)
	}
	return out, nil
}
