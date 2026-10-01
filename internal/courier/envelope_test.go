package courier

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hpke"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func testMessage(body string) Message {
	id := NewID()
	return Message{V: 1, ID: id, Thread: id, Agent: "backend", Subject: "orders", Body: body, SentAt: time.Now().UTC()}
}

// knows returns a peer lookup that knows exactly the given identities.
func knows(ids map[string]Identity) PeerLookup {
	return func(id Identity) (string, bool) {
		for name, known := range ids {
			if known.Equal(id) {
				return name, true
			}
		}
		return "", false
	}
}

func TestMessageValidate(t *testing.T) {
	ok := testMessage("hello")
	if err := ok.Validate(); err != nil {
		t.Fatalf("a good message failed: %v", err)
	}
	bad := map[string]func(m *Message){
		"version":        func(m *Message) { m.V = 2 },
		"id short":       func(m *Message) { m.ID = "abc" },
		"id upper":       func(m *Message) { m.ID = strings.ToUpper(m.ID) },
		"thread missing": func(m *Message) { m.Thread = "" },
		"reply_to bad":   func(m *Message) { m.ReplyTo = "zz" },
		"agent space":    func(m *Message) { m.Agent = "back end" },
		"agent long":     func(m *Message) { m.Agent = strings.Repeat("a", 65) },
		"subject long":   func(m *Message) { m.Subject = strings.Repeat("s", MaxSubjectBytes+1) },
		"body long":      func(m *Message) { m.Body = strings.Repeat("b", MaxBodyBytes+1) },
		"body empty":     func(m *Message) { m.Body = "" },
		"no time":        func(m *Message) { m.SentAt = time.Time{} },
	}
	for name, mutate := range bad {
		m := ok
		mutate(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: Validate accepted it", name)
		}
	}
	m := ok
	m.Agent = ""
	if err := m.Validate(); err != nil {
		t.Errorf("an empty agent label is for anyone, and must validate: %v", err)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	alice, bob := mustKeys(t), mustKeys(t)
	m := testMessage("is POST /orders returning totals yet?")
	box, err := Seal(alice, bob.Identity(), m)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(box, []byte("POST /orders")) || bytes.Contains(box, []byte(m.ID)) {
		t.Fatal("the sealed box carries plaintext")
	}
	got, err := Open(bob, box, knows(map[string]Identity{"alice": alice.Identity()}))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got.Peer != "alice" || !got.From.Equal(alice.Identity()) {
		t.Fatalf("opened from %q %v", got.Peer, got.From)
	}
	if got.Msg.Body != m.Body || got.Msg.ID != m.ID || got.Msg.Agent != "backend" || !got.Msg.SentAt.Equal(m.SentAt) {
		t.Fatalf("message changed in transit: %+v", got.Msg)
	}
}

func TestSealRefusesInvalidMessage(t *testing.T) {
	alice, bob := mustKeys(t), mustKeys(t)
	m := testMessage(strings.Repeat("x", MaxBodyBytes+1))
	if _, err := Seal(alice, bob.Identity(), m); err == nil {
		t.Fatal("Seal accepted an oversized body")
	}
}

func TestOpenRejects(t *testing.T) {
	alice, bob, carol := mustKeys(t), mustKeys(t), mustKeys(t)
	everyone := knows(map[string]Identity{"alice": alice.Identity(), "bob": bob.Identity(), "carol": carol.Identity()})
	m := testMessage("secret")
	box, err := Seal(alice, bob.Identity(), m)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("wrong recipient key", func(t *testing.T) {
		if _, err := Open(carol, box, everyone); err == nil {
			t.Fatal("carol opened mail sealed to bob")
		}
	})

	t.Run("any flipped byte", func(t *testing.T) {
		for i := range box {
			bad := bytes.Clone(box)
			bad[i] ^= 0x01
			if _, err := Open(bob, bad, everyone); err == nil {
				t.Fatalf("a box with byte %d flipped opened", i)
			}
		}
	})

	t.Run("truncated", func(t *testing.T) {
		for _, n := range []int{0, 1, 31, 32, len(box) - 1} {
			if _, err := Open(bob, box[:n], everyone); err == nil {
				t.Fatalf("a box cut to %d bytes opened", n)
			}
		}
	})

	t.Run("unknown sender", func(t *testing.T) {
		_, err := Open(bob, box, knows(map[string]Identity{"carol": carol.Identity()}))
		if err == nil || RejectReason(err) != ReasonUnknownSender {
			t.Fatalf("mail from a sender bob never added: %v (reason %q)", err, RejectReason(err))
		}
	})

	// Bob opens alice's mail and seals the same signed inner envelope to
	// carol, hoping carol reads it as alice writing to her. The recipient is
	// inside alice's signature, so carol sees it was written to bob.
	t.Run("forwarded to someone else", func(t *testing.T) {
		inner := openInnerForTest(t, bob, box)
		fwd := sealInnerForTest(t, carol.Identity(), inner)
		_, err := Open(carol, fwd, everyone)
		if err == nil || RejectReason(err) != ReasonNotForMe {
			t.Fatalf("a forwarded message opened: %v (reason %q)", err, RejectReason(err))
		}
		// Rewriting "to" breaks alice's signature instead.
		inner.To = carol.Identity().String()
		fwd = sealInnerForTest(t, carol.Identity(), inner)
		_, err = Open(carol, fwd, everyone)
		if err == nil || RejectReason(err) != ReasonBadSignature {
			t.Fatalf("a re-addressed message opened: %v (reason %q)", err, RejectReason(err))
		}
	})

	// Carol claims to be alice: she signs with her own key and names alice as
	// the sender.
	t.Run("signed by someone other than from", func(t *testing.T) {
		inner := openInnerForTest(t, bob, box)
		msg := inner.Msg
		inner.Sig = ed25519.Sign(carol.sign, signedMessageBytes(bob.Identity(), msg))
		fwd := sealInnerForTest(t, bob.Identity(), inner)
		_, err := Open(bob, fwd, everyone)
		if err == nil || RejectReason(err) != ReasonBadSignature {
			t.Fatalf("mail signed by carol as alice opened: %v (reason %q)", err, RejectReason(err))
		}
	})

	t.Run("invalid message inside a good signature", func(t *testing.T) {
		bad := testMessage("x")
		bad.Agent = "no spaces allowed"
		raw, _ := json.Marshal(bad)
		inner := innerEnvelope{From: alice.Identity().String(), To: bob.Identity().String(), Msg: raw}
		inner.Sig = ed25519.Sign(alice.sign, signedMessageBytes(bob.Identity(), raw))
		_, err := Open(bob, sealInnerForTest(t, bob.Identity(), inner), everyone)
		if err == nil || RejectReason(err) != ReasonInvalid {
			t.Fatalf("an invalid message opened: %v (reason %q)", err, RejectReason(err))
		}
	})
}

func openInnerForTest(t *testing.T, k *Keys, box []byte) innerEnvelope {
	t.Helper()
	priv, err := hpke.NewDHKEMPrivateKey(k.kem)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := hpke.Open(priv, hpke.HKDFSHA256(), hpke.ChaCha20Poly1305(), []byte(hpkeInfo), box)
	if err != nil {
		t.Fatal(err)
	}
	var inner innerEnvelope
	if err := json.Unmarshal(plain, &inner); err != nil {
		t.Fatal(err)
	}
	return inner
}

func sealInnerForTest(t *testing.T, to Identity, inner innerEnvelope) []byte {
	t.Helper()
	plain, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	box, err := sealBytes(to, plain)
	if err != nil {
		t.Fatal(err)
	}
	return box
}
