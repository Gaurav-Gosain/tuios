// Sign signs the server build's archives for a release, as the release
// workflow runs it: for each file, file.sig, which update.Verify checks,
// and file.sha256, which the install scripts check. The private key is
// $MYGO_UPDATER_PRIVATE_KEY, the one the app's updates are signed with.
//
//	go run ./internal/update/sign build/server/*.tar.gz build/server/*.zip
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/guios/update"
)

func main() {
	key := os.Getenv("MYGO_UPDATER_PRIVATE_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "sign: MYGO_UPDATER_PRIVATE_KEY is not set")
		os.Exit(1)
	}
	for _, path := range os.Args[1:] {
		b, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		version, goos, goarch, err := parseName(filepath.Base(path))
		if err != nil {
			fail(err)
		}
		sig, err := update.Sign(b, version, goos, goarch, key)
		if err != nil {
			fail(err)
		}
		if err := update.Verify(b, version, goos, goarch, sig); err != nil {
			fail(fmt.Errorf("%s: the key is not the one Kero checks (update.PublicKey): %w", path, err))
		}
		sum := sha256.Sum256(b)
		if err := os.WriteFile(path+".sig", []byte(sig+"\n"), 0o644); err != nil {
			fail(err)
		}
		line := hex.EncodeToString(sum[:]) + "  " + filepath.Base(path) + "\n"
		if err := os.WriteFile(path+".sha256", []byte(line), 0o644); err != nil {
			fail(err)
		}
		fmt.Println("signed", path)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sign:", err)
	os.Exit(1)
}

// parseName reads the version and platform of an archive from its name,
// as update.Asset writes it: kero-server-2.1.0-linux-amd64.tar.gz, or
// kero-server-2.1.0-windows-amd64.zip.
func parseName(name string) (version, goos, goarch string, err error) {
	rest, ok := strings.CutPrefix(name, "kero-server-")
	rest, ok2 := strings.CutSuffix(rest, ".tar.gz")
	if !ok2 {
		rest, ok2 = strings.CutSuffix(rest, ".zip")
	}
	parts := strings.Split(rest, "-")
	if !ok || !ok2 || len(parts) < 3 {
		return "", "", "", fmt.Errorf("%s is not named as update.Asset names archives", name)
	}
	n := len(parts)
	version, goos, goarch = strings.Join(parts[:n-2], "-"), parts[n-2], parts[n-1]
	if update.Asset(version, goos, goarch) != name || !update.ValidVersion(version) {
		return "", "", "", fmt.Errorf("%s is not named as update.Asset names archives", name)
	}
	return version, goos, goarch, nil
}
