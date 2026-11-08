// Command minisign checks a minisign signature, so that
// scripts/ghostty-lib.sh can verify the libghostty-vt release tarballs on a
// machine with Go and no minisign.
//
// Usage: go run ./scripts/minisign PUBLIC_KEY FILE [SIGNATURE]
//
// PUBLIC_KEY is the base64 key line minisign prints. SIGNATURE defaults to
// FILE.minisig. It checks the signature of the file and the global signature
// that covers the trusted comment, and exits 1 when either fails. It accepts
// the prehashed algorithm (ED) that minisign signs with by default, and the
// legacy one (Ed).
package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/blake2b"
)

func main() {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/minisign PUBLIC_KEY FILE [SIGNATURE]")
		os.Exit(2)
	}
	sigPath := os.Args[2] + ".minisig"
	if len(os.Args) == 4 {
		sigPath = os.Args[3]
	}
	comment, err := verify(os.Args[1], os.Args[2], sigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "minisign: %s: %v\n", os.Args[2], err)
		os.Exit(1)
	}
	fmt.Printf("%s: signature good (%s)\n", os.Args[2], comment)
}

func verify(keyText, filePath, sigPath string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyText))
	if err != nil || len(key) != 42 || string(key[:2]) != "Ed" {
		return "", fmt.Errorf("the public key is not a minisign Ed25519 key")
	}
	keyID, pub := key[2:10], ed25519.PublicKey(key[10:])

	raw, err := os.ReadFile(sigPath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	if len(lines) < 4 {
		return "", fmt.Errorf("%s is not a minisign signature file", sigPath)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(sig) != 74 {
		return "", fmt.Errorf("%s has a bad signature line", sigPath)
	}
	trusted, ok := strings.CutPrefix(strings.TrimRight(lines[2], "\r"), "trusted comment: ")
	if !ok {
		return "", fmt.Errorf("%s has no trusted comment", sigPath)
	}
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != ed25519.SignatureSize {
		return "", fmt.Errorf("%s has a bad global signature", sigPath)
	}
	if !bytes.Equal(sig[2:10], keyID) {
		return "", fmt.Errorf("signed with key id %X, not the key given (%X)", sig[2:10], keyID)
	}

	msg, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	switch string(sig[:2]) {
	case "ED":
		sum := blake2b.Sum512(msg)
		msg = sum[:]
	case "Ed":
	default:
		return "", fmt.Errorf("unknown signature algorithm %q", sig[:2])
	}
	if !ed25519.Verify(pub, msg, sig[10:]) {
		return "", fmt.Errorf("the signature does not match the file")
	}
	if !ed25519.Verify(pub, append(append([]byte{}, sig[10:]...), trusted...), global) {
		return "", fmt.Errorf("the trusted comment signature does not match")
	}
	return trusted, nil
}
