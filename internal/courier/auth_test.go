package courier

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func anyone(Identity) bool { return true }

func TestRequestSignVerify(t *testing.T) {
	k := mustKeys(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	v := NewVerifier(clock.Now)
	body := []byte(`{"ids":["x"]}`)
	h := SignRequest(k, "POST", "/v1/ack?x=1", body, clock.Now())
	id, err := v.Verify(h, "POST", "/v1/ack?x=1", body, anyone)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !id.Equal(k.Identity()) {
		t.Fatal("Verify returned another identity")
	}
}

func TestRequestVerifyRejectsChanges(t *testing.T) {
	k := mustKeys(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	body := []byte("box")
	cases := map[string]func() (h, method, path string, body []byte){
		"method": func() (string, string, string, []byte) {
			return SignRequest(k, "POST", "/v1/mail/a", body, clock.Now()), "GET", "/v1/mail/a", body
		},
		"path": func() (string, string, string, []byte) {
			return SignRequest(k, "POST", "/v1/mail/a", body, clock.Now()), "POST", "/v1/mail/b", body
		},
		"query": func() (string, string, string, []byte) {
			return SignRequest(k, "GET", "/v1/mail?wait=1", nil, clock.Now()), "GET", "/v1/mail?wait=50", nil
		},
		"body": func() (string, string, string, []byte) {
			return SignRequest(k, "POST", "/v1/mail/a", body, clock.Now()), "POST", "/v1/mail/a", []byte("boy")
		},
		"too old": func() (string, string, string, []byte) {
			return SignRequest(k, "GET", "/v1/mail", nil, clock.Now().Add(-121*time.Second)), "GET", "/v1/mail", nil
		},
		"from the future": func() (string, string, string, []byte) {
			return SignRequest(k, "GET", "/v1/mail", nil, clock.Now().Add(121*time.Second)), "GET", "/v1/mail", nil
		},
	}
	for name, c := range cases {
		v := NewVerifier(clock.Now)
		h, method, path, b := c()
		if _, err := v.Verify(h, method, path, b, anyone); err == nil {
			t.Errorf("%s: a changed request verified", name)
		}
	}
}

func TestRequestVerifyRefusesReplay(t *testing.T) {
	k := mustKeys(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	v := NewVerifier(clock.Now)
	h := SignRequest(k, "GET", "/v1/mail", nil, clock.Now())
	if _, err := v.Verify(h, "GET", "/v1/mail", nil, anyone); err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second)
	if _, err := v.Verify(h, "GET", "/v1/mail", nil, anyone); err == nil {
		t.Fatal("the same signed request verified twice")
	}
	// Once the timestamp is outside the window the nonce may be forgotten:
	// the request is refused for its age instead.
	clock.Advance(10 * time.Minute)
	if _, err := v.Verify(h, "GET", "/v1/mail", nil, anyone); err == nil {
		t.Fatal("an expired request verified")
	}
	if n := v.nonceCount(); n != 0 {
		t.Fatalf("%d nonces kept after their window", n)
	}
}

func TestRequestVerifyChecksAllowedBeforeRemembering(t *testing.T) {
	outsider := mustKeys(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	v := NewVerifier(clock.Now)
	for range 100 {
		h := SignRequest(outsider, "GET", "/v1/mail", nil, clock.Now())
		if _, err := v.Verify(h, "GET", "/v1/mail", nil, func(Identity) bool { return false }); err == nil {
			t.Fatal("an identity that is not allowed verified")
		}
	}
	if n := v.nonceCount(); n != 0 {
		t.Fatalf("an outsider filled the nonce cache with %d entries", n)
	}
}

func TestRequestVerifyBoundsNonces(t *testing.T) {
	k := mustKeys(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	v := NewVerifier(clock.Now)
	refused := 0
	for range maxNoncesPerIdentity + 10 {
		h := SignRequest(k, "GET", "/v1/mail", nil, clock.Now())
		if _, err := v.Verify(h, "GET", "/v1/mail", nil, anyone); err != nil {
			refused++
		}
	}
	if refused == 0 || v.nonceCount() > maxNoncesPerIdentity {
		t.Fatalf("nonce cache unbounded: %d refused, %d kept", refused, v.nonceCount())
	}
}

func TestRequestVerifyMalformed(t *testing.T) {
	k := mustKeys(t)
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	good := SignRequest(k, "GET", "/v1/mail", nil, clock.Now())
	for _, h := range []string{
		"", "TC1", "Bearer abc", "TC1 id=", "TC1 id=x, ts=1, nonce=AA, sig=AA",
		strings.Replace(good, "ts=", "ts=x", 1),
		strings.Replace(good, "sig=", "sig=!!", 1),
		strings.Replace(good, "nonce=", "nonce=%%", 1),
		good + ", extra=1, extra=2",
		strings.Repeat("TC1 id=a, ", 1000),
	} {
		v := NewVerifier(clock.Now)
		if _, err := v.Verify(h, "GET", "/v1/mail", nil, anyone); err == nil {
			t.Errorf("malformed header verified: %q", clip(h))
		}
	}
}
