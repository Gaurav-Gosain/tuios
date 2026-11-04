// Package update updates the server build of Kero (cmd/kero-server) in
// place: it downloads the archive of a version from Kero's releases,
// checks it against the key the app's own updates are signed with, and
// puts it where the running one is, which a restart of kerod then runs.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// PublicKey checks Kero's releases: mygo.json's updates.publicKey, whose
// private key signs the app's updates and the server build's archives.
const PublicKey = "WzJsOGNIuf6mcEqo5ff8jub+NoQQOEk4JXreLPYjgyQ="

// Repo is the GitHub repository of Kero's releases.
const Repo = "egoist/kero"

// maxArchive bounds an archive's download.
const maxArchive = 256 << 20

// Asset is the name of the server build's archive of a version for a
// platform: a .zip for Windows, else a .tar.gz. Its signature is
// Asset+".sig", its SHA-256 Asset+".sha256".
func Asset(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("kero-server-%s-%s-%s%s", version, goos, goarch, ext)
}

// Platforms are those the server build is made for.
var Platforms = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"}

// exe is the name of kero in an archive for goos.
func exe(goos string) string {
	if goos == "windows" {
		return "kero.exe"
	}
	return "kero"
}

// releases is where the releases are: $KERO_RELEASES, for tests and
// mirrors, else GitHub's.
func releases() string {
	if r := os.Getenv("KERO_RELEASES"); r != "" {
		return strings.TrimSuffix(r, "/")
	}
	return "https://github.com/" + Repo + "/releases/download"
}

// URL is where a file of a version's release is.
func URL(version, name string) string {
	return releases() + "/v" + strings.TrimPrefix(version, "v") + "/" + name
}

// InstallScript is the URL of the script that installs a version ("" the
// latest) of the server build: `curl -fsSL <it> | sh`.
func InstallScript(version string) string { return script(version, "install-server.sh") }

// InstallScriptPS is the URL of the script that installs a version of the
// server build on Windows: `irm <it> | iex`, in PowerShell.
func InstallScriptPS(version string) string { return script(version, "install-server.ps1") }

func script(version, name string) string {
	if version == "" {
		return "https://github.com/" + Repo + "/releases/latest/download/" + name
	}
	return URL(version, name)
}

var httpClient = &http.Client{Timeout: 5 * time.Minute}

func get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s: too large", url)
	}
	return b, nil
}

// Latest is the version of Kero's latest release.
func Latest(ctx context.Context) (string, error) {
	b, err := get(ctx, "https://api.github.com/repos/"+Repo+"/releases/latest", 1<<20)
	if err != nil {
		return "", err
	}
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	if r.Tag == "" {
		return "", errors.New("no release of Kero is published")
	}
	return strings.TrimPrefix(r.Tag, "v"), nil
}

// Marker is the file of an install of the server build that updates
// itself: install-server.sh's, in ~/.local/lib/kero, which the archives
// carry. A directory without it is never replaced, as one the user put
// kero in by hand may hold other files.
const Marker = ".kero-server"

// Managed reports whether dir is an install of the server build that
// updates itself.
func Managed(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, Marker))
	return err == nil
}

// InstallDir is the directory of the running kero, links resolved: the
// install a self-update replaces.
func InstallDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// ValidVersion reports whether v is a version of Kero, as 2.1.0 or
// 2.1.0-beta.1.
func ValidVersion(v string) bool { return versionRe.MatchString(v) }

// Compare compares two versions as semantic versions: -1, 0 or 1. A
// prerelease comes before its release.
func Compare(a, b string) int {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	av, apre, _ := strings.Cut(a, "-")
	bv, bpre, _ := strings.Cut(b, "-")
	an, bn := strings.Split(av, "."), strings.Split(bv, ".")
	for i := range max(len(an), len(bn)) {
		var x, y int
		if i < len(an) {
			x, _ = strconv.Atoi(an[i])
		}
		if i < len(bn) {
			y, _ = strconv.Atoi(bn[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	return strings.Compare(apre, bpre)
}

// message is what an archive's signature signs: the archive, as the server
// build of a version for a platform, so that a signature passes for no
// other version, platform or file signed with the key.
func message(archive []byte, version, goos, goarch string) []byte {
	sum := sha256.Sum256(archive)
	return []byte("kero-server " + version + " " + goos + "/" + goarch + " " + hex.EncodeToString(sum[:]))
}

// Verify checks an archive of a version for a platform against its
// signature: Ed25519, base64, with the key of Kero's releases.
func Verify(archive []byte, version, goos, goarch, sig string) error {
	key, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("update: bad public key")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), message(archive, version, goos, goarch), s) {
		return errors.New("update: the archive's signature is not Kero's for this version")
	}
	return nil
}

// Sign signs an archive as Verify checks it, with a private key as mygo
// keeps it (MYGO_UPDATER_PRIVATE_KEY): base64 of the Ed25519 key.
func Sign(archive []byte, version, goos, goarch, privateKey string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil || len(k) != ed25519.PrivateKeySize {
		return "", errors.New("update: bad private key")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(k), message(archive, version, goos, goarch))), nil
}

// Install puts the server build of version ("" the latest), for this
// platform, in dir, in place of what it holds: the install of the running
// kero (InstallDir), which keeps running the old one until it restarts.
// It goes back from current, the version installed, only with downgrade.
func Install(ctx context.Context, version, dir, current string, downgrade bool) error {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if !slices.Contains(Platforms, platform) {
		return fmt.Errorf("the server build of Kero is not made for %s", platform)
	}
	if !Managed(dir) {
		return fmt.Errorf("Kero in %s was not installed with Kero's install script: update it as you installed it", dir)
	}
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		v, err := Latest(ctx)
		if err != nil {
			return err
		}
		version = v
	}
	if !ValidVersion(version) {
		return fmt.Errorf("%q is not a version of Kero", version)
	}
	if current != "" && ValidVersion(current) {
		switch c := Compare(version, current); {
		case c == 0:
			return nil
		case c < 0 && !downgrade:
			return fmt.Errorf("Kero %s is installed, newer than %s: update the app instead", current, version)
		}
	}
	name := Asset(version, runtime.GOOS, runtime.GOARCH)
	archive, err := get(ctx, URL(version, name), maxArchive)
	if err != nil {
		return fmt.Errorf("downloading Kero %s: %w", version, err)
	}
	sig, err := get(ctx, URL(version, name+".sig"), 4096)
	if err != nil {
		return fmt.Errorf("downloading the signature of Kero %s: %w", version, err)
	}
	if err := Verify(archive, version, runtime.GOOS, runtime.GOARCH, string(sig)); err != nil {
		return err
	}
	return Replace(archive, dir)
}

// Replace puts what the archive holds in place of dir, as a whole, so
// that no file of the old version stays: into a directory beside it
// first, then by renames. The archive is the server build's, for this
// platform: it holds kero and the Marker.
func Replace(archive []byte, dir string) error {
	dir = filepath.Clean(dir)
	staging := dir + ".new"
	old := dir + ".old"
	os.RemoveAll(staging)
	os.RemoveAll(old)
	if err := extract(archive, staging); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if st, err := os.Stat(filepath.Join(staging, exe(runtime.GOOS))); err != nil || runtime.GOOS != "windows" && st.Mode()&0o111 == 0 || !Managed(staging) {
		os.RemoveAll(staging)
		return errors.New("update: the archive is not the server build of Kero")
	}
	if runtime.GOOS == "windows" {
		return replaceFiles(staging, dir)
	}
	if err := os.Rename(dir, old); err != nil {
		os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, dir); err != nil {
		os.Rename(old, dir)
		return err
	}
	os.RemoveAll(old)
	return nil
}

// oldSuffix ends the files a Windows update moved aside, as
// kero.exe.<stamp>.kero-old: its own, which no file of the user's ends with.
const oldSuffix = ".kero-old"

// replaceFiles puts staging's files in place of dir's, one by one, as
// Windows takes it: the files kerod runs, kero.exe and its library, and
// the directory that holds them, can be neither removed nor replaced, but
// those files can be moved. So dir's go aside first (oldSuffix),
// removed once nothing runs them (RemoveOld).
func replaceFiles(staging, dir string) error {
	defer os.RemoveAll(staging)
	RemoveOld(dir)
	olds, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	news, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	type move struct{ from, to string }
	var done []move
	undo := func(err error) error {
		for i := len(done) - 1; i >= 0; i-- {
			os.Rename(done[i].to, done[i].from)
		}
		return err
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	for _, e := range olds {
		if strings.HasSuffix(e.Name(), oldSuffix) {
			continue
		}
		m := move{filepath.Join(dir, e.Name()), filepath.Join(dir, e.Name()+"."+stamp+oldSuffix)}
		if err := os.Rename(m.from, m.to); err != nil {
			return undo(err)
		}
		done = append(done, m)
	}
	for _, e := range news {
		m := move{filepath.Join(staging, e.Name()), filepath.Join(dir, e.Name())}
		if err := os.Rename(m.from, m.to); err != nil {
			return undo(err)
		}
		done = append(done, m)
	}
	RemoveOld(dir)
	return nil
}

// RemoveOld removes what an update on Windows moved aside in dir, an
// install that updates itself (Managed), but what still runs, which the
// next one removes.
func RemoveOld(dir string) {
	if !Managed(dir) {
		return
	}
	olds, _ := filepath.Glob(filepath.Join(dir, "*"+oldSuffix))
	for _, o := range olds {
		os.RemoveAll(o)
	}
}

// extract unpacks a .zip or a .tar.gz into dir: its regular files and
// directories, nothing outside dir.
func extract(archive []byte, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if bytes.HasPrefix(archive, []byte("PK\x03\x04")) {
		return extractZip(archive, dir)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = place(dir, h.Name, true, 0, nil)
		case tar.TypeReg:
			err = place(dir, h.Name, false, os.FileMode(h.Mode), tr)
		}
		if err != nil {
			return err
		}
	}
}

func extractZip(archive []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			if err := place(dir, f.Name, true, 0, nil); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return fmt.Errorf("update: %w", err)
		}
		mode := f.Mode()
		if mode&0o777 == 0 {
			mode = 0o644
		}
		err = place(dir, f.Name, false, mode, r)
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// place writes a file, or a directory, of an archive into dir.
func place(dir, name string, isDir bool, mode os.FileMode, r io.Reader) error {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." {
		return nil
	}
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("update: %s is outside the archive", name)
	}
	path := filepath.Join(dir, clean)
	if isDir {
		return os.MkdirAll(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode&0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, maxArchive))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
