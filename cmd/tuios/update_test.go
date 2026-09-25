package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
