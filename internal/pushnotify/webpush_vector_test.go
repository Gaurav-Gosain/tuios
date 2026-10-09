package pushnotify

import (
	"bytes"
	"crypto/ecdh"
	"strings"
	"testing"
)

// TestEncryptRFC8291AppendixA is a wire compatibility test: the example of
// RFC 8291, section 5, with the intermediate values of its Appendix A. A
// phone decrypts what Encrypt makes with its own RFC 8291 code, so the bytes
// must be the RFC's to the octet. The example has no padding.
func TestEncryptRFC8291AppendixA(t *testing.T) {
	b := func(s string) []byte {
		t.Helper()
		v, err := DecodeKey(strings.Join(strings.Fields(s), ""))
		if err != nil {
			t.Fatalf("decode %q: %v", s, err)
		}
		return v
	}
	plaintext := b("V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPrivate, err := ecdh.P256().NewPrivateKey(b("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	wantASPublic := b(`BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIg
		Dll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8`)
	if !bytes.Equal(asPrivate.PublicKey().Bytes(), wantASPublic) {
		t.Fatalf("as_public does not match the RFC")
	}
	uaPublic, err := ecdh.P256().NewPublicKey(b(`BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-
		JvLexhqUzORcx aOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4`))
	if err != nil {
		t.Fatal(err)
	}
	salt := b("DGv6ra1nlYgDCS1FRnbzlw")
	auth := b("BTBZMqHH6r4Tts7J_aSIgg")

	got, err := encrypt(asPrivate, uaPublic, auth, salt, plaintext, 0)
	if err != nil {
		t.Fatal(err)
	}
	header := b(`DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z 9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml
		mlMoZIIgDll6e3vCYLocInmYWAmS6Tlz AC8wEqKK6PBru3jl7A8`)
	ciphertext := b(`8pfeW0KbunFT06SuDKoJH9Ql87S1QUrd irN6GcG7sFz1y1sqLgVi1VhjVkHsUoEs
		bI_0LpXMuGvnzQ`)
	want := append(append([]byte{}, header...), ciphertext...)
	if !bytes.Equal(got, want) {
		t.Fatalf("Encrypt of the RFC 8291 example:\n got %x\nwant %x", got, want)
	}
}
