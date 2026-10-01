package courier

import (
	"crypto/ed25519"
	"crypto/hpke"
	"encoding/json"
	"errors"
	"fmt"
)

// The envelope is signed, then sealed.
//
// The sender signs the message bytes together with the recipient's identity,
// wraps message, signature and both identities in the inner envelope, and seals
// that to the recipient's X25519 key with HPKE (RFC 9180, base mode,
// DHKEM(X25519, HKDF-SHA256), HKDF-SHA256, ChaCha20-Poly1305). The relay sees
// the sealed bytes only.
//
// Binding the recipient into the signature is what stops forwarding: a
// recipient who opens a message and seals the same signed envelope to someone
// else hands them a message signed as written to somebody else, which Open
// refuses. Plain sign-then-encrypt without it would let them pass it off.

const (
	hpkeInfo      = "tuios-courier v1 mail"
	msgSigContext = "tuios-courier-msg-v1\n"
)

// innerEnvelope is the plaintext HPKE seals.
type innerEnvelope struct {
	From string `json:"from"`
	To   string `json:"to"`
	Msg  []byte `json:"msg"`
	Sig  []byte `json:"sig"`
}

// signedMessageBytes is what the sender's signature covers.
func signedMessageBytes(to Identity, msg []byte) []byte {
	b := make([]byte, 0, len(msgSigContext)+len(IdentityPrefix)+90+len(msg))
	b = append(b, msgSigContext...)
	b = append(b, to.String()...)
	b = append(b, '\n')
	return append(b, msg...)
}

// Seal signs m with k and seals it to the recipient to.
func Seal(k *Keys, to Identity, m Message) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	inner := innerEnvelope{
		From: k.Identity().String(),
		To:   to.String(),
		Msg:  raw,
		Sig:  ed25519.Sign(k.sign, signedMessageBytes(to, raw)),
	}
	plain, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}
	return sealBytes(to, plain)
}

func sealBytes(to Identity, plain []byte) ([]byte, error) {
	kemKey, err := to.kemKey()
	if err != nil {
		return nil, err
	}
	pub, err := hpke.NewDHKEMPublicKey(kemKey)
	if err != nil {
		return nil, err
	}
	return hpke.Seal(pub, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(hpkeInfo), plain)
}

// PeerLookup names the configured peer an identity belongs to.
type PeerLookup func(Identity) (name string, ok bool)

// Opened is a message that passed every check.
type Opened struct {
	Msg  Message
	From Identity
	// Peer is the local name the person gave the sender. It is the only name
	// for the sender worth printing: the message cannot choose it.
	Peer string
}

// Why Open refused a box.
const (
	ReasonUndecryptable = "undecryptable"
	ReasonNotForMe      = "not_for_me"
	ReasonBadSignature  = "bad_signature"
	ReasonUnknownSender = "unknown_sender"
	ReasonInvalid       = "invalid"
	ReasonStale         = "stale"
	ReasonFromMismatch  = "from_mismatch"
)

// RejectError is a box Open refused, and why.
type RejectError struct {
	Reason string
	Detail string
}

func (e *RejectError) Error() string {
	if e.Detail == "" {
		return "message refused: " + e.Reason
	}
	return "message refused: " + e.Reason + ": " + e.Detail
}

// RejectReason is the reason of a RejectError, or empty for any other error.
func RejectReason(err error) string {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return ""
}

func reject(reason, format string, args ...any) error {
	return &RejectError{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Open opens a box sealed to k and checks it. The checks run in an order
// where nothing a check relies on is trusted before it has been checked: the
// recipient first, then the signature against the key inside the sender's own
// identity, then whether the person knows that sender, then the message.
func Open(k *Keys, box []byte, known PeerLookup) (Opened, error) {
	priv, err := hpke.NewDHKEMPrivateKey(k.kem)
	if err != nil {
		return Opened{}, err
	}
	plain, err := hpke.Open(priv, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(hpkeInfo), box)
	if err != nil {
		return Opened{}, reject(ReasonUndecryptable, "%v", err)
	}
	var inner innerEnvelope
	if err := json.Unmarshal(plain, &inner); err != nil {
		return Opened{}, reject(ReasonInvalid, "inner envelope: %v", err)
	}
	me := k.Identity()
	to, err := ParseIdentity(inner.To)
	if err != nil || !to.Equal(me) {
		return Opened{}, reject(ReasonNotForMe, "addressed to someone else")
	}
	from, err := ParseIdentity(inner.From)
	if err != nil {
		return Opened{}, reject(ReasonInvalid, "sender: %v", err)
	}
	if !ed25519.Verify(from.signKey(), signedMessageBytes(me, inner.Msg), inner.Sig) {
		return Opened{}, reject(ReasonBadSignature, "the signature does not match the sender %s", from.Fingerprint())
	}
	peer, ok := known(from)
	if !ok {
		return Opened{From: from}, reject(ReasonUnknownSender, "sender %s is not in your peers", from.Fingerprint())
	}
	var m Message
	if err := json.Unmarshal(inner.Msg, &m); err != nil {
		return Opened{}, reject(ReasonInvalid, "message: %v", err)
	}
	if err := m.Validate(); err != nil {
		return Opened{}, reject(ReasonInvalid, "%v", err)
	}
	return Opened{Msg: m, From: from, Peer: peer}, nil
}
