package tuie2e

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Web Push, end to end: a real daemon, a stub push service on loopback (the
// test's own HTTP server), and this test as the phone. The phone registers
// with register-push over the phone link, with the nonce of attach-presence.
// The real Claude Code hook holds an approval, and the stub receives the
// push. The test decrypts it with the phone's private key, by its own
// implementation of RFC 8291 and RFC 8188, checks the payload, the headers
// and the VAPID JWT, then answers the approval and decrypts the close.
//
// It also checks who may register: no nonce, a made-up nonce, a link without
// the respond capability, and a process inside a pane holding the person's
// live nonce are all refused. Each refusal has its positive half: the same
// call with the person's nonce, over the phone link, from outside every pane,
// is taken.
//
// And delivery: a push service that answers 410 has its phone removed, one
// that answers 503 once gets the push on the retry, and a phone that asked for
// another kind gets nothing.
//
// The artifact is webpush-transcript.json in TUIOS_E2E_FRAMES: every request
// the stub received, decrypted.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - the n.web.note call cut from pushNotifier.note: no push arrives.
//   - the requirePushPerson call cut from verbRegisterPush: a call with no
//     nonce registers.
//   - the register-push row cut from the link policy table: the link
//     without respond registers.
//   - the ErrGone arm cut from webPusher.deliver: the 410 phone stays.
//   - the retry cut (no RetryableError check): the 503 phone never gets it.

// stubPush is the push service: it records each request and answers with the
// status its path asks for.
type stubPush struct {
	srv  *http.Server
	ln   net.Listener
	mu   sync.Mutex
	reqs []stubReq
	// flaky counts the requests to /s/flaky, which fail once.
	flaky int
	got   chan stubReq
}

type stubReq struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    []byte            `json:"-"`
	Status  int               `json:"status"`
	// Payload is the decrypted JSON, filled in by the test.
	Payload map[string]any `json:"payload,omitempty"`
}

func startStubPush(t *testing.T) *stubPush {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &stubPush{ln: ln, got: make(chan stubReq, 64)}
	s.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		h := map[string]string{}
		for _, k := range []string{"Content-Encoding", "Content-Type", "Ttl", "Urgency", "Topic", "Authorization"} {
			h[k] = r.Header.Get(k)
		}
		status := http.StatusCreated
		s.mu.Lock()
		switch r.URL.Path {
		case "/s/gone":
			status = http.StatusGone
		case "/s/redirect":
			status = http.StatusTemporaryRedirect
			w.Header().Set("Location", "/s/landed")
		case "/s/flaky":
			s.flaky++
			if s.flaky == 1 {
				status = http.StatusServiceUnavailable
			}
		}
		req := stubReq{Path: r.URL.Path, Headers: h, Body: body, Status: status}
		s.reqs = append(s.reqs, req)
		s.mu.Unlock()
		w.WriteHeader(status)
		select {
		case s.got <- req:
		default:
		}
	})}
	go func() { _ = s.srv.Serve(ln) }()
	t.Cleanup(func() { _ = s.srv.Close() })
	return s
}

func (s *stubPush) url(path string) string { return "http://" + s.ln.Addr().String() + path }

func (s *stubPush) requests(path string) []stubReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []stubReq
	for _, r := range s.reqs {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// phoneKeys is a subscription's private half, kept by the test as a phone
// keeps it.
type phoneKeys struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newPhoneKeys(t *testing.T) phoneKeys {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return phoneKeys{priv: priv, auth: auth}
}

func (k phoneKeys) p256dh() string {
	return base64.RawURLEncoding.EncodeToString(k.priv.PublicKey().Bytes())
}
func (k phoneKeys) authB64() string { return base64.RawURLEncoding.EncodeToString(k.auth) }

// hmacSHA256 is HMAC-SHA-256(key, data).
func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// decrypt is the phone's side of RFC 8291 over one aes128gcm record, written
// from the RFCs and not from the daemon's code.
func (k phoneKeys) decrypt(body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, fmt.Errorf("body of %d bytes is shorter than the header", len(body))
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	if idlen != 65 || len(body) < 21+idlen {
		return nil, fmt.Errorf("keyid length %d, want 65", idlen)
	}
	asPublic := body[21 : 21+idlen]
	ct := body[21+idlen:]
	if uint32(len(ct)) > rs {
		return nil, fmt.Errorf("the record of %d bytes is longer than rs %d", len(ct), rs)
	}
	as, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		return nil, fmt.Errorf("keyid is not a P-256 point: %w", err)
	}
	ecdhSecret, err := k.priv.ECDH(as)
	if err != nil {
		return nil, err
	}
	uaPublic := k.priv.PublicKey().Bytes()
	prkKey := hmacSHA256(k.auth, ecdhSecret)
	keyInfo := append(append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...), 1)
	ikm := hmacSHA256(prkKey, keyInfo)
	prk := hmacSHA256(salt, ikm)
	cek := hmacSHA256(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := hmacSHA256(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("AES-GCM open: %w", err)
	}
	// The last record ends in 0x02, then any zero padding was before it.
	end := len(plain) - 1
	for end >= 0 && plain[end] == 0 {
		end--
	}
	if end < 0 || plain[end] != 2 {
		return nil, fmt.Errorf("no last-record delimiter")
	}
	return plain[:end], nil
}

// checkVAPID verifies the Authorization header against the key register-push
// returned, and returns the JWT's claims.
func checkVAPID(t *testing.T, header, wantKey string) map[string]any {
	t.Helper()
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		t.Fatalf("Authorization %q is not vapid", header)
	}
	var tok, key string
	for part := range strings.SplitSeq(rest, ",") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, "t="); ok {
			tok = v
		}
		if v, ok := strings.CutPrefix(part, "k="); ok {
			key = v
		}
	}
	if key != wantKey {
		t.Fatalf("vapid k=%q, want the key register-push returned %q", key, wantKey)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("vapid t is not a JWT: %q", tok)
	}
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var h map[string]any
	if json.Unmarshal(hdr, &h) != nil || h["alg"] != "ES256" || h["typ"] != "JWT" {
		t.Fatalf("JWT header %s, want ES256 JWT", hdr)
	}
	pub, _ := base64.RawURLEncoding.DecodeString(key)
	if len(pub) != 65 || pub[0] != 4 {
		t.Fatalf("vapid key is not an uncompressed point")
	}
	pk := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(pub[1:33]), Y: new(big.Int).SetBytes(pub[33:])}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pk, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatalf("the VAPID JWT signature does not verify")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("JWT claims: %v", err)
	}
	return claims
}

// waitPush waits for a push to path that decrypts to a payload pred accepts.
func waitPush(t *testing.T, s *stubPush, k phoneKeys, path string, pred func(map[string]any) bool) stubReq {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	seen := 0
	for time.Now().Before(deadline) {
		reqs := s.requests(path)
		for ; seen < len(reqs); seen++ {
			r := reqs[seen]
			if r.Status >= 300 {
				continue
			}
			plain, err := k.decrypt(r.Body)
			if err != nil {
				t.Fatalf("decrypt the push to %s: %v", path, err)
			}
			if err := json.Unmarshal(plain, &r.Payload); err != nil {
				t.Fatalf("the push to %s is not JSON: %v\n%s", path, err, plain)
			}
			if len(plain) > 3<<10 {
				t.Fatalf("the payload is %d bytes, over 3 KiB", len(plain))
			}
			if pred(r.Payload) {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no push to %s matched; got %d requests", path, len(s.requests(path)))
	return stubReq{}
}

func TestWebPushToAPhone(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n\n" +
		"[notify]\nallow_http_redirects = true\n\n" +
		"[notify.webpush]\nallow_insecure = true\n\n" +
		"[hosts.phone]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\"]\n\n" +
		"[hosts.viewer]\nallow = [\"list\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", streamSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	stub := startStubPush(t)
	var transcript []stubReq
	defer func() {
		if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
			data, _ := json.MarshalIndent(transcript, "", "  ")
			_ = os.WriteFile(filepath.Join(dir, "webpush-transcript.json"), data, 0o644)
		}
	}()

	link := startPhoneLink(t, base)
	ctl := link.open(t, true)
	pres, verr := ctl.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence: %v", verr)
	}
	nonce := pres["human_nonce"].(string)
	phone := newPhoneKeys(t)
	reg := func(device, path string, k phoneKeys, kinds []string, n string) (map[string]any, *spVerbErr) {
		p := map[string]any{"endpoint": stub.url(path), "p256dh": k.p256dh(), "auth": k.authB64(), "device": device}
		if kinds != nil {
			p["kinds"] = kinds
		}
		if n != "" {
			p["human_nonce"] = n
		}
		return ctl.call(t, "register-push", p)
	}

	// --- Refusals, each before its positive half.
	if _, verr := reg("pixel", "/s/pixel", phone, nil, ""); verr == nil || verr.Code != "not_human" {
		t.Fatalf("ASSERTION: register-push with no nonce: want not_human, got %v", verr)
	}
	if _, verr := reg("pixel", "/s/pixel", phone, nil, "0000"); verr == nil || verr.Code != "not_human" {
		t.Fatalf("ASSERTION: register-push with a made-up nonce: want not_human, got %v", verr)
	}
	if _, verr := ctl.call(t, "list-push", nil); verr == nil || verr.Code != "not_human" {
		t.Fatalf("ASSERTION: list-push with no nonce: want not_human, got %v", verr)
	}
	viewer := startLinkAs(t, base, "viewer").open(t, true)
	vpres, verr := viewer.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence over the viewer link: %v", verr)
	}
	if _, verr := viewer.call(t, "register-push", map[string]any{"endpoint": stub.url("/s/viewer"), "p256dh": phone.p256dh(), "auth": phone.authB64(), "device": "viewer", "human_nonce": vpres["human_nonce"]}); verr == nil || verr.Code != "forbidden" {
		t.Fatalf("ASSERTION: register-push over a link without respond: want forbidden, got %v", verr)
	}
	// Plain http to a public address is refused even with allow_insecure.
	if _, verr := ctl.call(t, "register-push", map[string]any{"endpoint": "http://203.0.113.9/s/x", "p256dh": phone.p256dh(), "auth": phone.authB64(), "device": "x", "human_nonce": nonce}); verr == nil || verr.Code != "invalid_params" {
		t.Fatalf("register-push to http on a public address: want invalid_params, got %v", verr)
	}

	// From a pane, with the person's live nonce in hand.
	wins, verr := ctl.call(t, "list-windows", map[string]any{"session": streamSession})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	w0 := wins["windows"].([]any)[0].(map[string]any)["window_id"].(string)
	line := fmt.Sprintf("%s notify push register --device pane --endpoint %s --p256dh %s --auth %s --human-nonce %s; echo PANE_EXIT=$?\n",
		tuiosBin, stub.url("/s/pane"), phone.p256dh(), phone.authB64(), nonce)
	if out, err := tuiosCLI(t, base, "send-text", "-s", streamSession, "-w", w0, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	paneOut := ""
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) && !strings.Contains(paneOut, "PANE_EXIT=") {
		time.Sleep(100 * time.Millisecond)
		paneOut, _ = tuiosCLI(t, base, "capture-pane", "-s", streamSession, "-w", w0)
	}
	if !strings.Contains(paneOut, "PANE_EXIT=1") || !strings.Contains(paneOut, "for the person") {
		t.Fatalf("ASSERTION: register-push from a pane was not refused:\n%s", paneOut)
	}
	// The positive half: the same command outside every pane, with a
	// presence of its own, is taken.
	if out, err := tuiosCLI(t, base, "notify", "push", "register", "--device", "desk", "--endpoint", stub.url("/s/desk"), "--p256dh", phone.p256dh(), "--auth", phone.authB64(), "--kind", "finished"); err != nil || !strings.Contains(out, "Registered desk") {
		t.Fatalf("notify push register outside a pane: %v\n%s", err, out)
	}

	// --- Register the phone, a gone one, a flaky one and one for another kind.
	res, verr := reg("pixel", "/s/pixel", phone, nil, nonce)
	if verr != nil {
		t.Fatalf("register-push: %v", verr)
	}
	vapidKey, _ := res["vapid_public_key"].(string)
	machine, _ := res["machine"].(string)
	if fmt.Sprint(res["kinds"]) != "[approval plan ask question]" || vapidKey == "" {
		t.Fatalf("register-push reply: %v", res)
	}
	gone, flaky, hop := newPhoneKeys(t), newPhoneKeys(t), newPhoneKeys(t)
	// The endpoint an Android emulator's UnifiedPush distributor gives, with
	// adb reverse taking its loopback to a push service on this machine.
	android := newPhoneKeys(t)
	if _, verr := reg("android", "/upAbC123xyz?up=1", android, nil, nonce); verr != nil {
		t.Fatalf("ASSERTION: register-push of a loopback UnifiedPush endpoint with allow_insecure: %v", verr)
	}
	for _, r := range []struct {
		device, path string
		k            phoneKeys
	}{{"old", "/s/gone", gone}, {"flaky", "/s/flaky", flaky}, {"hop", "/s/redirect", hop}} {
		if _, verr := reg(r.device, r.path, r.k, nil, nonce); verr != nil {
			t.Fatalf("register %s: %v", r.device, verr)
		}
	}
	// The files the daemon keeps, private to the person.
	stateRoot := xdgDir(base, "XDG_STATE_HOME")
	found := 0
	_ = filepath.WalkDir(stateRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "push" {
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0o600 {
				t.Errorf("%s has mode %v, want 0600", path, info.Mode().Perm())
			}
			found++
		}
		return nil
	})
	if found != 2 {
		t.Fatalf("want the subscriptions and the VAPID key under the state dir, found %d files", found)
	}

	// --- An approval opens: the phone gets it, decrypted and signed.
	hook := startApprovalHook(t, base, w0)
	open := waitPush(t, stub, phone, "/s/pixel", func(p map[string]any) bool {
		return p["type"] == "open" && p["request_id"] != nil && p["request_id"] != ""
	})
	transcript = append(transcript, stub.requests("/s/pixel")...)
	p := open.Payload
	for k, want := range map[string]any{"v": float64(1), "kind": "approval", "session": streamSession, "harness": "claude-code", "machine": machine, "summary": "approve Bash: npm test"} {
		if p[k] != want {
			t.Errorf("payload %s = %v, want %v (payload %v)", k, p[k], want, p)
		}
	}
	if w, _ := p["window"].(string); !strings.HasPrefix(w0, w) || w == "" {
		t.Errorf("payload window %v, want %s", p["window"], w0)
	}
	if opts, _ := p["options"].([]any); len(opts) == 0 {
		t.Errorf("payload has no options: %v", p)
	}
	h := open.Headers
	if h["Content-Encoding"] != "aes128gcm" || h["Ttl"] != "120" || h["Urgency"] != "high" || len(h["Topic"]) != 32 || h["Content-Type"] != "application/octet-stream" {
		t.Errorf("push headers %v", h)
	}
	claims := checkVAPID(t, h["Authorization"], vapidKey)
	if claims["aud"] != "http://"+stub.ln.Addr().String() {
		t.Errorf("JWT aud %v, want the stub's origin", claims["aud"])
	}
	exp, _ := claims["exp"].(float64)
	if left := time.Until(time.Unix(int64(exp), 0)); left <= 0 || left > 24*time.Hour {
		t.Errorf("JWT exp is %v from now, want within 24 h", left)
	}
	if sub, _ := claims["sub"].(string); !strings.HasPrefix(sub, "https://") || strings.Contains(sub, "@") {
		t.Errorf("JWT sub %q, want an https URL with no address", sub)
	}

	// The flaky service got it on the retry. The gone one lost its phone.
	// The push the 503 refused is sent again: the same payload, encrypted
	// again, in a later request.
	failed := stub.requests("/s/flaky")[0]
	if failed.Status != http.StatusServiceUnavailable {
		t.Fatalf("the flaky service's first answer was %d", failed.Status)
	}
	firstPlain, err := flaky.decrypt(failed.Body)
	if err != nil {
		t.Fatalf("decrypt the refused push: %v", err)
	}
	waitPush(t, stub, flaky, "/s/flaky", func(p map[string]any) bool {
		again, _ := json.Marshal(p)
		var first map[string]any
		_ = json.Unmarshal(firstPlain, &first)
		want, _ := json.Marshal(first)
		return string(again) == string(want)
	})
	waitPush(t, stub, android, "/upAbC123xyz", func(p map[string]any) bool { return p["type"] == "open" })

	// A push service that redirects is not followed, even with
	// allow_http_redirects for the other providers, and not tried again.
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && len(stub.requests("/s/redirect")) == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(6 * time.Second) // past the first two retry waits
	if n := len(stub.requests("/s/landed")); n != 0 {
		t.Fatalf("ASSERTION: the push followed the redirect: /s/landed got %d requests", n)
	}
	// A retry sends the same payload again. Each push here is a new one.
	seenPlain := map[string]bool{}
	for _, r := range stub.requests("/s/redirect") {
		plain, err := hop.decrypt(r.Body)
		if err != nil {
			t.Fatalf("decrypt the push to /s/redirect: %v", err)
		}
		if seenPlain[string(plain)] {
			t.Fatalf("ASSERTION: the push the redirect refused was tried again: %s", plain)
		}
		seenPlain[string(plain)] = true
	}

	deadline = time.Now().Add(15 * time.Second)
	var listed map[string]any
	for time.Now().Before(deadline) {
		listed, verr = ctl.call(t, "list-push", map[string]any{"human_nonce": nonce})
		if verr != nil {
			t.Fatalf("list-push: %v", verr)
		}
		if !strings.Contains(fmt.Sprint(listed["devices"]), "device:old") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(fmt.Sprint(listed["devices"]), "device:old") || len(stub.requests("/s/gone")) == 0 {
		t.Fatalf("ASSERTION: the phone whose service answered 410 is still listed: %v", listed)
	}
	if s := fmt.Sprint(listed["devices"]); strings.Contains(s, "/s/") || !strings.Contains(s, "device:pixel") {
		t.Errorf("list-push shows %s, want pixel with only the service origin", s)
	}

	// --- The person answers: the phone gets the close, under the same topic.
	reqID := open.Payload["request_id"].(string)
	if _, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": reqID, "decision": "deny", "human_nonce": nonce}); verr != nil {
		t.Fatalf("reply-approval: %v", verr)
	}
	hook.wait(t)
	closed := waitPush(t, stub, phone, "/s/pixel", func(p map[string]any) bool {
		return p["type"] == "close" && p["id"] == open.Payload["id"]
	})
	transcript = append(transcript, closed)
	if closed.Headers["Topic"] != h["Topic"] {
		t.Errorf("the close's topic %q differs from the open's %q", closed.Headers["Topic"], h["Topic"])
	}
	if len(stub.requests("/s/desk")) != 0 {
		t.Errorf("the phone registered for finished got an approval push")
	}

	// --- remove-push.
	if _, verr := ctl.call(t, "remove-push", map[string]any{"device": "pixel"}); verr == nil || verr.Code != "not_human" {
		t.Fatalf("remove-push with no nonce: want not_human, got %v", verr)
	}
	if res, verr := ctl.call(t, "remove-push", map[string]any{"device": "pixel", "human_nonce": nonce}); verr != nil || res["removed"] != true {
		t.Fatalf("remove-push: %v %v", res, verr)
	}
	if _, verr := ctl.call(t, "remove-push", map[string]any{"device": "pixel", "human_nonce": nonce}); verr == nil || verr.Code != "invalid_params" {
		t.Fatalf("remove-push of a removed phone: want invalid_params, got %v", verr)
	}
	for i := range transcript {
		if transcript[i].Payload == nil {
			if plain, err := phone.decrypt(transcript[i].Body); err == nil {
				_ = json.Unmarshal(plain, &transcript[i].Payload)
			}
		}
	}
}

// TestWebPushRefusesLocalAddresses: without allow_insecure, register-push
// may not aim the daemon at this machine or its network. The caller can be a
// linked machine, so this is the line between a phone and a port scan of
// this machine's loopback.
//
// An https endpoint on a loopback or link-local IP literal, or on localhost,
// is refused when it is registered. A name the check cannot see through,
// tuios-test.localhost (RFC 6761: it resolves to loopback), is taken, and
// the daemon then refuses to connect to the address it resolved to: the
// listener on that port is never dialed, and list-push says why.
//
// The positive half is TestWebPushToAPhone: with allow_insecure, the same
// loopback stub gets every push.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - the hostAllowed check cut from CheckEndpoint's https arm: the loopback
//     literal registers.
//   - the Control hook cut from the push client's dialer: the listener is
//     dialed.
func TestWebPushRefusesLocalAddresses(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n\n" +
		"[hosts.phone]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", streamSession, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	// A listener that counts who connects, and hangs up.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dialedMu sync.Mutex
	dialed := 0
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dialedMu.Lock()
			dialed++
			dialedMu.Unlock()
			_ = c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	ctl := startPhoneLink(t, base).open(t, true)
	pres, verr := ctl.call(t, "attach-presence", nil)
	if verr != nil {
		t.Fatalf("attach-presence: %v", verr)
	}
	nonce := pres["human_nonce"].(string)
	phone := newPhoneKeys(t)
	reg := func(device, endpoint string) *spVerbErr {
		_, verr := ctl.call(t, "register-push", map[string]any{
			"endpoint": endpoint, "p256dh": phone.p256dh(), "auth": phone.authB64(),
			"device": device, "human_nonce": nonce,
		})
		return verr
	}
	for _, ep := range []string{
		fmt.Sprintf("https://127.0.0.1:%d/s/x", port),
		fmt.Sprintf("https://[::1]:%d/s/x", port),
		fmt.Sprintf("https://localhost:%d/s/x", port),
		"https://169.254.169.254/latest/meta-data",
		"https://0.0.0.0/s/x",
	} {
		if verr := reg("local", ep); verr == nil || verr.Code != "invalid_params" {
			t.Fatalf("ASSERTION: register-push to %s without allow_insecure: want invalid_params, got %v", ep, verr)
		}
	}

	if verr := reg("byname", fmt.Sprintf("https://tuios-test.localhost:%d/s/x", port)); verr != nil {
		t.Fatalf("register-push to a name: %v", verr)
	}
	wins, verr := ctl.call(t, "list-windows", map[string]any{"session": streamSession})
	if verr != nil {
		t.Fatalf("list-windows: %v", verr)
	}
	w0 := wins["windows"].([]any)[0].(map[string]any)["window_id"].(string)
	hook := startApprovalHook(t, base, w0)

	lastError := ""
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		dialedMu.Lock()
		n := dialed
		dialedMu.Unlock()
		if n > 0 {
			break
		}
		listed, verr := ctl.call(t, "list-push", map[string]any{"human_nonce": nonce})
		if verr != nil {
			t.Fatalf("list-push: %v", verr)
		}
		if devs, _ := listed["devices"].([]any); len(devs) == 1 {
			lastError, _ = devs[0].(map[string]any)["last_error"].(string)
			if lastError != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	dialedMu.Lock()
	n := dialed
	dialedMu.Unlock()
	if n != 0 {
		t.Fatalf("ASSERTION: the daemon connected %d times to a loopback address it reached by name", n)
	}
	if !strings.Contains(lastError, "loopback") {
		t.Fatalf("ASSERTION: list-push last_error %q, want the refusal of a loopback address", lastError)
	}

	// Let the hook go.
	deadline = time.Now().Add(uiTimeout)
	answered := false
	for !answered && time.Now().Before(deadline) {
		items, verr := ctl.call(t, "list-attention", nil)
		if verr != nil {
			t.Fatalf("list-attention: %v", verr)
		}
		list, _ := items["items"].([]any)
		for _, it := range list {
			if id, _ := it.(map[string]any)["request_id"].(string); id != "" {
				if _, verr := ctl.call(t, "reply-approval", map[string]any{"request_id": id, "decision": "deny", "human_nonce": nonce}); verr != nil {
					t.Fatalf("reply-approval: %v", verr)
				}
				answered = true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	hook.wait(t)
}
