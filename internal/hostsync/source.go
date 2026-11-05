package hostsync

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/release"
)

// The kinds of Source: where the binary comes from.
const (
	KindRelease = "release"
	KindDev     = "dev"
	KindBinary  = "binary"
)

// Binary is one binary for one platform.
type Binary struct {
	// Data is the executable.
	Data []byte
	// SHA is the sha256 of Data, in hex.
	SHA string
	// Version is the version the binary reports.
	Version string
}

// Source makes binaries.
type Source struct {
	// Kind is KindRelease, KindDev or KindBinary.
	Kind string
	// SrcDir is the checkout a KindDev source builds.
	SrcDir string
	// BinPath is the file a KindBinary source sends, as an absolute path.
	BinPath string
	// Version is what the binary reports, or "" when it cannot be known
	// here (a --binary for another platform).
	Version string
	// Platform is the one platform a --binary is for.
	Platform string
	// Explicit says the person named the checkout (--src), so a later
	// command names it too.
	Explicit bool
	// Releases reads the release for a KindRelease source. A KindRelease
	// source needs it.
	Releases Releases

	commit string
	date   string
	tmpDir string
	rel    *release.Release
	sums   release.Checksums
	mu     sync.Mutex
	built  map[string]*Binary
	errs   map[string]error
	errOut io.Writer
}

// Releases reads a tuios release from GitHub for a Source. The tuios command
// gives it the helpers of tuios update, so a failed lookup or download reads
// the same in both commands.
type Releases interface {
	// ExplainLookupError turns a failed release lookup into the error a
	// person reads.
	ExplainLookupError(err error) error
	// FetchChecksums reads the checksum list of rel.
	FetchChecksums(ctx context.Context, src release.Source, rel release.Release) (release.Checksums, error)
	// ReadAsset downloads one asset of a release.
	ReadAsset(ctx context.Context, src release.Source, asset release.Asset) ([]byte, error)
	// ExplainChecksumError turns a failed check of a download into the
	// error a person reads.
	ExplainChecksumError(err error, rel release.Release) error
}

// NewSource makes an empty Source that reports its progress to errOut. The
// caller sets the kind with LoadBinary or InitDev, or sets Kind, Version and
// Releases for a release.
func NewSource(errOut io.Writer) *Source {
	return &Source{built: map[string]*Binary{}, errs: map[string]error{}, errOut: errOut}
}

// FindTuiosCheckout walks up from dir to the folder whose go.mod is the
// tuios module.
func FindTuiosCheckout(dir string) (string, bool) {
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil {
			line, _, _ := strings.Cut(string(data), "\n")
			if strings.TrimSpace(line) == "module github.com/"+release.Repo {
				if _, err := os.Stat(filepath.Join(dir, "cmd", "tuios")); err == nil {
					return dir, true
				}
			}
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Cleanup removes the folder the builds were written to.
func (s *Source) Cleanup() {
	if s.tmpDir != "" {
		_ = os.RemoveAll(s.tmpDir)
	}
}

// DisplayVersion is the version the hosts are synced to.
func (s *Source) DisplayVersion() string {
	if s.Version == "" {
		return "unknown"
	}
	return s.Version
}

// ErrFor is the error making the binary for one platform hit.
func (s *Source) ErrFor(platform string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[platform]; err != nil {
		return err
	}
	if s.built[platform] == nil {
		return errfmt.Errorf("no binary was made for %s", platform)
	}
	return nil
}

// BinaryFor is the binary made for platform, or nil.
func (s *Source) BinaryFor(platform string) *Binary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.built[platform]
}

// Prepare makes the binary for each platform, once each.
func (s *Source) Prepare(platforms []string) error {
	if len(platforms) == 0 {
		return nil
	}
	switch s.Kind {
	case KindRelease:
		if err := s.LookupRelease(); err != nil {
			return err
		}
	case KindDev:
		dir, err := os.MkdirTemp("", "tuios-hosts-sync-")
		if err != nil {
			return err
		}
		s.tmpDir = dir
	}
	for _, p := range platforms {
		var b *Binary
		var err error
		switch s.Kind {
		case KindRelease:
			b, err = s.FetchRelease(p)
		case KindDev:
			b, err = s.BuildDev(p)
		case KindBinary:
			if p == s.Platform {
				b = s.built[p]
			}
		}
		s.mu.Lock()
		if err != nil {
			s.errs[p] = err
		} else if b != nil {
			s.built[p] = b
		}
		s.mu.Unlock()
	}
	return nil
}
