package hostsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/release"
)

// Where `tuios hosts sync` gets its binaries: the release that matches this
// build, a build of a checkout, or a file the person names.

// syncBuildTimeout bounds one cross-compile.
const syncBuildTimeout = 10 * time.Minute

// networkTimeout bounds one release lookup or download. It is the bound of
// tuios update (networkTimeout in cmd/tuios/update.go).
const networkTimeout = 3 * time.Minute

// NewBinary is data as a Binary that reports the version ver.
func NewBinary(data []byte, ver string) *Binary {
	sum := sha256.Sum256(data)
	return &Binary{Data: data, SHA: hex.EncodeToString(sum[:]), Version: ver}
}

// LoadBinary reads a --binary file and the platform it is for.
func (s *Source) LoadBinary(p string) error {
	s.Kind = KindBinary
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	s.BinPath = abs
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	goos, goarch, err := BinaryPlatform(data)
	if err != nil {
		return errfmt.Errorf("%s: %w", p, err)
	}
	// The module path is in every Go binary built from the tuios main
	// package, stripped or not.
	if !bytes.Contains(data, []byte("github.com/"+release.Repo+"/cmd/tuios")) {
		return errfmt.Errorf("%s is not a tuios binary", p)
	}
	s.Platform = goos + "/" + goarch
	// The version is read by running it, which only works for this
	// machine's own system.
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, abs, "--version").Output()
		if err != nil {
			return errfmt.Errorf("%s did not run: %w", p, err)
		}
		line, _, _ := strings.Cut(string(out), "\n")
		s.Version, _ = ParseVersionLine(line)
	}
	s.built[s.Platform] = NewBinary(data, s.Version)
	return nil
}

// InitDev reads the version a build of the checkout in dir reports:
// dev+COMMIT, and for a tree with changes dev+COMMIT-dirty.HASH, where HASH
// is of the changes. Two different sets of changes give two versions, so a
// host with the last build is not taken to be up to date.
func (s *Source) InitDev(dir string) error {
	s.Kind = KindDev
	s.SrcDir = dir
	git := func(args ...string) (string, error) {
		cmd := childproc.Git(context.Background(), dir, args...)
		out, err := cmd.Output()
		return string(out), err
	}
	rev, err := git("rev-parse", "HEAD")
	if err != nil {
		return errfmt.Errorf("could not read the commit of %s: %w", dir, err)
	}
	rev = strings.TrimSpace(rev)
	s.commit = rev
	s.Version = "dev+" + rev[:min(12, len(rev))]
	if when, err := git("log", "-1", "--format=%cI"); err == nil {
		s.date = strings.TrimSpace(when)
	}
	status, err := git("status", "--porcelain")
	if err != nil {
		return errfmt.Errorf("could not read the state of %s: %w", dir, err)
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	h := sha256.New()
	diff, err := git("diff", "HEAD", "--binary")
	if err != nil {
		return errfmt.Errorf("could not read the changes in %s: %w", dir, err)
	}
	_, _ = io.WriteString(h, diff)
	untracked, _ := git("ls-files", "--others", "--exclude-standard", "-z")
	for name := range strings.SplitSeq(untracked, "\x00") {
		if name == "" {
			continue
		}
		_, _ = io.WriteString(h, "\x00"+name+"\x00")
		if f, err := os.Open(filepath.Join(dir, name)); err == nil {
			_, _ = io.Copy(h, io.LimitReader(f, 64<<20))
			_ = f.Close()
		}
	}
	s.commit += "-dirty"
	s.Version += "-dirty." + hex.EncodeToString(h.Sum(nil))[:8]
	return nil
}

// BuildDev cross-compiles the checkout for one platform with the release
// flags.
func (s *Source) BuildDev(platform string) (*Binary, error) {
	goos, goarch, _ := strings.Cut(platform, "/")
	out := filepath.Join(s.tmpDir, "tuios-"+goos+"-"+goarch)
	fmt.Fprintf(s.errOut, "Building tuios %s for %s...\n", s.Version, platform)
	ldflags := "-s -w -X main.version=" + s.Version + " -X main.commit=" + s.commit + " -X main.builtBy=hosts-sync"
	if s.date != "" {
		ldflags += " -X main.date=" + s.date
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/tuios")
	cmd.Dir = s.SrcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, errfmt.Errorf("the build for %s failed: %v: %s", platform, err, LastLine(stderr.String()))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	return NewBinary(data, s.Version), nil
}

// LookupRelease reads the release of this version and its checksums.
func (s *Source) LookupRelease() error {
	if s.rel != nil {
		return nil
	}
	tag := "v" + strings.TrimPrefix(s.Version, "v")
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	gh := release.NewGitHub()
	rel, err := gh.Tag(ctx, tag)
	if err != nil {
		if errors.Is(err, release.ErrNoRelease) {
			return &Problem{
				What:  fmt.Sprintf("GitHub has no release %s.", tag),
				Cause: "sync installs the release that matches this build.",
				Fix:   "run the command in a tuios checkout with --dev, or pass --binary PATH",
			}
		}
		return s.Releases.ExplainLookupError(err)
	}
	sums, err := s.Releases.FetchChecksums(ctx, gh, rel)
	if err != nil {
		return err
	}
	s.rel, s.sums = &rel, sums
	return nil
}

// FetchRelease downloads and checks the archive for one platform.
func (s *Source) FetchRelease(platform string) (*Binary, error) {
	goos, goarch, _ := strings.Cut(platform, "/")
	name, err := release.AssetName("tuios", s.rel.Tag, goos, goarch)
	if err != nil {
		return nil, errfmt.Errorf("no release archive fits %s: %w", platform, err)
	}
	asset, ok := s.rel.AssetNamed(name)
	if !ok {
		return nil, errfmt.Errorf("%s publishes no %s", s.rel.Tag, name)
	}
	fmt.Fprintf(s.errOut, "Downloading %s...\n", name)
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	archive, err := s.Releases.ReadAsset(ctx, release.NewGitHub(), asset)
	if err != nil {
		return nil, err
	}
	if err := s.sums.Verify(name, archive); err != nil {
		return nil, s.Releases.ExplainChecksumError(err, *s.rel)
	}
	data, err := release.BinaryFromArchive(bytes.NewReader(archive), "tuios")
	if err != nil {
		return nil, errfmt.Errorf("%s did not hold tuios: %w", name, err)
	}
	return NewBinary(data, s.Version), nil
}

// BinaryPlatform reads the system and architecture an executable is for from
// its header. It knows the ELF and Mach-O builds that sync can install, and
// is kept to a few header fields: debug/elf and debug/macho would add their
// whole parsers to the binary for two numbers.
func BinaryPlatform(data []byte) (goos, goarch string, err error) {
	switch {
	case len(data) >= 20 && bytes.HasPrefix(data, []byte("\x7fELF")):
		if data[5] != 1 {
			return "", "", errors.New("the binary is a big-endian ELF file, which sync does not install")
		}
		goos = "linux"
		if data[7] == 9 { // ELFOSABI_FREEBSD
			goos = "freebsd"
		}
		switch binary.LittleEndian.Uint16(data[18:20]) {
		case 0x3e:
			goarch = "amd64"
		case 0xb7:
			goarch = "arm64"
		case 0x03:
			goarch = "386"
		case 0xf3:
			goarch = "riscv64"
		}
	case len(data) >= 8 && binary.LittleEndian.Uint32(data[0:4]) == 0xfeedfacf:
		goos = "darwin"
		switch binary.LittleEndian.Uint32(data[4:8]) {
		case 0x01000007:
			goarch = "amd64"
		case 0x0100000c:
			goarch = "arm64"
		}
	default:
		return "", "", errors.New("the file is not a Linux, macOS or FreeBSD executable")
	}
	if goarch == "" {
		return "", "", errors.New("the binary is for an architecture sync does not install")
	}
	return goos, goarch, nil
}
