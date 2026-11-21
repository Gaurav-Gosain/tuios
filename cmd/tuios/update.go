package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"tuios.dev/vt"
)

type updateOptions struct {
	check      bool
	prerelease bool
	source     release.Source
	out        io.Writer
	facts      *release.Facts
}

const networkTimeout = 3 * time.Minute

func runUpdate(opts updateOptions) error {
	out := opts.out
	if out == nil {
		out = os.Stdout
	}

	facts, err := opts.resolveFacts()
	if err != nil {
		return err
	}
	prov := release.Detect(*facts)
	fmt.Fprintf(out, "Installed at %s\n", facts.Path)
	fmt.Fprintf(out, "This build came from %s.\n", prov.What)

	if !prov.Replaceable {
		return refuseToReplace(prov, facts)
	}
	dir := filepath.Dir(facts.Path)
	if !opts.check && !release.Writable(dir) {
		return &diagnosticError{
			What:  fmt.Sprintf("%s is not writable by you.", dir),
			Cause: "The binary lives in a directory this user cannot write to, so it cannot be replaced in place.",
			Fix: "re-run the installer, which asks for sudo when it needs it:\n" +
				"  curl -fsSL https://raw.githubusercontent.com/" + release.Repo + "/main/install.sh | bash",
		}
	}

	src := opts.source
	if src == nil {
		src = release.NewGitHub()
	}
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()

	rel, err := src.Latest(ctx, opts.prerelease)
	if err != nil {
		return explainLookupError(err, opts.prerelease)
	}

	fmt.Fprintf(out, "Running %s, latest is %s", facts.Version, rel.Tag)
	if rel.Prerelease {
		fmt.Fprint(out, " (a prerelease)")
	}
	fmt.Fprintln(out, ".")

	newer, comparable := release.Newer(facts.Version, rel.Tag)
	switch {
	case !comparable:
		return &diagnosticError{
			What: fmt.Sprintf("%q is not a version this can compare against %q.", facts.Version, rel.Tag),
			Cause: "Only a binary built from a published release carries a version tag, " +
				"and this one does not.",
			Fix: "reinstall from a release:\n" +
				"  curl -fsSL https://raw.githubusercontent.com/" + release.Repo + "/main/install.sh | bash",
		}
	case !newer:
		fmt.Fprintln(out, "Already up to date. Nothing to do.")
		return nil
	}

	if opts.check {
		fmt.Fprintf(out, "\n%s is available. Run `tuios update` to install it.\n", rel.Tag)
		return nil
	}
	return installRelease(ctx, src, rel, facts, dir, out)
}

func (opts updateOptions) resolveFacts() (*release.Facts, error) {
	if opts.facts != nil {
		return opts.facts, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, &diagnosticError{
			What:  "This binary's own path could not be read.",
			Cause: err.Error(),
			Fix:   "reinstall with the install script, or use the package manager that installed tuios.",
		}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	facts := &release.Facts{
		Path:       exe,
		BuiltBy:    builtBy,
		Version:    version,
		GOOS:       runtime.GOOS,
		BrewPrefix: os.Getenv("HOMEBREW_PREFIX"),
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		facts.ModuleVersion = info.Main.Version
	}
	return facts, nil
}

func refuseToReplace(prov release.Provenance, facts *release.Facts) error {
	e := &diagnosticError{
		What:  fmt.Sprintf("This tuios was installed by %s, so it is not this command's to replace.", prov.What),
		Cause: "Writing over it would leave " + ownerNoun(prov) + " describing a file that is no longer there.",
		Extra: []string{"Binary: " + facts.Path},
	}
	if prov.Fix != "" {
		e.Fix = prov.Fix
	} else {
		e.Fix = "update it the way you installed it."
	}
	return e
}

func ownerNoun(prov release.Provenance) string {
	switch prov.Origin {
	case release.OriginNixStore:
		return "the Nix store"
	case release.OriginHomebrew:
		return "Homebrew's records"
	case release.OriginSystemPackage:
		return "the package database"
	case release.OriginGoInstall:
		return "the Go build cache"
	case release.OriginSourceScript:
		return "your checkout"
	}
	return "whatever installed it"
}

func installRelease(ctx context.Context, src release.Source, rel release.Release,
	facts *release.Facts, dir string, out io.Writer,
) error {
	sums, err := fetchChecksums(ctx, src, rel)
	if err != nil {
		return err
	}

	wanted := []string{"tuios"}
	webPath := filepath.Join(dir, release.ExecutableName("tuios-web"))
	if _, err := os.Stat(webPath); err == nil {
		wanted = append(wanted, "tuios-web")
	} else {
		fmt.Fprintln(out, "No tuios-web beside it, so only tuios is being updated.")
	}

	var staged []*release.Staged
	defer func() {
		for _, s := range staged {
			s.Discard()
		}
	}()

	for _, binary := range wanted {
		name, err := release.AssetName(binary, rel.Tag, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return &diagnosticError{
				What:  fmt.Sprintf("There is no release archive this command can pick for %s/%s.", runtime.GOOS, runtime.GOARCH),
				Cause: err.Error(),
				Fix:   "download the right archive by hand from " + releasePage(rel),
			}
		}
		if binary == "tuios" && vt.Backend == "ghostty" {
			ghosttyName, err := release.AssetName("tuios-ghostty", rel.Tag, runtime.GOOS, runtime.GOARCH)
			if err == nil {
				if _, ok := rel.AssetNamed(ghosttyName); ok {
					name = ghosttyName
				} else {
					fmt.Fprintf(out, "%s publishes no %s, so this installs the pure Go backend.\n", rel.Tag, ghosttyName)
				}
			}
		}
		asset, ok := rel.AssetNamed(name)
		if !ok {
			return &diagnosticError{
				What:  fmt.Sprintf("%s publishes no %s.", rel.Tag, name),
				Cause: "This platform has no archive in that release.",
				Fix:   "see what it does publish at " + releasePage(rel),
			}
		}

		fmt.Fprintf(out, "Downloading %s...\n", name)
		archive, err := readAsset(ctx, src, asset)
		if err != nil {
			return err
		}
		if err := sums.Verify(name, archive); err != nil {
			return explainChecksumError(err, rel)
		}

		exe := release.ExecutableName(binary)
		data, err := release.BinaryFromArchive(bytes.NewReader(archive), exe)
		if err != nil {
			return &diagnosticError{
				What:  fmt.Sprintf("%s did not hold %s.", name, exe),
				Cause: err.Error(),
				Fix:   "report this at https://github.com/" + release.Repo + "/issues, naming the release " + rel.Tag,
			}
		}

		target := filepath.Join(dir, exe)
		s, err := release.Stage(target, data, release.BinaryMode(target))
		if err != nil {
			return err
		}
		staged = append(staged, s)
	}

	for i, s := range staged {
		if err := s.Commit(); err != nil {
			if i > 0 {
				return &diagnosticError{
					What:  "The update was applied to only one of the two binaries.",
					Cause: err.Error(),
					Extra: []string{"tuios and tuios-web now report different versions."},
					Fix: "re-run the installer to put both at the same version:\n" +
						"  curl -fsSL https://raw.githubusercontent.com/" + release.Repo + "/main/install.sh | bash",
				}
			}
			return err
		}
		fmt.Fprintf(out, "Replaced %s\n", s.Target)
	}

	fmt.Fprintf(out, "\nUpdated to %s.\n", rel.Tag)
	updateFirstPartyPlugins(filepath.Join(dir, release.ExecutableName("tuios")), out)
	reportDaemonAfterUpdate(out)
	return nil
}

func updateFirstPartyPlugins(bin string, out io.Writer) {
	path, err := pluginsConfigPath()
	if err != nil {
		return
	}
	lock, err := herdrplugin.ReadLock(herdrplugin.DefaultDirs(path, nil).LockPath())
	if err != nil {
		return
	}
	args := []string{"plugins", "update"}
	for _, e := range lock.Plugins {
		if e.Release() {
			args = append(args, e.ID)
		}
	}
	if len(args) == 2 {
		return
	}
	fmt.Fprintln(out, "\nUpdating the plugins of the tuios release.")
	cmd := exec.Command(bin, args...) //nolint:gosec // the binary this command just installed
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, out
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(out, "Some plugins were not updated, and they keep running as before. Run tuios plugins update to try again.")
	}
}

func releasePage(rel release.Release) string {
	if rel.URL != "" {
		return rel.URL
	}
	return "https://github.com/" + release.Repo + "/releases"
}

func fetchChecksums(ctx context.Context, src release.Source, rel release.Release) (release.Checksums, error) {
	asset, ok := rel.AssetNamed(release.ChecksumFile)
	if !ok {
		return nil, &diagnosticError{
			What:  fmt.Sprintf("%s publishes no %s.", rel.Tag, release.ChecksumFile),
			Cause: "Without it there is nothing to check a download against, and this will not install an unverified binary.",
			Fix:   "download and verify the archive by hand from " + releasePage(rel),
		}
	}
	data, err := readAsset(ctx, src, asset)
	if err != nil {
		return nil, err
	}
	sums, err := release.ParseChecksums(bytes.NewReader(data))
	if err != nil {
		return nil, &diagnosticError{
			What:  "The release's checksum list could not be read.",
			Cause: err.Error(),
			Fix:   "download and verify the archive by hand from " + releasePage(rel),
		}
	}
	return sums, nil
}

const maxAssetSize = 512 << 20

func readAsset(ctx context.Context, src release.Source, asset release.Asset) ([]byte, error) {
	body, err := src.Fetch(ctx, asset.URL)
	if err != nil {
		return nil, explainLookupError(err, false)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, maxAssetSize+1))
	if err != nil {
		return nil, &diagnosticError{
			What:  "The download did not finish.",
			Cause: err.Error(),
			Fix:   "check the network and run `tuios update` again.",
		}
	}
	if int64(len(data)) > maxAssetSize {
		return nil, &diagnosticError{
			What:  asset.Name + " is larger than this command will read.",
			Cause: "A release archive is tens of megabytes. Something answered with far more than that.",
			Fix:   "download it by hand and check what you are being served.",
		}
	}
	return data, nil
}

func explainChecksumError(err error, rel release.Release) error {
	if mismatch, ok := errors.AsType[*release.ChecksumMismatch](err); ok {
		return &diagnosticError{
			What:  "The downloaded archive is not the one the release published.",
			Cause: "Its checksum does not match. The download was corrupted, or something between here and GitHub changed it.",
			Extra: []string{
				"Expected: " + mismatch.Want,
				"Got:      " + mismatch.Got,
				"Nothing was installed and the old binary is untouched.",
			},
			Fix: "run `tuios update` again. If it fails the same way, download the archive by hand from " +
				releasePage(rel) + " and check it yourself.",
		}
	}
	return &diagnosticError{
		What:  "The download could not be verified.",
		Cause: err.Error(),
		Extra: []string{"Nothing was installed and the old binary is untouched."},
		Fix:   "download and verify the archive by hand from " + releasePage(rel),
	}
}

func explainLookupError(err error, prerelease bool) error {
	if limit, ok := errors.AsType[*release.RateLimitError](err); ok {
		e := &diagnosticError{
			What:  "GitHub is rate limiting this address.",
			Cause: "Sixty release lookups an hour are allowed without a token, shared by everyone on this address.",
			Fix:   "wait and run it again, or set GITHUB_TOKEN to a token with no scopes to raise the limit.",
		}
		if !limit.Reset.IsZero() {
			e.Extra = []string{"The limit resets at " + limit.Reset.Local().Format(time.Kitchen) + "."}
		}
		return e
	}
	if errors.Is(err, release.ErrNoRelease) {
		what := "This repository has published no release."
		if prerelease {
			what = "This repository has published no release or prerelease."
		}
		return &diagnosticError{
			What:  what,
			Cause: "There is nothing to update to.",
			Fix:   "see https://github.com/" + release.Repo + "/releases",
		}
	}
	if httpErr, ok := errors.AsType[*release.HTTPError](err); ok {
		return &diagnosticError{
			What:  "GitHub refused the release lookup.",
			Cause: httpErr.Error(),
			Fix:   "try again shortly. If it keeps failing, check https://www.githubstatus.com.",
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return &diagnosticError{
			What:  "GitHub could not be reached.",
			Cause: err.Error(),
			Fix:   "check the network, or set HTTPS_PROXY if this machine goes out through a proxy.",
		}
	}
	return &diagnosticError{
		What:  "The release lookup failed.",
		Cause: err.Error(),
		Fix:   "check the network and run `tuios update` again.",
	}
}

func reportDaemonAfterUpdate(out io.Writer) {
	fmt.Fprintln(out, "The daemon is still running the old build. Sessions you have open keep working.")
	fmt.Fprintln(out, "To move it to the new one and keep every program running, run `"+serverCommand("daemon upgrade")+"` (experimental).")
	fmt.Fprintln(out, "It says why when the daemon cannot be upgraded live. `"+serverCommand("restart-server")+"`, or \"Restart the daemon\" in the command palette, then restarts it: the panes come back, but the programs in them stop.")
}
