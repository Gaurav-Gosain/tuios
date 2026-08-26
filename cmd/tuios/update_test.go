package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/release"
)

// Nothing here touches the network. The release lookup is behind
// release.Source, and every case below supplies a fake built in memory: a test
// that reached api.github.com would fail on a plane, fail behind a proxy, and
// fail once the hourly limit was spent, which are three ways to make a real
// regression indistinguishable from the weather.

// fakeSource is a release and its assets, served from memory.
type fakeSource struct {
	rel    release.Release
	bodies map[string][]byte
	// err, when set, is what Latest returns instead of a release.
	err error
	// fetched records what was downloaded, in order, so a case can assert that
	// nothing was fetched at all.
	fetched []string
}

func (f *fakeSource) Latest(_ context.Context, _ bool) (release.Release, error) {
	if f.err != nil {
		return release.Release{}, f.err
	}
	return f.rel, nil
}

func (f *fakeSource) Fetch(_ context.Context, url string) (io.ReadCloser, error) {
	f.fetched = append(f.fetched, url)
	body, ok := f.bodies[url]
	if !ok {
		return nil, &release.HTTPError{Status: 404, URL: url}
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// buildRelease makes a release whose archives really contain the named
// binaries, with a checksums.txt that really matches them. Building it properly
// is what lets the checksum case below alter one byte and mean something.
func buildRelease(t *testing.T, tag string, binaries map[string]string) *fakeSource {
	t.Helper()
	src := &fakeSource{
		rel:    release.Release{Tag: tag, URL: "https://example.invalid/releases/" + tag},
		bodies: map[string][]byte{},
	}
	var sums strings.Builder
	for binary, contents := range binaries {
		name, err := release.AssetName(binary, tag, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			t.Skipf("no asset name for %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
		}
		archive := tarGzOne(t, release.ExecutableName(binary), contents)
		url := "https://example.invalid/" + name
		src.bodies[url] = archive
		src.rel.Assets = append(src.rel.Assets, release.Asset{Name: name, URL: url, Size: int64(len(archive))})
		sum := sha256.Sum256(archive)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	checksumURL := "https://example.invalid/" + release.ChecksumFile
	src.bodies[checksumURL] = []byte(sums.String())
	src.rel.Assets = append(src.rel.Assets, release.Asset{Name: release.ChecksumFile, URL: checksumURL})
	return src
}

func tarGzOne(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// installedTree lays out a directory the way the curl script leaves one, and
// returns the facts a binary in it would report.
func installedTree(t *testing.T, currentVersion string, withWeb bool) (dir string, facts *release.Facts) {
	t.Helper()
	dir = t.TempDir()
	tuios := filepath.Join(dir, release.ExecutableName("tuios"))
	if err := os.WriteFile(tuios, []byte("old tuios"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withWeb {
		web := filepath.Join(dir, release.ExecutableName("tuios-web"))
		if err := os.WriteFile(web, []byte("old tuios-web"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir, &release.Facts{
		Path:    tuios,
		BuiltBy: "goreleaser",
		Version: currentVersion,
		GOOS:    runtime.GOOS,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - the test's own temp dir
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// TestUpdateMovesBothBinariesTogether. tuios and tuios-web are separate
// binaries that talk to one daemon, and the daemon compares their versions and
// warns when they differ. An update that moved one and not the other would
// manufacture the exact mismatch that warning exists to catch.
//
// Negative control: drop tuios-web from the wanted list in installRelease and
// this fails on the web binary's contents.
func TestUpdateMovesBothBinariesTogether(t *testing.T) {
	dir, facts := installedTree(t, "v0.7.0", true)
	src := buildRelease(t, "v0.8.0", map[string]string{
		"tuios":     "new tuios",
		"tuios-web": "new tuios-web",
	})

	var out bytes.Buffer
	if err := runUpdate(updateOptions{source: src, facts: facts, out: &out}); err != nil {
		t.Fatalf("runUpdate: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "new tuios" {
		t.Errorf("tuios holds %q", got)
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios-web"))); got != "new tuios-web" {
		t.Errorf("tuios-web holds %q", got)
	}
	if !strings.Contains(out.String(), "v0.8.0") {
		t.Errorf("the report does not name the version it installed:\n%s", out.String())
	}
}

// TestUpdateSaysWhatToDoAboutTheRunningDaemon. The daemon holds the old binary
// open and goes on running it, so a user who does nothing gets a build-mismatch
// warning on their next attach with no idea why.
//
// Negative control: delete reportDaemonAfterUpdate's call and this fails.
func TestUpdateSaysWhatToDoAboutTheRunningDaemon(t *testing.T) {
	_, facts := installedTree(t, "v0.7.0", false)
	src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})

	var out bytes.Buffer
	if err := runUpdate(updateOptions{source: src, facts: facts, out: &out}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	text := out.String()
	for _, want := range []string{"daemon", "kill-server"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report never mentions %q:\n%s", want, text)
		}
	}
}

// TestUpdateSkipsWebWhenItIsNotInstalled, and says so rather than silently
// updating half of what the user might expect.
//
// Negative control: always add tuios-web to the wanted list and this fails,
// because the release has no web asset here.
func TestUpdateSkipsWebWhenItIsNotInstalled(t *testing.T) {
	dir, facts := installedTree(t, "v0.7.0", false)
	src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})

	var out bytes.Buffer
	if err := runUpdate(updateOptions{source: src, facts: facts, out: &out}); err != nil {
		t.Fatalf("runUpdate: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "new tuios" {
		t.Errorf("tuios holds %q", got)
	}
	if !strings.Contains(out.String(), "No tuios-web") {
		t.Errorf("the report does not say tuios-web was skipped:\n%s", out.String())
	}
}

// TestCheckInstallsNothing. --check has to be safe to run from a cron job.
//
// Negative control: let --check fall through to installRelease and this fails
// on the untouched-binary check.
func TestCheckInstallsNothing(t *testing.T) {
	dir, facts := installedTree(t, "v0.7.0", true)
	src := buildRelease(t, "v0.8.0", map[string]string{
		"tuios":     "new tuios",
		"tuios-web": "new tuios-web",
	})

	var out bytes.Buffer
	if err := runUpdate(updateOptions{source: src, facts: facts, out: &out, check: true}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "old tuios" {
		t.Errorf("--check replaced the binary: %q", got)
	}
	if len(src.fetched) != 0 {
		t.Errorf("--check downloaded %v", src.fetched)
	}
	if !strings.Contains(out.String(), "v0.8.0 is available") {
		t.Errorf("--check does not report the newer release:\n%s", out.String())
	}
}

// TestUpToDateDoesNothing, and does not download an archive to find that out.
//
// Negative control: compare versions after downloading and this fails on the
// fetch count.
func TestUpToDateDoesNothing(t *testing.T) {
	dir, facts := installedTree(t, "v0.8.0", false)
	src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})

	var out bytes.Buffer
	if err := runUpdate(updateOptions{source: src, facts: facts, out: &out}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "old tuios" {
		t.Errorf("an up-to-date binary was replaced: %q", got)
	}
	if len(src.fetched) != 0 {
		t.Errorf("an up-to-date binary still downloaded %v", src.fetched)
	}
	if !strings.Contains(out.String(), "Already up to date") {
		t.Errorf("no report of being up to date:\n%s", out.String())
	}
}

// TestABadChecksumInstallsNothing is the case the verification exists for. A
// mismatched archive must leave both binaries exactly as they were, and the
// message must say so, because a user who is told "checksum failed" and nothing
// else does not know whether they are now half updated.
//
// Negative control: skip the Verify call in installRelease and this fails: the
// altered archive installs.
func TestABadChecksumInstallsNothing(t *testing.T) {
	dir, facts := installedTree(t, "v0.7.0", true)
	src := buildRelease(t, "v0.8.0", map[string]string{
		"tuios":     "new tuios",
		"tuios-web": "new tuios-web",
	})
	// Alter the tuios archive after its digest was published, which is what a
	// corrupted download or a tampering proxy looks like from here.
	name, err := release.AssetName("tuios", "v0.8.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	url := "https://example.invalid/" + name
	src.bodies[url] = append(src.bodies[url], 0x00)

	var out bytes.Buffer
	err = runUpdate(updateOptions{source: src, facts: facts, out: &out})
	if err == nil {
		t.Fatal("an archive that did not match its checksum was installed")
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "old tuios" {
		t.Errorf("tuios was replaced despite the mismatch: %q", got)
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios-web"))); got != "old tuios-web" {
		t.Errorf("tuios-web was replaced despite the mismatch: %q", got)
	}
	if !strings.Contains(err.Error(), "untouched") {
		t.Errorf("the message does not say the old binary is untouched:\n%v", err)
	}
}

// TestAMissingChecksumFileIsRefused. A check that is skipped whenever it is
// inconvenient is not a check.
//
// Negative control: install without a digest when checksums.txt is absent and
// this fails.
func TestAMissingChecksumFileIsRefused(t *testing.T) {
	dir, facts := installedTree(t, "v0.7.0", false)
	src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})
	// Drop the checksum asset, as an incompletely uploaded release would.
	var kept []release.Asset
	for _, a := range src.rel.Assets {
		if a.Name != release.ChecksumFile {
			kept = append(kept, a)
		}
	}
	src.rel.Assets = kept

	if err := runUpdate(updateOptions{source: src, facts: facts, out: io.Discard}); err == nil {
		t.Fatal("a release with no checksums.txt was installed anyway")
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "old tuios" {
		t.Errorf("tuios was replaced: %q", got)
	}
}

// TestUpdateRefusesWhatItDoesNotOwn, naming the installer and the command that
// does update it.
//
// Negative control: drop the Replaceable check in runUpdate and every row here
// proceeds to a download.
func TestUpdateRefusesWhatItDoesNotOwn(t *testing.T) {
	cases := []struct {
		name  string
		facts release.Facts
		fix   string
	}{
		{"nix", release.Facts{Path: "/nix/store/x-tuios/bin/tuios"}, "nix profile"},
		{"homebrew", release.Facts{Path: "/opt/homebrew/Cellar/tuios/0.7.0/bin/tuios"}, "brew upgrade"},
		{"system package", release.Facts{Path: "/usr/bin/tuios", BuiltBy: "goreleaser"}, "package manager"},
		{"source build", release.Facts{Path: "/home/x/.local/bin/tuios", BuiltBy: "install.sh"}, "scripts/install.sh"},
		{"go install", release.Facts{Path: "/home/x/go/bin/tuios", ModuleVersion: "v0.7.0"}, "go install"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})
			facts := tc.facts
			err := runUpdate(updateOptions{source: src, facts: &facts, out: io.Discard})
			if err == nil {
				t.Fatal("the update was not refused")
			}
			if !strings.Contains(err.Error(), tc.fix) {
				t.Errorf("the refusal does not name %q:\n%v", tc.fix, err)
			}
			if len(src.fetched) != 0 {
				t.Errorf("a refused update still downloaded %v", src.fetched)
			}
		})
	}
}

// TestRefusalHappensBeforeTheNetwork, stated on its own because it is what
// makes `tuios update` safe to run on a machine with no network and a packaged
// binary: the answer is the same either way.
//
// Negative control: look up the release before calling Detect and this fails
// with the transport error instead of the refusal.
func TestRefusalHappensBeforeTheNetwork(t *testing.T) {
	src := &fakeSource{err: errors.New("dial tcp: no route to host")}
	facts := release.Facts{Path: "/nix/store/x-tuios/bin/tuios"}
	err := runUpdate(updateOptions{source: src, facts: &facts, out: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "Nix") {
		t.Errorf("got %v, want the Nix refusal rather than a network error", err)
	}
}

// TestUnreachableGitHubIsExplained rather than reported as a raw dial error.
//
// Negative control: return the transport error unwrapped and this fails on the
// proxy hint, which is the one thing a user behind a corporate proxy needs.
func TestUnreachableGitHubIsExplained(t *testing.T) {
	_, facts := installedTree(t, "v0.7.0", false)
	src := &fakeSource{err: &net.OpError{Op: "dial", Err: errors.New("no route to host")}}

	err := runUpdate(updateOptions{source: src, facts: facts, out: io.Discard})
	if err == nil {
		t.Fatal("an unreachable GitHub was not reported")
	}
	for _, want := range []string{"could not be reached", "PROXY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not mention %q:\n%v", want, err)
		}
	}
}

// TestRateLimitNamesTheResetAndTheWayAround it, since "try again later" with no
// time is not an instruction.
//
// Negative control: report the rate limit as a plain HTTP 403 and this fails.
func TestRateLimitNamesTheResetAndTheWayAround(t *testing.T) {
	_, facts := installedTree(t, "v0.7.0", false)
	reset := time.Now().Add(20 * time.Minute)
	src := &fakeSource{err: &release.RateLimitError{Reset: reset}}

	err := runUpdate(updateOptions{source: src, facts: facts, out: io.Discard})
	if err == nil {
		t.Fatal("the rate limit was not reported")
	}
	for _, want := range []string{"rate limiting", "GITHUB_TOKEN", reset.Local().Format(time.Kitchen)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not mention %q:\n%v", want, err)
		}
	}
}

// TestANonReleaseBuildIsNotToldItIsOutOfDate. A "dev+sha" build has no version
// to compare, and telling its owner to update would be telling them to throw
// away the build they made on purpose.
//
// Negative control: treat an unparsable version as older than everything and
// this fails: the source build downloads and installs a release over itself.
func TestANonReleaseBuildIsNotToldItIsOutOfDate(t *testing.T) {
	dir, facts := installedTree(t, "dev+abc123def456", false)
	// Still stamped goreleaser, so provenance passes and only the version
	// comparison stands between this and an install.
	src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})

	err := runUpdate(updateOptions{source: src, facts: facts, out: io.Discard})
	if err == nil {
		t.Fatal("an unversioned build was updated without comment")
	}
	if got := readFile(t, filepath.Join(dir, release.ExecutableName("tuios"))); got != "old tuios" {
		t.Errorf("the binary was replaced: %q", got)
	}
	if len(src.fetched) != 0 {
		t.Errorf("an unversioned build downloaded %v", src.fetched)
	}
}

// TestAPlatformWithNoArchiveIsExplained rather than 404ing.
//
// Negative control: skip the AssetNamed check and this fails with a download
// error instead of a message naming the release page.
func TestAPlatformWithNoArchiveIsExplained(t *testing.T) {
	_, facts := installedTree(t, "v0.7.0", false)
	src := buildRelease(t, "v0.8.0", map[string]string{"tuios": "new tuios"})
	// A release that published only the checksum list, as a partial upload
	// would leave it.
	src.rel.Assets = []release.Asset{{
		Name: release.ChecksumFile,
		URL:  "https://example.invalid/" + release.ChecksumFile,
	}}

	err := runUpdate(updateOptions{source: src, facts: facts, out: io.Discard})
	if err == nil {
		t.Fatal("a release with no archive for this platform was not reported")
	}
	if !strings.Contains(err.Error(), "publishes no") {
		t.Errorf("the message does not say the release lacks the archive:\n%v", err)
	}
}

// TestUpdateIsRegistered. Cobra registers by value, so a command declared and
// never added to the root compiles, passes vet, and is absent from the binary.
//
// Negative control: leave updateCmd out of the AddCommand call and this fails.
func TestUpdateIsRegistered(t *testing.T) {
	cmd, _, err := newRootCommand().Find([]string{"update"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if cmd.Name() != "update" {
		t.Fatalf("resolved to %q", cmd.Name())
	}
	for _, flag := range []string{"check", "pre"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("--%s is not a flag on `tuios update`", flag)
		}
	}
	// The help has to say what this will not touch, because the refusal is the
	// command's main behaviour for most of the ways tuios is installed.
	flat := strings.Join(strings.Fields(cmd.Long), " ")
	for _, want := range []string{"release archive", "checksum", "tuios-web", "daemon"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the help never mentions %q", want)
		}
	}
}
