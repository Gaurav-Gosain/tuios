package courier

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func mustKeys(t *testing.T) *Keys {
	t.Helper()
	k, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys: %v", err)
	}
	return k
}

func TestIdentityRoundTrip(t *testing.T) {
	k := mustKeys(t)
	id := k.Identity()
	s := id.String()
	if !strings.HasPrefix(s, IdentityPrefix) {
		t.Fatalf("identity %q lacks prefix %q", s, IdentityPrefix)
	}
	got, err := ParseIdentity(s)
	if err != nil {
		t.Fatalf("ParseIdentity(%q): %v", s, err)
	}
	if !got.Equal(id) || got.String() != s {
		t.Fatalf("round trip changed the identity: %q -> %q", s, got.String())
	}
	// Surrounding whitespace is what a paste carries; it is not part of the key.
	if got, err := ParseIdentity("  " + s + "\n"); err != nil || !got.Equal(id) {
		t.Fatalf("a pasted identity with whitespace did not parse: %v", err)
	}
}

func TestIdentityFingerprintAndMailbox(t *testing.T) {
	a, b := mustKeys(t).Identity(), mustKeys(t).Identity()
	fp := regexp.MustCompile(`^[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}$`)
	if !fp.MatchString(a.Fingerprint()) {
		t.Fatalf("fingerprint %q has the wrong shape", a.Fingerprint())
	}
	again, err := ParseIdentity(a.String())
	if err != nil || again.Fingerprint() != a.Fingerprint() || again.MailboxID() != a.MailboxID() {
		t.Fatal("fingerprint or mailbox changed across a round trip")
	}
	mb := regexp.MustCompile(`^[0-9a-f]{32}$`)
	if !mb.MatchString(a.MailboxID()) {
		t.Fatalf("mailbox id %q has the wrong shape", a.MailboxID())
	}
	if a.MailboxID() == b.MailboxID() || a.Fingerprint() == b.Fingerprint() {
		t.Fatal("two identities share a mailbox or a fingerprint")
	}
	if a.Equal(b) {
		t.Fatal("two generated identities compare equal")
	}
}

func TestParseIdentityRejects(t *testing.T) {
	good := mustKeys(t).Identity().String()
	body := strings.TrimPrefix(good, IdentityPrefix)
	for name, in := range map[string]string{
		"empty":        "",
		"prefix only":  IdentityPrefix,
		"wrong prefix": "tc9." + body,
		"no prefix":    body,
		"bad base64":   IdentityPrefix + strings.Repeat("!", len(body)),
		"short":        IdentityPrefix + body[:len(body)-4],
		"long":         IdentityPrefix + body + "AAAA",
		"padded":       good + "==",
		"inner space":  IdentityPrefix + body[:10] + " " + body[10:],
	} {
		if _, err := ParseIdentity(in); err == nil {
			t.Errorf("%s: ParseIdentity(%q) accepted it", name, in)
		}
	}
}

func TestKeysSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "identity.key")
	k := mustKeys(t)
	if err := SaveKeys(path, k); err != nil {
		t.Fatalf("SaveKeys: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode %v, want 0600", st.Mode().Perm())
		}
	}
	got, err := LoadKeys(path)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if !got.Identity().Equal(k.Identity()) {
		t.Fatal("loaded keys have a different identity")
	}

	// A second save never replaces a key: losing it loses every peer's trust.
	if err := SaveKeys(path, mustKeys(t)); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("SaveKeys over an existing key: %v, want ErrKeyExists", err)
	}
	again, err := LoadKeys(path)
	if err != nil || !again.Identity().Equal(k.Identity()) {
		t.Fatalf("the existing key changed after a refused save: %v", err)
	}
}

func TestLoadKeysRefusesOpenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not checked on windows")
	}
	path := filepath.Join(t.TempDir(), "identity.key")
	if err := SaveKeys(path, mustKeys(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeys(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("LoadKeys on a 0644 key: %v, want a refusal naming chmod 600", err)
	}
}

func TestLoadKeysRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.key")
	for _, body := range []string{"", "{}", `{"v":2}`, `{"v":1,"sign_seed":"AA","kem_key":"AA"}`, "not json"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadKeys(path); err == nil {
			t.Errorf("LoadKeys accepted %q", body)
		}
	}
}
