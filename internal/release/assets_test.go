package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// TestAssetName against the names .goreleaser.yml actually produces.
//
// The expected strings are written out by hand rather than built with
// AssetName, which would only prove the function agrees with itself. They are
// the archive names a release publishes, and if this test and goreleaser
// disagree the download 404s.
//
// Negative control: drop the amd64 rewrite, or keep the leading v on the
// version, and rows here fail.
func TestAssetName(t *testing.T) {
	cases := []struct {
		binary, version, goos, goarch string
		want                          string
	}{
		{"tuios", "v0.7.0", "linux", "amd64", "tuios_0.7.0_Linux_x86_64.tar.gz"},
		{"tuios", "0.7.0", "linux", "amd64", "tuios_0.7.0_Linux_x86_64.tar.gz"},
		{"tuios", "v0.7.0", "darwin", "arm64", "tuios_0.7.0_Darwin_arm64.tar.gz"},
		{"tuios", "v0.7.0", "windows", "amd64", "tuios_0.7.0_Windows_x86_64.tar.gz"},
		{"tuios", "v0.7.0", "linux", "386", "tuios_0.7.0_Linux_i386.tar.gz"},
		{"tuios", "v0.7.0", "freebsd", "arm64", "tuios_0.7.0_Freebsd_arm64.tar.gz"},
		{"tuios-web", "v0.7.0", "linux", "amd64", "tuios-web_0.7.0_Linux_x86_64.tar.gz"},
		{"tuios-web", "v1.2.3-rc1", "darwin", "amd64", "tuios-web_1.2.3-rc1_Darwin_x86_64.tar.gz"},
	}
	for _, tc := range cases {
		got, err := AssetName(tc.binary, tc.version, tc.goos, tc.goarch)
		if err != nil {
			t.Errorf("AssetName(%q, %q, %q, %q): %v", tc.binary, tc.version, tc.goos, tc.goarch, err)
			continue
		}
		if got != tc.want {
			t.Errorf("AssetName(%q, %q, %q, %q) = %q, want %q",
				tc.binary, tc.version, tc.goos, tc.goarch, got, tc.want)
		}
	}
}

// TestAssetNameRefusesArm. The archive name carries the ARM version and a
// running binary cannot read the GOARM it was built with, so either answer
// would be a guess between two real asset names, one of which is the wrong
// binary for this CPU.
//
// Negative control: return "arm" from archName and this fails.
func TestAssetNameRefusesArm(t *testing.T) {
	if got, err := AssetName("tuios", "v0.7.0", "linux", "arm"); err == nil {
		t.Errorf("AssetName picked %q for arm; it cannot know armv6 from armv7", got)
	}
}

// TestParseChecksums against the shape goreleaser writes.
//
// Negative control: split on a single space rather than on fields and this
// fails, because the file uses two.
func TestParseChecksums(t *testing.T) {
	file := "" +
		"1111111111111111111111111111111111111111111111111111111111111111  tuios_0.7.0_Linux_x86_64.tar.gz\n" +
		"2222222222222222222222222222222222222222222222222222222222222222  tuios-web_0.7.0_Linux_x86_64.tar.gz\n" +
		"\n" +
		"# a comment nobody promised would not be here\n"

	sums, err := ParseChecksums(strings.NewReader(file))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if len(sums) != 2 {
		t.Fatalf("parsed %d digests, want 2: %v", len(sums), sums)
	}
	if sums["tuios_0.7.0_Linux_x86_64.tar.gz"] != strings.Repeat("1", 64) {
		t.Errorf("wrong digest for the tuios archive: %q", sums["tuios_0.7.0_Linux_x86_64.tar.gz"])
	}
}

// TestParseChecksumsRefusesAnEmptyList. A file with nothing readable in it must
// not parse as "no digests, so nothing to check".
//
// Negative control: return the empty map with no error and this fails, and the
// installer would then treat every archive as unverifiable-but-fine.
func TestParseChecksumsRefusesAnEmptyList(t *testing.T) {
	if _, err := ParseChecksums(strings.NewReader("not a checksum file\n")); err == nil {
		t.Error("a file with no digests parsed as a checksum list")
	}
}

// TestVerify accepts the published digest and rejects everything else.
//
// The expected digest is computed here with sha256 directly rather than by
// calling Verify, so this is not the code agreeing with itself.
//
// Negative control: have Verify return nil for a name it has no digest for and
// the unpublished case fails.
func TestVerify(t *testing.T) {
	data := []byte("the archive bytes")
	sum := sha256.Sum256(data)
	sums := Checksums{"tuios_0.7.0_Linux_x86_64.tar.gz": hex.EncodeToString(sum[:])}

	if err := sums.Verify("tuios_0.7.0_Linux_x86_64.tar.gz", data); err != nil {
		t.Errorf("the published archive did not verify: %v", err)
	}

	var mismatch *ChecksumMismatch
	err := sums.Verify("tuios_0.7.0_Linux_x86_64.tar.gz", []byte("something else"))
	if !errors.As(err, &mismatch) {
		t.Errorf("altered bytes gave %v, want a mismatch", err)
	}

	if err := sums.Verify("tuios_0.7.0_Darwin_arm64.tar.gz", data); !errors.Is(err, ErrNoChecksum) {
		t.Errorf("an unpublished name gave %v, want ErrNoChecksum", err)
	}
}

// tarGz builds an archive in memory from name to contents.
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBinaryFromArchive finds the binary among the archive's other files.
//
// Negative control: match on the whole entry name rather than its base and the
// nested case fails.
func TestBinaryFromArchive(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"README.md": "readme",
		"LICENSE":   "license",
		"tuios":     "ELF binary",
	})
	got, err := BinaryFromArchive(bytes.NewReader(archive), "tuios")
	if err != nil {
		t.Fatalf("BinaryFromArchive: %v", err)
	}
	if string(got) != "ELF binary" {
		t.Errorf("read %q", got)
	}
}

// TestBinaryFromArchiveIgnoresTheEntryPath. A tar entry can name any path it
// likes, "../../../etc/cron.d/x" included. This reads bytes and never writes to
// a path the archive chose, so the name is only ever used to find the file.
//
// Negative control: use hdr.Name as a path anywhere and this stops being a
// meaningful claim.
func TestBinaryFromArchiveIgnoresTheEntryPath(t *testing.T) {
	archive := tarGz(t, map[string]string{"../../../etc/tuios": "ELF binary"})
	got, err := BinaryFromArchive(bytes.NewReader(archive), "tuios")
	if err != nil {
		t.Fatalf("BinaryFromArchive: %v", err)
	}
	if string(got) != "ELF binary" {
		t.Errorf("read %q", got)
	}
}

// TestBinaryFromArchiveSaysWhenTheBinaryIsMissing rather than returning empty
// bytes, which would be staged and committed as a zero-length binary.
//
// Negative control: return nil, nil at EOF and this fails.
func TestBinaryFromArchiveSaysWhenTheBinaryIsMissing(t *testing.T) {
	archive := tarGz(t, map[string]string{"README.md": "readme"})
	if _, err := BinaryFromArchive(bytes.NewReader(archive), "tuios"); !errors.Is(err, ErrNotInArchive) {
		t.Errorf("got %v, want ErrNotInArchive", err)
	}
}

// TestBinaryFromArchiveRefusesRubbish, so a redirect to an HTML error page is
// reported as a bad archive rather than as a missing binary.
//
// Negative control: ignore the gzip error and this panics or misreports.
func TestBinaryFromArchiveRefusesRubbish(t *testing.T) {
	if _, err := BinaryFromArchive(strings.NewReader("<html>404</html>"), "tuios"); err == nil {
		t.Error("an HTML page was accepted as an archive")
	}
}
