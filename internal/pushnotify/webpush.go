package pushnotify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Web Push: an encrypted message to a push service the phone's operating
// system or its UnifiedPush distributor keeps a connection to, so the phone
// gets an Inbox item while it sleeps without holding a connection to tuios.
//
// The message is encrypted for the one subscription it goes to (RFC 8291,
// with the aes128gcm content coding of RFC 8188), so the push service carries
// bytes it cannot read. The daemon signs each request with its VAPID key (RFC
// 8292), so a push service can tie the subscription to this sender.
//
// Nothing here logs. An error names the push service's host, never the
// endpoint, which is a capability: anyone who has it can send to the phone.

// Subscription is one device's push subscription, as register-push takes it.
type Subscription struct {
	// Endpoint is the push service's address for this device.
	Endpoint string `json:"endpoint"`
	// P256dh is the device's public key, the uncompressed P-256 point,
	// base64url.
	P256dh string `json:"p256dh"`
	// Auth is the device's 16-byte authentication secret, base64url.
	Auth string `json:"auth"`
}

// Lengths of the subscription's keys, decoded.
const (
	p256PointLen = 65
	authLen      = 16
	saltLen      = 16
)

// MaxPayload bounds the JSON a push carries before encryption. Push services
// take 4096 bytes of body. The RFC 8188 header with a P-256 key id is 86
// bytes, and the tag and padding delimiter 17, so 3 KiB leaves room.
const MaxPayload = 3 << 10

// recordSize is the rs the header names. One record holds the whole payload.
const recordSize = 4096

// padBuckets are the sizes a payload is padded up to before it is encrypted
// (RFC 8188, section 2: zero octets after the delimiter), so the length the
// push service sees says only which bucket the payload fell in. The last is
// MaxPayload, which every payload fits.
var padBuckets = []int{256, 512, 1024, 2048, MaxPayload}

// PaddedSize is the size a payload of n bytes is padded to: the smallest
// bucket that holds it, or n when n is over MaxPayload.
func PaddedSize(n int) int {
	for _, b := range padBuckets {
		if n <= b {
			return b
		}
	}
	return n
}

// DecodeKey decodes a base64url value, with or without padding.
func DecodeKey(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// ParseSubscriptionKeys checks a subscription's keys: p256dh an uncompressed
// point on P-256, auth 16 bytes.
func ParseSubscriptionKeys(p256dh, auth string) (*ecdh.PublicKey, []byte, error) {
	pub, err := DecodeKey(p256dh)
	if err != nil || len(pub) != p256PointLen || pub[0] != 4 {
		return nil, nil, errors.New("p256dh is not a base64url uncompressed P-256 point (65 bytes, starting with 0x04)")
	}
	key, err := ecdh.P256().NewPublicKey(pub)
	if err != nil {
		return nil, nil, errors.New("p256dh is not a point on P-256")
	}
	secret, err := DecodeKey(auth)
	if err != nil || len(secret) != authLen {
		return nil, nil, errors.New("auth is not 16 bytes of base64url")
	}
	return key, secret, nil
}

// CheckEndpoint checks a push endpoint: an https address, or an http one
// when allowInsecure and its host is a loopback or private IP address (or
// localhost). It returns the endpoint's origin, the VAPID audience.
//
// The endpoint comes from whoever called register-push, which can be a
// linked machine, so it may not aim the daemon at this machine or its
// network. A loopback or private address, as an IP literal or localhost, is
// taken only when allowInsecure. A link-local, multicast or unspecified one
// never is. A name is checked when the daemon connects (dialGuard), since
// what it resolves to can change after this check.
func CheckEndpoint(raw string, allowInsecure bool) (string, error) {
	if len(raw) > 2048 {
		return "", errors.New("endpoint is longer than 2048 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("endpoint is not an absolute http or https address")
	}
	switch u.Scheme {
	case "https":
		if !hostAllowed(u.Hostname(), allowInsecure) {
			return "", errLocalEndpoint
		}
	case "http":
		if !allowInsecure {
			return "", errors.New("endpoint is http. Use https, or set [notify.webpush] allow_insecure = true for a push service on a loopback or private address")
		}
		if !localHost(u.Hostname()) {
			return "", errors.New("endpoint is http on a public address. Plain http is only for a loopback or private IP address")
		}
	default:
		return "", errors.New("endpoint is not an absolute http or https address")
	}
	return u.Scheme + "://" + u.Host, nil
}

// errLocalEndpoint refuses an endpoint on this machine or its network.
var errLocalEndpoint = errors.New("endpoint is on a loopback, private or link-local address. Set [notify.webpush] allow_insecure = true for a push service on your own network")

// localHost reports whether host is localhost or a loopback or private IP
// address. A name other than localhost is not resolved: what it resolves to
// can change after the check.
func localHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.WithZone("").Unmap()
	return ip.IsLoopback() || ip.IsPrivate()
}

// hostAllowed reports whether an endpoint's host may be used: a name other
// than localhost always (the dial checks it), and localhost or an IP literal
// by addrAllowed.
func hostAllowed(host string, allowLocal bool) bool {
	if strings.EqualFold(host, "localhost") {
		return allowLocal
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return true
	}
	return addrAllowed(ip, allowLocal)
}

// addrAllowed reports whether the daemon may connect to ip for a push: never
// to a link-local (the cloud metadata service is one), multicast or
// unspecified address, and to a loopback or private one only when
// allowLocal.
func addrAllowed(ip netip.Addr, allowLocal bool) bool {
	ip = ip.WithZone("").Unmap()
	switch {
	case !ip.IsValid(), ip.IsUnspecified(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(),
		ip.IsInterfaceLocalMulticast(), ip.IsMulticast():
		return false
	case ip.IsLoopback(), ip.IsPrivate():
		return allowLocal
	}
	return true
}

// dialPolicy is what the push client's dialer checks, carried in the
// request's context.
type dialPolicy struct{ allowLocal bool }

type dialPolicyKey struct{}

// newPushHTTPClient is the client Web Push sends with. It follows no
// redirect: a push service answers a push with 201, and a redirect would send
// the VAPID token again to an address the endpoint did not name. Its dialer
// refuses an address addrAllowed refuses, after the name is resolved, so a
// name that resolves to this machine, or changes to, gets nothing.
func newPushHTTPClient() *http.Client {
	base := &net.Dialer{Timeout: SendTimeout, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				pol, ok := ctx.Value(dialPolicyKey{}).(dialPolicy)
				if !ok {
					return base.DialContext(ctx, network, addr)
				}
				d := *base
				d.Control = func(_, address string, _ syscall.RawConn) error {
					ap, err := netip.ParseAddrPort(address)
					if err != nil || !addrAllowed(ap.Addr(), pol.allowLocal) {
						return errLocalEndpoint
					}
					return nil
				}
				return d.DialContext(ctx, network, addr)
			},
			// A connection made under one allow_insecure is not reused
			// under another.
			DisableKeepAlives:   true,
			TLSHandshakeTimeout: SendTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Encrypt encrypts plaintext for a subscription per RFC 8291, as one
// aes128gcm record (RFC 8188), padded with zero octets after the delimiter
// up to PaddedSize. The result is the request body:
//
//	salt (16) | rs (4, big endian) | idlen (1) = 65 | keyid = as_public (65) | ciphertext
//
// The key derivation, with HMAC-SHA-256 throughout:
//
//	ecdh_secret = ECDH(as_private, ua_public)
//	PRK_key     = HMAC(auth_secret, ecdh_secret)
//	IKM         = HMAC(PRK_key, "WebPush: info" 0x00 ua_public as_public 0x01)
//	PRK         = HMAC(salt, IKM)
//	CEK         = HMAC(PRK, "Content-Encoding: aes128gcm" 0x00 0x01)[0:16]
//	NONCE       = HMAC(PRK, "Content-Encoding: nonce" 0x00 0x01)[0:12]
//	ciphertext  = AES-128-GCM(CEK, NONCE, plaintext 0x02 0x00*)
//
// A receiver strips the zero octets at the end of the record, then the 0x02.
func Encrypt(uaPublic *ecdh.PublicKey, authSecret, plaintext []byte) ([]byte, error) {
	asPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(asPrivate, uaPublic, authSecret, salt, plaintext, PaddedSize(len(plaintext)))
}

// encrypt is Encrypt with the sender's key and the salt given, padding the
// plaintext to padTo bytes (no padding when padTo is not more than its
// length).
func encrypt(asPrivate *ecdh.PrivateKey, uaPublic *ecdh.PublicKey, authSecret, salt, plaintext []byte, padTo int) ([]byte, error) {
	padTo = max(padTo, len(plaintext))
	if padTo+1+16 > recordSize {
		return nil, errors.New("the payload does not fit one record")
	}
	ecdhSecret, err := asPrivate.ECDH(uaPublic)
	if err != nil {
		return nil, err
	}
	asPublic := asPrivate.PublicKey().Bytes()
	uaBytes := uaPublic.Bytes()
	keyInfo := "WebPush: info\x00" + string(uaBytes) + string(asPublic)
	// HKDF with the auth secret as salt and ecdh_secret as the input keying
	// material gives PRK_key, then 32 bytes of IKM.
	ikm, err := hkdf.Key(sha256.New, ecdhSecret, authSecret, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(recordSize))
	out.WriteByte(byte(len(asPublic)))
	out.Write(asPublic)
	record := make([]byte, padTo+1)
	copy(record, plaintext)
	record[len(plaintext)] = 0x02
	out.Write(gcm.Seal(nil, nonce, record, nil))
	return out.Bytes(), nil
}

// VAPIDKey is the daemon's VAPID signing key.
type VAPIDKey struct {
	priv *ecdsa.PrivateKey
}

// NewVAPIDKey makes a new P-256 key.
func NewVAPIDKey() (*VAPIDKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &VAPIDKey{priv: priv}, nil
}

// ParseVAPIDKey reads a key MarshalPEM wrote.
func ParseVAPIDKey(data []byte) (*VAPIDKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("the VAPID key file is not a PEM private key")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("the VAPID key file does not hold a valid key")
	}
	priv, ok := k.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, errors.New("the VAPID key is not a P-256 key")
	}
	return &VAPIDKey{priv: priv}, nil
}

// MarshalPEM is the key as a PKCS #8 PEM block.
func (k *VAPIDKey) MarshalPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// PublicKey is the public key as the uncompressed point, base64url: the
// applicationServerKey a phone subscribes with, and the k of the
// Authorization header.
func (k *VAPIDKey) PublicKey() string {
	pub, err := k.priv.PublicKey.ECDH()
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(pub.Bytes())
}

// vapidTTL is how long a VAPID token is valid. RFC 8292 allows 24 hours.
const vapidTTL = 12 * time.Hour

// Token is the VAPID JWT for audience: ES256 over
// {"typ":"JWT","alg":"ES256"} and {"aud","exp","sub"}.
func (k *VAPIDKey) Token(audience, subject string, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}{audience, now.Add(vapidTTL).Unix(), subject})
	if err != nil {
		return "", err
	}
	signing := header + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signing + "." + enc.EncodeToString(sig), nil
}

// VerifyToken checks a VAPID JWT against the public key, for tests and for a
// push service written in Go. It returns the claims.
func VerifyToken(token, publicKey string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	pub, err := DecodeKey(publicKey)
	if err != nil || len(pub) != p256PointLen {
		return nil, errors.New("bad public key")
	}
	x, y := new(big.Int).SetBytes(pub[1:33]), new(big.Int).SetBytes(pub[33:])
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, errors.New("bad signature encoding")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("the signature does not verify")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// Urgency values for the Urgency header (RFC 8030).
const (
	UrgencyHigh   = "high"
	UrgencyNormal = "normal"
)

// PushTTL is the TTL header a Push with no TTL of its own carries: a push
// service drops a message it could not deliver in this many seconds.
const PushTTL = 120

// Push is one Web Push request.
type Push struct {
	Sub     Subscription
	Payload []byte
	Urgency string
	// Topic lets a newer push for the same item replace one the push
	// service still holds: base64url, at most 32 characters.
	Topic   string
	Subject string
	// TTL is the TTL header in seconds. Zero means PushTTL.
	TTL int
}

// ErrGone is a push service's answer that the subscription no longer exists
// (404 or 410). The subscription should be dropped.
var ErrGone = errors.New("the push service says the subscription is gone")

// RetryableError is a failure worth trying again: no answer, 429 or 5xx.
type RetryableError struct {
	Err error
	// After is the wait a Retry-After header asked for, zero when none.
	After time.Duration
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// SendWebPush encrypts and sends one push. An error names the push service's
// host only. ErrGone and *RetryableError tell the caller what to do next.
func (c *Client) SendWebPush(ctx context.Context, key *VAPIDKey, p Push, allowInsecure bool) error {
	aud, err := CheckEndpoint(p.Sub.Endpoint, allowInsecure)
	if err != nil {
		return err
	}
	ua, secret, err := ParseSubscriptionKeys(p.Sub.P256dh, p.Sub.Auth)
	if err != nil {
		return err
	}
	body, err := Encrypt(ua, secret, p.Payload)
	if err != nil {
		return err
	}
	jwt, err := key.Token(aud, p.Subject, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("the endpoint is not an http or https address")
	}
	req.Header.Set("User-Agent", "tuios-notify")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	ttl := p.TTL
	if ttl <= 0 {
		ttl = PushTTL
	}
	req.Header.Set("TTL", strconv.Itoa(ttl))
	urgency := p.Urgency
	if urgency == "" {
		urgency = UrgencyNormal
	}
	req.Header.Set("Urgency", urgency)
	if p.Topic != "" {
		req.Header.Set("Topic", p.Topic)
	}
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+key.PublicKey())
	// Through a proxy, the proxy resolves the name and the dial reaches the
	// proxy, which the person configured. Without one, the dial checks the
	// address the name resolved to.
	if proxy, perr := http.ProxyFromEnvironment(req); perr == nil && proxy == nil {
		req = req.WithContext(context.WithValue(ctx, dialPolicyKey{}, dialPolicy{allowLocal: allowInsecure}))
	}
	resp, err := c.push.Do(req)
	if err != nil {
		if errors.Is(err, errLocalEndpoint) {
			return errLocalEndpoint
		}
		return &RetryableError{Err: describeTransport(ctx, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusNotFound || code == http.StatusGone:
		return ErrGone
	case code == http.StatusTooManyRequests || code >= 500:
		var after time.Duration
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			after = time.Duration(s) * time.Second
		}
		return &RetryableError{Err: describeStatus(code), After: after}
	case code >= 300 && code < 400:
		return errors.New("the push service answered with a redirect, and a push follows none. Register the phone again")
	default:
		return fmt.Errorf("the push service answered %s", statusText(code))
	}
}

// Topic makes a Topic header value from any string: the first 32 characters
// of its SHA-256, base64url.
func Topic(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}
