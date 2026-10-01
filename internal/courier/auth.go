package courier

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Every request to the relay but the health check is signed by the identity
// making it. The relay learns who is asking from the signature and nothing
// else: there are no accounts and no passwords, only the roster of identities
// it serves.
//
//	Authorization: TC1 id=<identity>, ts=<unix seconds>, nonce=<base64url>, sig=<base64url>
//
// The signature covers the method, the path with its query, the time, the
// nonce and a digest of the body, so none of them can be changed in flight. The
// time bounds how long a captured request stays usable, and the nonce makes it
// usable once inside that window.

const (
	authScheme     = "TC1"
	reqSigContext  = "tuios-courier-req-v1\n"
	maxRequestSkew = 120 * time.Second
	// nonceWindow is how long a nonce is remembered. It covers the whole span a
	// timestamp is accepted for, on either side of now.
	nonceWindow = 2 * maxRequestSkew
	// maxNoncesPerIdentity bounds the cache for one identity. A client that
	// makes more requests than this inside the window is refused until it
	// drains, which no honest client comes near.
	maxNoncesPerIdentity = 4096
	maxAuthHeaderBytes   = 512
)

// ErrUnauthorized is a request whose signature does not hold.
var ErrUnauthorized = errors.New("unauthorized")

func requestSigBytes(method, path, ts, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(reqSigContext + method + "\n" + path + "\n" + ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:]))
}

// SignRequest is the Authorization header for one request. path is the path
// relative to the relay's prefix, with its query.
func SignRequest(k *Keys, method, path string, body []byte, now time.Time) string {
	var n [16]byte
	_, _ = rand.Read(n[:])
	nonce := base64.RawURLEncoding.EncodeToString(n[:])
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := ed25519.Sign(k.sign, requestSigBytes(method, path, ts, nonce, body))
	return authScheme + " id=" + k.Identity().String() + ", ts=" + ts + ", nonce=" + nonce +
		", sig=" + base64.RawURLEncoding.EncodeToString(sig)
}

// Verifier checks signed requests and remembers their nonces.
type Verifier struct {
	now func() time.Time

	mu     sync.Mutex
	nonces map[Identity]map[string]time.Time
}

// NewVerifier makes a verifier reading the time from now.
func NewVerifier(now func() time.Time) *Verifier {
	return &Verifier{now: now, nonces: map[Identity]map[string]time.Time{}}
}

// Verify checks header against the request and returns who signed it. allowed
// is asked before the nonce is remembered, so an identity the relay does not
// serve cannot fill the cache.
func (v *Verifier) Verify(header, method, path string, body []byte, allowed func(Identity) bool) (Identity, error) {
	fields, ok := parseAuthHeader(header)
	if !ok {
		return Identity{}, ErrUnauthorized
	}
	id, err := ParseIdentity(fields["id"])
	if err != nil {
		return Identity{}, ErrUnauthorized
	}
	ts, err := strconv.ParseInt(fields["ts"], 10, 64)
	if err != nil {
		return Identity{}, ErrUnauthorized
	}
	nonce, err := base64.RawURLEncoding.Strict().DecodeString(fields["nonce"])
	if err != nil || len(nonce) != 16 {
		return Identity{}, ErrUnauthorized
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(fields["sig"])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return Identity{}, ErrUnauthorized
	}
	now := v.now()
	if d := now.Sub(time.Unix(ts, 0)); d > maxRequestSkew || d < -maxRequestSkew {
		return Identity{}, ErrUnauthorized
	}
	if !ed25519.Verify(id.signKey(), requestSigBytes(method, path, fields["ts"], fields["nonce"], body), sig) {
		return Identity{}, ErrUnauthorized
	}
	if !allowed(id) {
		return Identity{}, ErrUnauthorized
	}
	if !v.remember(id, fields["nonce"], now) {
		return Identity{}, ErrUnauthorized
	}
	return id, nil
}

// remember records a nonce and reports whether it was new.
func (v *Verifier) remember(id Identity, nonce string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	seen := v.nonces[id]
	if seen == nil {
		seen = map[string]time.Time{}
		v.nonces[id] = seen
	}
	if _, dup := seen[nonce]; dup {
		return false
	}
	if len(seen) >= maxNoncesPerIdentity {
		v.pruneLocked(now)
		if len(v.nonces[id]) >= maxNoncesPerIdentity {
			return false
		}
		seen = v.nonces[id]
		if seen == nil {
			seen = map[string]time.Time{}
			v.nonces[id] = seen
		}
	}
	seen[nonce] = now.Add(nonceWindow)
	return true
}

// Prune forgets nonces whose window has passed. The relay calls it from its
// sweep; Verify also prunes when an identity's cache is full.
func (v *Verifier) Prune() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pruneLocked(v.now())
}

func (v *Verifier) pruneLocked(now time.Time) {
	for id, seen := range v.nonces {
		for n, until := range seen {
			if now.After(until) {
				delete(seen, n)
			}
		}
		if len(seen) == 0 {
			delete(v.nonces, id)
		}
	}
}

func (v *Verifier) nonceCount() int {
	v.Prune()
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for _, seen := range v.nonces {
		n += len(seen)
	}
	return n
}

// parseAuthHeader reads the four fields, each exactly once, and nothing else.
func parseAuthHeader(h string) (map[string]string, bool) {
	if len(h) > maxAuthHeaderBytes {
		return nil, false
	}
	rest, ok := strings.CutPrefix(h, authScheme+" ")
	if !ok {
		return nil, false
	}
	fields := map[string]string{}
	for part := range strings.SplitSeq(rest, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || val == "" {
			return nil, false
		}
		switch k {
		case "id", "ts", "nonce", "sig":
		default:
			return nil, false
		}
		if _, dup := fields[k]; dup {
			return nil, false
		}
		fields[k] = val
	}
	return fields, len(fields) == 4
}
