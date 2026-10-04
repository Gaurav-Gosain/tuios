package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
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

	"github.com/Gaurav-Gosain/tuios/internal/release"
)

// Where `tuios hosts sync` gets its binaries: the release that matches this
// build, a build of a checkout, or a file the person names.

// syncBuildTimeout bounds one cross-compile.
const syncBuildTimeout = 10 * time.Minute

func newSyncBinary(data []byte, ver string) *syncBinary {
	sum := sha256.Sum256(data)
	return &syncBinary{data: data, sha: hex.EncodeToString(sum[:]), version: ver}
}

// loadBinary reads a --binary file and the platform it is for.
func (s *syncSource) loadBinary(p string) error {
	s.kind = sourceBinary
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	s.binPath = abs
	info, err := buildinfo.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("%s is not a Go binary that sync can read: %w", p, err)
	}
	var goos, goarch string
	for _, st := range info.Settings {
		switch st.Key {
		case "GOOS":
			goos = st.Value
		case "GOARCH":
			goarch = st.Value
		}
	}
	if goos == "" || goarch == "" {
		return fmt.Errorf("%s does not say which system it is for", p)
	}
	if !strings.HasSuffix(info.Path, "/cmd/tuios") {
		return fmt.Errorf("%s is %s, not tuios", p, info.Path)
	}
	s.platform = goos + "/" + goarch
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	// The version is read by running it, which only works for this
	// machine's own system.
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, abs, "--version").Output()
		if err != nil {
			return fmt.Errorf("%s did not run: %w", p, err)
		}
		line, _, _ := strings.Cut(string(out), "\n")
		s.version, _ = parseVersionLine(line)
	}
	s.built[s.platform] = newSyncBinary(data, s.version)
	return nil
}

// initDev reads the version a build of the checkout in dir reports:
// dev+COMMIT, and for a tree with changes dev+COMMIT-dirty.HASH, where HASH
// is of the changes. Two different sets of changes give two versions, so a
// host with the last build is not taken to be up to date.
func (s *syncSource) initDev(dir string) error {
	s.kind = sourceDev
	s.srcDir = dir
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.Output()
		return string(out), err
	}
	rev, err := git("rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("could not read the commit of %s: %w", dir, err)
	}
	rev = strings.TrimSpace(rev)
	s.commit = rev
	s.version = "dev+" + rev[:min(12, len(rev))]
	if when, err := git("log", "-1", "--format=%cI"); err == nil {
		s.date = strings.TrimSpace(when)
	}
	status, err := git("status", "--porcelain")
	if err != nil {
		return fmt.Errorf("could not read the state of %s: %w", dir, err)
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	h := sha256.New()
	diff, err := git("diff", "HEAD", "--binary")
	if err != nil {
		return fmt.Errorf("could not read the changes in %s: %w", dir, err)
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
	s.version += "-dirty." + hex.EncodeToString(h.Sum(nil))[:8]
	return nil
}

// buildDev cross-compiles the checkout for one platform with the release
// flags.
func (s *syncSource) buildDev(platform string) (*syncBinary, error) {
	goos, goarch, _ := strings.Cut(platform, "/")
	out := filepath.Join(s.tmpDir, "tuios-"+goos+"-"+goarch)
	fmt.Fprintf(s.errOut, "Building tuios %s for %s...\n", s.version, platform)
	ldflags := "-s -w -X main.version=" + s.version + " -X main.commit=" + s.commit + " -X main.builtBy=hosts-sync"
	if s.date != "" {
		ldflags += " -X main.date=" + s.date
	}
	ctx, cancel := context.WithTimeout(context.Background(), syncBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/tuios")
	cmd.Dir = s.srcDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("the build for %s failed: %v: %s", platform, err, lastLine(stderr.String()))
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	return newSyncBinary(data, s.version), nil
}

// lookupRelease reads the release of this version and its checksums.
func (s *syncSource) lookupRelease() error {
	if s.rel != nil {
		return nil
	}
	tag := "v" + strings.TrimPrefix(s.version, "v")
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	gh := release.NewGitHub()
	rel, err := gh.Tag(ctx, tag)
	if err != nil {
		if errors.Is(err, release.ErrNoRelease) {
			return &diagnosticError{
				What:  fmt.Sprintf("GitHub has no release %s.", tag),
				Cause: "sync installs the release that matches this build.",
				Fix:   "run the command in a tuios checkout with --dev, or pass --binary PATH",
			}
		}
		return explainLookupError(err, false)
	}
	sums, err := fetchChecksums(ctx, gh, rel)
	if err != nil {
		return err
	}
	s.rel, s.sums = &rel, sums
	return nil
}

// fetchRelease downloads and checks the archive for one platform.
func (s *syncSource) fetchRelease(platform string) (*syncBinary, error) {
	goos, goarch, _ := strings.Cut(platform, "/")
	name, err := release.AssetName("tuios", s.rel.Tag, goos, goarch)
	if err != nil {
		return nil, fmt.Errorf("no release archive fits %s: %w", platform, err)
	}
	asset, ok := s.rel.AssetNamed(name)
	if !ok {
		return nil, fmt.Errorf("%s publishes no %s", s.rel.Tag, name)
	}
	fmt.Fprintf(s.errOut, "Downloading %s...\n", name)
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	archive, err := readAsset(ctx, release.NewGitHub(), asset)
	if err != nil {
		return nil, err
	}
	if err := s.sums.Verify(name, archive); err != nil {
		return nil, explainChecksumError(err, *s.rel)
	}
	data, err := release.BinaryFromArchive(bytes.NewReader(archive), "tuios")
	if err != nil {
		return nil, fmt.Errorf("%s did not hold tuios: %w", name, err)
	}
	return newSyncBinary(data, s.version), nil
}
