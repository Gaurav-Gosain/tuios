package courier

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// IdentityPrefix starts every public identity. The version is in the prefix
// so that a later key type (a post-quantum hybrid KEM, say) is a new prefix a
// reader refuses by name, rather than a string that parses as the wrong key.
const IdentityPrefix = "tc1."

// identityLen is the raw identity: the Ed25519 public key that signs, then the
// X25519 public key that mail is sealed to.
const identityLen = ed25519.PublicKeySize + 32

// ErrKeyExists is returned by SaveKeys when a key is already there. A key is
// never replaced: every peer pinned the old one, and a new one is a different
// person to them.
var ErrKeyExists = errors.New("an identity key already exists")

// Identity is a person's public identity: what they hand their teammates and
// what the relay's roster lists.
type Identity struct {
	raw [identityLen]byte
}

// ParseIdentity reads the string form, tc1. and the raw key in unpadded
// base64url. Whitespace around it is dropped, since it usually arrives pasted.
func ParseIdentity(s string) (Identity, error) {
	s = strings.TrimSpace(s)
	body, ok := strings.CutPrefix(s, IdentityPrefix)
	if !ok {
		return Identity{}, fmt.Errorf("identity %q does not start with %s", clip(s), IdentityPrefix)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil {
		return Identity{}, fmt.Errorf("identity %q is not valid: %v", clip(s), err)
	}
	if len(raw) != identityLen {
		return Identity{}, fmt.Errorf("identity %q is %d bytes, want %d", clip(s), len(raw), identityLen)
	}
	var id Identity
	copy(id.raw[:], raw)
	// The X25519 half must be a point the KEM accepts, so a bad key is refused
	// here rather than at the first message sealed to it.
	if _, err := ecdh.X25519().NewPublicKey(id.raw[ed25519.PublicKeySize:]); err != nil {
		return Identity{}, fmt.Errorf("identity %q has an invalid encryption key: %v", clip(s), err)
	}
	return id, nil
}

func (i Identity) String() string {
	return IdentityPrefix + base64.RawURLEncoding.EncodeToString(i.raw[:])
}

// IsZero reports whether i was never set.
func (i Identity) IsZero() bool { return i == Identity{} }

// Equal reports whether two identities are the same key pair.
func (i Identity) Equal(o Identity) bool { return i.raw == o.raw }

// Fingerprint is a short form for people to compare out loud: the first eight
// bytes of the identity's SHA-256 in four groups.
func (i Identity) Fingerprint() string {
	sum := sha256.Sum256(i.raw[:])
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16]
}

// MailboxID is the identity's address on a relay: the first sixteen bytes of
// its SHA-256 in hex. It names a mailbox in a URL without putting a key there.
func (i Identity) MailboxID() string {
	sum := sha256.Sum256(i.raw[:])
	return hex.EncodeToString(sum[:16])
}

func (i Identity) signKey() ed25519.PublicKey {
	return ed25519.PublicKey(i.raw[:ed25519.PublicKeySize])
}

func (i Identity) kemKey() (*ecdh.PublicKey, error) {
	return ecdh.X25519().NewPublicKey(i.raw[ed25519.PublicKeySize:])
}

// Keys are a person's private keys.
type Keys struct {
	sign ed25519.PrivateKey
	kem  *ecdh.PrivateKey
}

// GenerateKeys makes a new key pair.
func GenerateKeys() (*Keys, error) {
	_, sign, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	kem, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Keys{sign: sign, kem: kem}, nil
}

// Identity is the public half.
func (k *Keys) Identity() Identity {
	var id Identity
	copy(id.raw[:], k.sign.Public().(ed25519.PublicKey))
	copy(id.raw[ed25519.PublicKeySize:], k.kem.PublicKey().Bytes())
	return id
}

// keyFile is the on-disk form of Keys.
type keyFile struct {
	V        int    `json:"v"`
	SignSeed []byte `json:"sign_seed"`
	KEMKey   []byte `json:"kem_key"`
}

// SaveKeys writes k to path, readable by its owner only, creating the
// directory. It refuses with ErrKeyExists when the file is already there.
func SaveKeys(path string, k *Keys) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(keyFile{V: 1, SignSeed: k.sign.Seed(), KEMKey: k.kem.Bytes()})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w at %s", ErrKeyExists, path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// LoadKeys reads the keys SaveKeys wrote. On unix it refuses a file other
// users can read, the same rule ssh applies to a private key.
func LoadKeys(path string) (*Keys, error) {
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("identity key %s is readable by other users (mode %v): run chmod 600 %s", path, st.Mode().Perm(), path)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kf keyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("identity key %s is not valid: %v", path, err)
	}
	if kf.V != 1 {
		return nil, fmt.Errorf("identity key %s has version %d, want 1", path, kf.V)
	}
	if len(kf.SignSeed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity key %s has a signing seed of %d bytes, want %d", path, len(kf.SignSeed), ed25519.SeedSize)
	}
	kem, err := ecdh.X25519().NewPrivateKey(kf.KEMKey)
	if err != nil {
		return nil, fmt.Errorf("identity key %s has an invalid encryption key: %v", path, err)
	}
	return &Keys{sign: ed25519.NewKeyFromSeed(kf.SignSeed), kem: kem}, nil
}

// clip shortens text another person supplied for an error message.
func clip(s string) string {
	const limit = 24
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
