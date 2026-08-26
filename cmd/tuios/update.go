package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/release"
)

// `tuios update`: replace a binary installed from a release archive with the
// newest one.
//
// The shape of this command is set by one fact. A binary the curl one-liner put
// on the machine has nothing that owns it, so nothing else can update it; every
// other way of installing tuios has an owner, and writing over one of those
// leaves that owner's records describing a file that is no longer there. So the
// first thing this does is work out which of the two it is looking at, and the
// second is refuse loudly if it is the wrong one.
//
// The second fact is that tuios-web is a separate binary that talks to the same
// daemon, and the daemon compares build strings and warns when they differ. An
// update that moved one and not the other would manufacture exactly the
// mismatch that warning exists to catch, so the pair moves together or not at
// all: both are downloaded and verified before either is put in place.

// updateOptions are the flags, gathered so the work can be tested without cobra.
type updateOptions struct {
	// check reports what would happen and changes nothing.
	check bool
	// prerelease allows a prerelease to count as the newest release.
	prerelease bool
	// source is where releases are read from. Nil means GitHub; a test supplies
	// its own and never opens a socket.
	source release.Source
	// out is where the report goes.
	out io.Writer
	// facts describe the running binary. Zero means read them from the process.
	facts *release.Facts
}

// networkTimeout bounds the whole command, download included.
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
	// Checked before the network is touched. Finding out that the directory is
	// read-only after downloading forty megabytes is a worse version of the
	// same refusal.
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
		// A version neither side can read is not an argument for replacing
		// anything. Saying which of the two could not be read is what makes the
		// next step obvious.
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

// resolveFacts reads what is known about the running binary, unless the caller
// supplied it.
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
	// Resolved, because Homebrew and many distributions put a symlink on PATH
	// and the real file somewhere that says who owns it.
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

// refuseToReplace is the message for a binary something else owns.
//
// It names the installer and gives the exact command that does update it. A
// refusal without that command is a dead end, and a user who hits a dead end
// reaches for the curl script, which is how a second copy of tuios ends up
// shadowing the packaged one on PATH.
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

// ownerNoun is what the refusal says would be left out of date.
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

// installRelease downloads, verifies and puts the new binaries in place.
func installRelease(ctx context.Context, src release.Source, rel release.Release,
	facts *release.Facts, dir string, out io.Writer,
) error {
	sums, err := fetchChecksums(ctx, src, rel)
	if err != nil {
		return err
	}

	// The pair moves together, so both are fetched and verified before either
	// is staged, and both are staged before either is committed. A tuios newer
	// than its tuios-web is the mismatch the daemon's build check exists to
	// warn about, and this command must not be the thing that creates one.
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
			s.Discard() // no-op once committed
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

	// Every download is verified by this point, so the only thing left that can
	// fail is the rename itself.
	for i, s := range staged {
		if err := s.Commit(); err != nil {
			if i > 0 {
				// One of the pair moved and the other did not. Said plainly,
				// because the daemon will report a build mismatch and the user
				// needs to know this is why.
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
	reportDaemonAfterUpdate(out)
	return nil
}

// releasePage is where to send someone who has to do this by hand.
func releasePage(rel release.Release) string {
	if rel.URL != "" {
		return rel.URL
	}
	return "https://github.com/" + release.Repo + "/releases"
}

// fetchChecksums reads the release's digest list.
//
// A release with no checksums.txt is refused rather than installed unverified.
// The check is the only thing standing between a redirected download and a
// binary this command puts on PATH, and a check that is skipped whenever it is
// inconvenient is not a check.
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

// maxAssetSize bounds a single download, so a redirect to something enormous
// cannot be read into memory.
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

// explainChecksumError turns a failed verification into a message that says
// what it means. A mismatch is not a hiccup to retry past.
func explainChecksumError(err error, rel release.Release) error {
	var mismatch *release.ChecksumMismatch
	if errors.As(err, &mismatch) {
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

// explainLookupError turns a network or API failure into an instruction.
func explainLookupError(err error, prerelease bool) error {
	var limit *release.RateLimitError
	if errors.As(err, &limit) {
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
	var httpErr *release.HTTPError
	if errors.As(err, &httpErr) {
		return &diagnosticError{
			What:  "GitHub refused the release lookup.",
			Cause: httpErr.Error(),
			Fix:   "try again shortly. If it keeps failing, check https://www.githubstatus.com.",
		}
	}
	// Anything that reaches here is a transport failure: no route, DNS that
	// answers nothing, a proxy that hung up. They read the same to a user, so
	// they get one message.
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

// reportDaemonAfterUpdate says what the new binary does and does not change
// about what is already running.
//
// The daemon holds the old binary open. It goes on running the code it started
// with, and a new client attaching to it gets a build-mismatch warning, so the
// instruction has to be here and not left to be discovered.
func reportDaemonAfterUpdate(out io.Writer) {
	fmt.Fprintln(out, "The daemon is still running the old build. Sessions you have open keep working.")
	fmt.Fprintln(out, "To move them to the new one, detach, then run `tuios kill-server` and start tuios again.")
	fmt.Fprintln(out, "Panes are restored, but programs running in them are not.")
}
