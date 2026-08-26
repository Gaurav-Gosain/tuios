package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// ChecksumFile is the name goreleaser gives the digest list attached to every
// release.
const ChecksumFile = "checksums.txt"

// maxBinarySize bounds what will be read out of an archive. A tuios binary is
// around forty megabytes with the ghostty backend; anything an order of
// magnitude past that is not the file this is looking for, and decompressing it
// into memory to find that out is how a bad download becomes an OOM.
const maxBinarySize = 512 << 20

// ErrNotInArchive is returned when the archive did not contain the binary it
// was supposed to.
var ErrNotInArchive = errors.New("the archive does not contain the binary")

// AssetName is the release archive holding one binary for one platform.
//
// It mirrors the name_template blocks in .goreleaser.yml, which are the only
// definition of these names that exists. Two of their rules are easy to get
// wrong and are the reason this is a function rather than a format string: the
// version drops its leading v, and amd64 and 386 are renamed to x86_64 and
// i386 while every other architecture keeps its Go name.
//
// version is the tag, with or without the v. binary is "tuios" or "tuios-web".
func AssetName(binary, version, goos, goarch string) (string, error) {
	arch, err := archName(goarch)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", binary, strings.TrimPrefix(version, "v"), osName(goos), arch), nil
}

// osName is goreleaser's `title .Os`: the Go name with its first letter raised.
func osName(goos string) string {
	if goos == "" {
		return ""
	}
	return strings.ToUpper(goos[:1]) + goos[1:]
}

// archName is goreleaser's architecture rewrite.
//
// arm is refused rather than guessed. The archive name carries the ARM version
// (armv6, armv7) and a running binary cannot read the GOARM it was built with,
// so any answer here would be a coin flip between two real asset names. Saying
// so and pointing at the release page is the honest outcome.
func archName(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x86_64", nil
	case "386":
		return "i386", nil
	case "arm":
		return "", errors.New("arm builds come in armv6 and armv7 archives and a running binary cannot tell which it is")
	case "":
		return "", errors.New("no architecture given")
	default:
		return goarch, nil
	}
}

// Checksums maps a file name to its hex sha256, parsed from a goreleaser
// checksums.txt: one "<hex>  <name>" per line.
type Checksums map[string]string

// ParseChecksums reads a checksums.txt.
//
// A line it cannot read is skipped rather than failing the parse. The file is
// generated, so a line this does not understand is far more likely to be a
// format that grew a column than a corrupt download, and Verify still refuses
// anything it has no digest for.
func ParseChecksums(r io.Reader) (Checksums, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", ChecksumFile, err)
	}
	out := Checksums{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != sha256.Size*2 {
			continue
		}
		// A name may be written "*name" for a binary-mode digest.
		out[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s held no digests", ChecksumFile)
	}
	return out, nil
}

// ChecksumMismatch is a downloaded file whose digest is not the published one.
type ChecksumMismatch struct {
	Name string
	Want string
	Got  string
}

func (e *ChecksumMismatch) Error() string {
	return fmt.Sprintf("%s does not match its published checksum (wanted %s, got %s)", e.Name, e.Want, e.Got)
}

// ErrNoChecksum is a file the checksum list does not mention. It is an error
// rather than a pass: an archive with no published digest is one nothing has
// vouched for, and installing it anyway would make the whole check decorative.
var ErrNoChecksum = errors.New("the release publishes no checksum for this file")

// Verify checks data against the published digest for name.
func (c Checksums) Verify(name string, data []byte) error {
	want, ok := c[name]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoChecksum, name)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return &ChecksumMismatch{Name: name, Want: want, Got: got}
	}
	return nil
}

// BinaryFromArchive pulls one named binary out of a .tar.gz.
//
// Only a regular file whose base name matches is accepted, and the entry's own
// directory is ignored rather than honoured. A tar entry can name any path it
// likes, including ../../etc/something, and this never writes to a path the
// archive chose: it returns bytes, and the caller decides where they go.
func BinaryFromArchive(r io.Reader, binary string) ([]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read the archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: %s", ErrNotInArchive, binary)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != binary {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxBinarySize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read %s from the archive: %w", binary, err)
		}
		if int64(len(data)) > maxBinarySize {
			return nil, fmt.Errorf("%s in the archive is larger than %d bytes", binary, int64(maxBinarySize))
		}
		return data, nil
	}
}
