package tuie2e

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// tuios pair, end to end: the real binary prints a pairing link, and this
// test is the phone. It reads the link, makes the MAC with the token, posts
// its key, and checks the reply against the token and the host key file.
// Then the key that was installed logs in to a real sshd and reaches a real
// daemon, which holds it to the link policy pair wrote.
//
// The failure modes this is written against:
//   - a request with a MAC that was not made with the token is accepted
//     (wrong token, or a key swapped in by a machine on the path);
//   - the token works twice, or after its time;
//   - a weak key, a bad device name, or a huge body is accepted;
//   - the key goes into authorized_keys without the forced command or
//     restrict, so it can open a shell or a port forward;
//   - the forced command does not pin the name, so the link policy of the
//     device does not apply;
//   - a key or a [hosts] table that is there already is changed;
//   - the QR code on screen holds something other than the printed link.

// pairPhone is the phone side: its key line and the token from the link.
type pairPhone struct {
	token []byte
	addr  string
	link  *url.URL
}

// pairRun is one tuios pair process.
type pairRun struct {
	cmd    *exec.Cmd
	lines  chan string
	stderr *lockedBuf
	done   chan error
	first  map[string]any
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// needTools skips when a tool the test drives is not installed.
func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

// keygen makes an ssh key pair under dir and returns the private key path and
// the public key line.
func keygen(t *testing.T, dir, name string, args ...string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, name)
	full := append([]string{"-q", "-N", "", "-C", name, "-f", path}, args...)
	if out, err := exec.Command("ssh-keygen", full...).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen %s: %v\n%s", name, err, out)
	}
	pub, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return path, strings.TrimSpace(string(pub))
}

// keyBody is the "TYPE BASE64" part of a key line.
func keyBody(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return line
	}
	return f[0] + " " + f[1]
}

// sha256FP is the fingerprint of a key line as ssh-keygen -lf prints it.
func sha256FP(t *testing.T, line string) string {
	t.Helper()
	f := strings.Fields(line)
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		t.Fatalf("decode key %q: %v", line, err)
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// keygenFP is the fingerprint ssh-keygen itself prints for a key file, so the
// test does not check tuios against a copy of its own arithmetic.
func keygenFP(t *testing.T, pubPath string) string {
	t.Helper()
	out, err := exec.Command("ssh-keygen", "-lf", pubPath).Output()
	if err != nil {
		t.Fatalf("ssh-keygen -lf: %v", err)
	}
	return strings.Fields(string(out))[1]
}

// startPair runs tuios pair --json with the environment of base and waits
// for the first line, the link.
func startPair(t *testing.T, base string, env []string, args ...string) *pairRun {
	t.Helper()
	return startPairInput(t, base, "", env, args...)
}

// startPairInput is startPair with what the person types on stdin.
func startPairInput(t *testing.T, base, input string, env []string, args ...string) *pairRun {
	t.Helper()
	return startPairStdin(t, base, strings.NewReader(input), env, args...)
}

// startPairStdin is startPair with stdin read from r, for a person who
// answers later. The test's phone posts from this machine, so it passes
// --accept-local. startPairFromHere leaves it out.
func startPairStdin(t *testing.T, base string, stdin io.Reader, env []string, args ...string) *pairRun {
	t.Helper()
	return startPairFromHere(t, base, stdin, env, append([]string{"--accept-local"}, args...)...)
}

// startPairFromHere runs tuios pair --json with exactly the flags given.
func startPairFromHere(t *testing.T, base string, stdin io.Reader, env []string, args ...string) *pairRun {
	t.Helper()
	cmd := exec.Command(tuiosBin, append([]string{"pair", "--json"}, args...)...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	run := &pairRun{cmd: cmd, lines: make(chan string, 16), stderr: &lockedBuf{}, done: make(chan error, 1)}
	cmd.Stderr = run.stderr
	cmd.Stdin = stdin
	if err := cmd.Start(); err != nil {
		t.Fatalf("start tuios pair: %v", err)
	}
	t.Cleanup(func() {
		select {
		case <-run.done:
		default:
			_ = cmd.Process.Kill()
			<-run.done
		}
	})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			run.lines <- sc.Text()
		}
		close(run.lines)
		run.done <- cmd.Wait()
	}()
	line := run.next(t, 15*time.Second)
	if err := json.Unmarshal([]byte(line), &run.first); err != nil {
		t.Fatalf("tuios pair --json printed %q first, not JSON: %v\nstderr:\n%s", line, err, run.stderr)
	}
	return run
}

// next is the next stdout line.
func (r *pairRun) next(t *testing.T, within time.Duration) string {
	t.Helper()
	select {
	case line, ok := <-r.lines:
		if !ok {
			t.Fatalf("tuios pair ended without the line expected\nstderr:\n%s", r.stderr)
		}
		return line
	case <-time.After(within):
		t.Fatalf("tuios pair printed nothing in %s\nstderr:\n%s", within, r.stderr)
	}
	return ""
}

// wait waits for the process to end and returns the last JSON line and the
// exit error.
func (r *pairRun) wait(t *testing.T, within time.Duration) (map[string]any, error) {
	t.Helper()
	var last map[string]any
	deadline := time.After(within)
	for {
		select {
		case line, ok := <-r.lines:
			if !ok {
				err := <-r.done
				r.done <- err
				return last, err
			}
			last = nil
			if err := json.Unmarshal([]byte(line), &last); err != nil {
				t.Fatalf("tuios pair printed %q, not JSON", line)
			}
		case <-deadline:
			t.Fatalf("tuios pair did not end in %s\nstderr:\n%s", within, r.stderr)
		}
	}
}

// phone reads the link the way the phone does.
func (r *pairRun) phone(t *testing.T) pairPhone {
	t.Helper()
	raw, _ := r.first["url"].(string)
	link, err := url.Parse(raw)
	if err != nil || link.Scheme != "tuios" || link.Host != "pair" {
		t.Fatalf("the link %q is not a tuios://pair URL: %v", raw, err)
	}
	token, err := base64.RawURLEncoding.DecodeString(link.Query().Get("t"))
	if err != nil || len(token) != 16 {
		t.Fatalf("the token %q is not 128 bits of base64url: %v", link.Query().Get("t"), err)
	}
	p := strings.Split(link.Query().Get("p"), ",")
	return pairPhone{token: token, addr: p[0], link: link}
}

// pairLabel is the protocol label every MAC message starts with. The
// messages are built from it in pairMessage, one line per part.
const pairLabel = "tuios-pair-v1"

// pairMessage joins a MAC message: the label with its suffix, then each part,
// one per line, with no line end at the end.
func pairMessage(suffix string, parts ...string) string {
	return strings.Join(append([]string{pairLabel + suffix}, parts...), "\n")
}

// pairMACFor is the MAC the phone sends, or the one the reply carries.
func pairMACFor(token []byte, msg string) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// pairBody is a request body with a MAC made with token over device and key.
func pairBody(token []byte, device, key string) []byte {
	b, _ := json.Marshal(map[string]string{
		"device": device,
		"key":    key,
		"mac":    pairMACFor(token, pairMessage("", device, key)),
	})
	return b
}

// pairCheckFor is the check code both screens show, computed here the way
// the docs give it, so the test does not borrow tuios's arithmetic.
func pairCheckFor(token []byte, key string) string {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(pairMessage("-check", key)))
	sum := m.Sum(nil)
	n := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
	return fmt.Sprintf("%06d", n%1000000)
}

// pairReplyT is the listener's answer.
type pairReplyT struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Device  string `json:"device"`
	User    string `json:"user"`
	Command string `json:"command"`
	HostKey string `json:"host_key"`
	MAC     string `json:"mac"`
	Check   string `json:"check"`
}

// post sends one request. A listener that is gone is an error.
func post(addr, method, path string, body []byte) (int, pairReplyT, error) {
	return postWithin(addr, method, path, body, 10*time.Second)
}

// postWithin is post for a phone that waits up to within for the answer.
func postWithin(addr, method, path string, body []byte, within time.Duration) (int, pairReplyT, error) {
	req, err := http.NewRequest(method, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		return 0, pairReplyT{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: within}
	resp, err := client.Do(req)
	if err != nil {
		return 0, pairReplyT{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var reply pairReplyT
	if err := json.Unmarshal(raw, &reply); err != nil {
		return resp.StatusCode, reply, fmt.Errorf("reply %q is not JSON: %w", raw, err)
	}
	return resp.StatusCode, reply, nil
}

// transcript collects what the phone sent and got, for the artifact.
type transcript struct{ b strings.Builder }

func (tr *transcript) add(format string, args ...any) { fmt.Fprintf(&tr.b, format+"\n", args...) }

func (tr *transcript) save(t *testing.T, name string) {
	t.Helper()
	path := filepath.Join(artifactDir(t), name)
	if err := os.WriteFile(path, []byte(tr.b.String()), 0o644); err != nil {
		t.Errorf("save %s: %v", path, err)
	}
	t.Logf("transcript: %s", path)
}

// TestPairAddsAPhoneKey is the whole exchange against one tuios pair: every
// refusal first, while the code is still good, then the request that pairs,
// then the same code again.
func TestPairAddsAPhoneKey(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	_, hostLine := keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine := keygen(t, keys, "phone", "-t", "ecdsa", "-b", "256")
	_, otherLine := keygen(t, keys, "other", "-t", "ed25519")
	_, weakLine := keygen(t, keys, "weak", "-t", "rsa", "-b", "2048")
	akDir := filepath.Join(base, "sshdir")
	ak := filepath.Join(akDir, "authorized_keys")
	me, _ := user.Current()

	run := startPair(t, base, nil,
		"--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222",
		"--allow", "list,mail", "--timeout", "2m")
	ph := run.phone(t)
	var tr transcript
	tr.add("link: %s", ph.link)

	q := ph.link.Query()
	if q.Get("v") != "1" || q.Get("u") != me.Username || q.Get("s") != "127.0.0.1:2222" || q.Get("m") == "" {
		t.Errorf("ASSERTION: the link fields are wrong: %s", ph.link)
	}
	listen, _ := run.first["listen"].([]any)
	if len(listen) != 1 || q.Get("p") != listen[0] {
		t.Errorf("ASSERTION: p=%q is not the listener %v", q.Get("p"), listen)
	}
	if want := keygenFP(t, filepath.Join(keys, "host.pub")); q.Get("fp") != want {
		t.Errorf("ASSERTION: fp=%q, ssh-keygen -lf says %q", q.Get("fp"), want)
	}

	// Refusals of a request whose MAC is good. They do not count toward the
	// limit of wrong requests, so they all go to this one listener. The
	// requests that do count are in TestPairRefusesWrongRequests.
	refusals := []struct {
		name   string
		body   []byte
		status int
	}{
		{"bad device name", pairBody(ph.token, "../evil", phoneLine), http.StatusBadRequest},
		{"device name with a quote", pairBody(ph.token, `x",permitopen="*`, phoneLine), http.StatusBadRequest},
		{"weak key", pairBody(ph.token, "phone", weakLine), http.StatusBadRequest},
		{"two keys", pairBody(ph.token, "phone", phoneLine+"\n"+otherLine), http.StatusBadRequest},
		// The ssh parser skips a line it cannot read, so this reads as one key.
		{"a line before the key", pairBody(ph.token, "phone", "not a key\n"+phoneLine), http.StatusBadRequest},
		{"a terminal escape in the name", pairBody(ph.token, "phone\x1b]0;owned\x07", phoneLine), http.StatusBadRequest},
	}
	for _, c := range refusals {
		status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", c.body)
		tr.add("%s: %d %+v %v", c.name, status, reply, err)
		if err != nil || status != c.status || reply.OK || reply.Error == "" {
			t.Errorf("ASSERTION: %s: status %d, reply %+v, err %v; want %d and ok false with an error", c.name, status, reply, err, c.status)
		}
		if _, err := os.Stat(ak); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ASSERTION: %s: authorized_keys was written", c.name)
		}
	}

	status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
	tr.add("pair: %d %+v %v", status, reply, err)
	if err != nil || status != http.StatusOK || !reply.OK {
		t.Fatalf("ASSERTION: the good request was refused: %d %+v %v\nstderr:\n%s", status, reply, err, run.stderr)
	}
	wantCommand := tuiosBin + " stdio-proxy --as phone"
	if reply.Device != "phone" || reply.User != me.Username || reply.Command != wantCommand {
		t.Errorf("ASSERTION: reply %+v, want device phone, user %s, command %q", reply, me.Username, wantCommand)
	}
	if reply.HostKey != keyBody(hostLine) {
		t.Errorf("ASSERTION: host_key %q is not the host key %q", reply.HostKey, keyBody(hostLine))
	}
	if fp := sha256FP(t, reply.HostKey); fp != q.Get("fp") {
		t.Errorf("ASSERTION: the host key in the reply has fingerprint %s, the code said %s", fp, q.Get("fp"))
	}
	if want := pairMACFor(ph.token, pairMessage("-ok", "phone", reply.HostKey)); reply.MAC != want {
		t.Errorf("ASSERTION: the reply MAC %q is not HMAC(token, ok message) %q", reply.MAC, want)
	}
	wantCheck := pairCheckFor(ph.token, phoneLine)
	if reply.Check != wantCheck {
		t.Errorf("ASSERTION: the reply check code %q is not %q", reply.Check, wantCheck)
	}

	// The same code again, with a key that is not in the file yet.
	status, again, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "tablet", otherLine))
	tr.add("reuse: %d %+v %v", status, again, err)
	if err == nil && again.OK {
		t.Errorf("ASSERTION: the code worked a second time: %+v", again)
	}

	last, exitErr := run.wait(t, 15*time.Second)
	tr.add("result: %v exit %v", last, exitErr)
	if exitErr != nil || last["ok"] != true || last["device"] != "phone" || last["config"] != "written" || last["check"] != wantCheck {
		t.Errorf("ASSERTION: the result line %v, exit %v", last, exitErr)
	}
	if _, _, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "tablet", otherLine)); err == nil {
		t.Errorf("ASSERTION: the listener still answers after pairing")
	}

	data, err := os.ReadFile(ak)
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	wantLine := `command="` + wantCommand + `",restrict ` + keyBody(phoneLine) + " tuios-pair:phone\n"
	if string(data) != wantLine {
		t.Errorf("ASSERTION: authorized_keys is\n%q\nwant\n%q", data, wantLine)
	}
	if fi, _ := os.Stat(ak); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("ASSERTION: authorized_keys mode is %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(akDir); fi == nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("ASSERTION: the folder mode is %v, want 0700", fi.Mode().Perm())
	}
	cfg, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml"))
	if !strings.Contains(string(cfg), "[hosts.phone]\nallow = [\"list\", \"mail\"]\n") {
		t.Errorf("ASSERTION: config.toml has no [hosts.phone] table with the allow list:\n%s", cfg)
	}
	tr.add("authorized_keys:\n%s", data)
	tr.add("config.toml:\n%s", cfg)
	tr.save(t, "pair-exchange.txt")
}

// TestPairRefusesWrongRequests sends each request that fails before or at
// the MAC check to its own tuios pair, so the limit of wrong requests does
// not end the run first. Each is refused and writes nothing, and the phone's
// good request after it still pairs: one wrong request does not spend the
// code.
func TestPairRefusesWrongRequests(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine := keygen(t, keys, "phone", "-t", "ecdsa", "-b", "256")
	_, otherLine := keygen(t, keys, "other", "-t", "ed25519")
	// A token the code does not hold: a MAC made with it is a MAC made by
	// someone who did not see the code.
	wrongToken := bytes.Repeat([]byte{7}, 16)
	cases := []struct {
		name   string
		method string
		path   string
		body   func(token []byte) []byte
		status int
	}{
		{"get", http.MethodGet, "/v1/pair", func([]byte) []byte { return nil }, http.StatusMethodNotAllowed},
		{"wrong path", http.MethodPost, "/v1/other", func(tok []byte) []byte { return pairBody(tok, "phone", phoneLine) }, http.StatusNotFound},
		// The MAC is good: only the cap refuses it.
		{"oversize body", http.MethodPost, "/v1/pair", func(tok []byte) []byte {
			return pairBody(tok, "phone", phoneLine+" "+strings.Repeat("x", 9000))
		}, http.StatusRequestEntityTooLarge},
		{"not json", http.MethodPost, "/v1/pair", func([]byte) []byte { return []byte("device=phone") }, http.StatusBadRequest},
		{"wrong mac", http.MethodPost, "/v1/pair", func([]byte) []byte { return pairBody(wrongToken, "phone", phoneLine) }, http.StatusForbidden},
		// The key a machine on the path would swap in, with the phone's MAC.
		{"swapped key", http.MethodPost, "/v1/pair", func(tok []byte) []byte {
			var m map[string]string
			_ = json.Unmarshal(pairBody(tok, "phone", phoneLine), &m)
			m["key"] = otherLine
			b, _ := json.Marshal(m)
			return b
		}, http.StatusForbidden},
	}
	var tr transcript
	for i, c := range cases {
		t.Run(strings.ReplaceAll(c.name, " ", "_"), func(t *testing.T) {
			ak := filepath.Join(base, fmt.Sprintf("ak%d", i))
			run := startPair(t, base, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
				"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m")
			ph := run.phone(t)
			status, reply, err := post(ph.addr, c.method, c.path, c.body(ph.token))
			tr.add("%s: %d %+v %v", c.name, status, reply, err)
			if err != nil || status != c.status || reply.OK || reply.Error == "" {
				t.Errorf("ASSERTION: status %d, reply %+v, err %v; want %d and ok false with an error", status, reply, err, c.status)
			}
			if _, err := os.Stat(ak); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("ASSERTION: authorized_keys was written")
			}
			// Each run writes [hosts.DEVICE] to the one config, so each
			// pairs a name of its own.
			status, reply, err = post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, fmt.Sprintf("phone%d", i), phoneLine))
			if err != nil || !reply.OK {
				t.Errorf("ASSERTION: the good request after one wrong one: %d %+v %v", status, reply, err)
			}
			data, _ := os.ReadFile(ak)
			if strings.Count(string(data), "\n") != 1 || !strings.Contains(string(data), keyBody(phoneLine)) {
				t.Errorf("ASSERTION: authorized_keys holds something other than the phone's key:\n%s", data)
			}
		})
	}
	tr.save(t, "pair-refusals.txt")
}

// TestPairListensOnlyWhereTheCodeSays: --listen on every interface is
// refused without --listen-all, and warned about with it. Without --listen,
// every listener is on an address the code names, never 0.0.0.0 or [::].
func TestPairListensOnlyWhereTheCodeSays(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	common := []string{"--yes", "--authorized-keys", filepath.Join(base, "ak"),
		"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1s"}

	// "0:0" is not an unspecified address as text, but it binds one where
	// the resolver reads "0" as 0.0.0.0. Go's own resolver, which CI's
	// runners use, does not, and there the bind fails before any check.
	zeroResolves := false
	if addrs, err := net.LookupHost("0"); err == nil && len(addrs) > 0 {
		zeroResolves = true
	} else {
		t.Logf("the resolver does not read \"0\" as an address (%v), so the 0:0 cases are left out", err)
	}
	everywhere := []string{"0.0.0.0:0", "[::]:0", ":0"}
	if zeroResolves {
		everywhere = append(everywhere, "0:0")
	}
	for _, addr := range everywhere {
		out, err := tuiosCLI(t, base, append([]string{"pair", "--listen", addr}, common...)...)
		if err == nil || !strings.Contains(out, "--listen-all") {
			t.Errorf("ASSERTION: --listen %s without --listen-all: err %v\n%s", addr, err, out)
		}
	}

	// A public address: the listener is plain HTTP, and the internet has no
	// reason to reach it. The address is not on this machine, so only a check
	// made before the bind names --listen-all.
	for _, addr := range []string{"8.8.8.8:0", "[2001:4860:4860::8888]:0"} {
		out, err := tuiosCLI(t, base, append([]string{"pair", "--listen", addr}, common...)...)
		if err == nil || !strings.Contains(out, "--listen-all") || !strings.Contains(out, "public address") {
			t.Errorf("ASSERTION: --listen %s without --listen-all: err %v\n%s", addr, err, out)
		}
	}

	withAll := []string{"0.0.0.0:0"}
	if zeroResolves {
		withAll = append(withAll, "0:0")
	}
	for _, addr := range withAll {
		run := startPair(t, base, nil, append([]string{"--listen", addr, "--listen-all"}, common...)...)
		_, _ = run.wait(t, 15*time.Second)
		if !strings.Contains(run.stderr.String(), "Warning: the pairing listener listens on every interface") {
			t.Errorf("ASSERTION: --listen %s --listen-all gave no warning:\n%s", addr, run.stderr)
		}
	}

	// The default: the addresses tuios finds. On a machine with none, the
	// command says so, which is also not a listener everywhere.
	run := startPair(t, base, nil, common...)
	listen, _ := run.first["listen"].([]any)
	pairs := strings.Split(run.phone(t).link.Query().Get("p"), ",")
	if len(listen) == 0 || len(listen) != len(pairs) {
		t.Fatalf("ASSERTION: listeners %v, code p=%v", listen, pairs)
	}
	for i, l := range listen {
		host, port, _ := net.SplitHostPort(fmt.Sprint(l))
		if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
			t.Errorf("ASSERTION: a listener on %v", l)
		}
		_, pport, _ := net.SplitHostPort(pairs[i])
		if port != pport {
			t.Errorf("ASSERTION: listener %v and code address %s differ in port", l, pairs[i])
		}
	}
	t.Logf("listeners %v, code addresses %v", listen, pairs)
	_, _ = run.wait(t, 15*time.Second)
}

// TestPairAsksBeforeItAddsTheKey runs without --yes. The terminal shows the
// device name and the key fingerprint and asks. "n" adds nothing and ends
// the pairing. "y" adds the key.
func TestPairAsksBeforeItAddsTheKey(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine := keygen(t, keys, "phone", "-t", "ecdsa", "-b", "256")
	fp := keygenFP(t, filepath.Join(keys, "phone.pub"))
	for _, answer := range []string{"n", "y"} {
		t.Run(answer, func(t *testing.T) {
			ak := filepath.Join(base, "ak-"+answer)
			run := startPairInput(t, base, answer+"\n", nil, "--authorized-keys", ak, "--listen", "127.0.0.1:0",
				"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m")
			ph := run.phone(t)
			_, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "pixel-9", phoneLine))
			last, exitErr := run.wait(t, 15*time.Second)
			prompt := run.stderr.String()
			if !strings.Contains(prompt, "Name:  pixel-9") || !strings.Contains(prompt, fp) || !strings.Contains(prompt, "[y/N]") {
				t.Errorf("ASSERTION: the question does not show the name and the fingerprint %s:\n%s", fp, prompt)
			}
			// The check code the phone computes for itself, and the policy
			// the device gets without --allow.
			if check := pairCheckFor(ph.token, phoneLine); !strings.Contains(prompt, "Check: "+check) {
				t.Errorf("ASSERTION: the question does not show the check code %s:\n%s", check, prompt)
			}
			if !strings.Contains(prompt, "Allow: list, mail\n") {
				t.Errorf("ASSERTION: the question does not show the allow list list, mail:\n%s", prompt)
			}
			_ = os.WriteFile(filepath.Join(artifactDir(t), "prompt.txt"), []byte(prompt), 0o644)
			data, _ := os.ReadFile(ak)
			cfg, _ := os.ReadFile(filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml"))
			if answer == "n" {
				if err != nil || reply.OK || exitErr == nil || last["ok"] != false || len(data) != 0 || strings.Contains(string(cfg), "pixel-9") {
					t.Errorf("ASSERTION: after n: reply %+v %v, result %v exit %v, authorized_keys %q, config %q", reply, err, last, exitErr, data, cfg)
				}
				return
			}
			if err != nil || !reply.OK || exitErr != nil || !strings.Contains(string(data), keyBody(phoneLine)) {
				t.Errorf("ASSERTION: after y: reply %+v %v, exit %v, authorized_keys %q", reply, err, exitErr, data)
			}
			if !strings.Contains(string(cfg), "[hosts.pixel-9]\nallow = [\"list\", \"mail\"]\n") {
				t.Errorf("ASSERTION: without --allow, config.toml has no [hosts.pixel-9] table with list and mail:\n%s", cfg)
			}
		})
	}
}

// TestPairCodeExpires: a code nobody uses stops working at its time, and the
// command says so and exits with an error.
func TestPairCodeExpires(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine := keygen(t, keys, "phone", "-t", "ed25519")
	ak := filepath.Join(base, "authorized_keys")

	run := startPair(t, base, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222", "--timeout", "2s")
	ph := run.phone(t)
	last, exitErr := run.wait(t, 15*time.Second)
	if exitErr == nil || last["ok"] != false || !strings.Contains(fmt.Sprint(last["error"]), "time ran out") {
		t.Errorf("ASSERTION: an unused code ended with %v, exit %v; want ok false, the time ran out, and an error exit", last, exitErr)
	}
	if status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine)); err == nil {
		t.Errorf("ASSERTION: an expired code was answered: %d %+v", status, reply)
	}
	if _, err := os.Stat(ak); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ASSERTION: authorized_keys was written after the code expired")
	}
}

// TestPairStopsAfterTooManyWrongRequests: three requests with a wrong MAC end
// the pairing, so a good one after them is not answered.
func TestPairStopsAfterTooManyWrongRequests(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine := keygen(t, keys, "phone", "-t", "ed25519")
	ak := filepath.Join(base, "authorized_keys")

	run := startPair(t, base, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m")
	ph := run.phone(t)
	wrong := bytes.Repeat([]byte{9}, 16)
	for i := 0; i < 3; i++ {
		if _, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(wrong, "phone", phoneLine)); err == nil && reply.OK {
			t.Fatalf("ASSERTION: a wrong MAC was accepted")
		}
	}
	if _, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine)); err == nil && reply.OK {
		t.Errorf("ASSERTION: a good request after three wrong ones paired")
	}
	last, exitErr := run.wait(t, 15*time.Second)
	if exitErr == nil || last["ok"] != false {
		t.Errorf("ASSERTION: the pairing did not stop with an error: %v, exit %v", last, exitErr)
	}
	if _, err := os.Stat(ak); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ASSERTION: authorized_keys was written")
	}
}

// TestPairLeavesWhatIsThereAlone: a key that is in authorized_keys already is
// refused, and so is a device name that has a [hosts] table, in another case.
// Neither refusal changes a file or spends the code. The file's last line has
// no newline, which the new line must not join.
func TestPairLeavesWhatIsThereAlone(t *testing.T) {
	needTools(t, "ssh-keygen")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine := keygen(t, keys, "phone", "-t", "ecdsa", "-b", "256")
	_, newLine := keygen(t, keys, "new", "-t", "ed25519")
	ak := filepath.Join(base, "authorized_keys")
	before := "# mine\n" + `from="10.0.0.0/8" ` + keyBody(phoneLine) + " old-phone"
	if err := os.WriteFile(ak, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(cfgDir)
	cfgBefore := "# hand written\n[hosts.Phone]\nallow = [\"list\", \"mail\", \"open\", \"write\"]\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBefore), 0o600); err != nil {
		t.Fatal(err)
	}

	run := startPair(t, base, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "127.0.0.1:2222",
		"--allow", "list", "--timeout", "1m")
	ph := run.phone(t)

	status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", newLine))
	if err != nil || status != http.StatusConflict || reply.OK {
		t.Errorf("ASSERTION: the name of a [hosts.Phone] table: %d %+v %v; want 409 and ok false", status, reply, err)
	}
	status, reply, err = post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "tablet", phoneLine))
	if err != nil || status != http.StatusConflict || reply.OK {
		t.Errorf("ASSERTION: a key in the file already: %d %+v %v; want 409 and ok false", status, reply, err)
	}
	if data, _ := os.ReadFile(ak); string(data) != before {
		t.Fatalf("ASSERTION: a refused request changed authorized_keys:\n%s", data)
	}
	if data, _ := os.ReadFile(filepath.Join(cfgDir, "config.toml")); string(data) != cfgBefore {
		t.Fatalf("ASSERTION: a refused request changed config.toml:\n%s", data)
	}

	status, reply, err = post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "tablet", newLine))
	if err != nil || status != http.StatusOK || !reply.OK {
		t.Fatalf("ASSERTION: a new key and name after two refused ones: %d %+v %v\nstderr:\n%s", status, reply, err, run.stderr)
	}
	last, exitErr := run.wait(t, 15*time.Second)
	if exitErr != nil || last["config"] != "written" {
		t.Errorf("ASSERTION: the result %v, exit %v; want config written", last, exitErr)
	}
	wantCfg := cfgBefore + "\n[hosts.tablet]\nallow = [\"list\"]\n"
	if data, _ := os.ReadFile(filepath.Join(cfgDir, "config.toml")); string(data) != wantCfg {
		t.Errorf("ASSERTION: config.toml is\n%q\nwant\n%q", data, wantCfg)
	}
	data, _ := os.ReadFile(ak)
	want := before + "\n" + `command="` + tuiosBin + ` stdio-proxy --as tablet",restrict ` + keyBody(newLine) + " tuios-pair:tablet\n"
	if string(data) != want {
		t.Errorf("ASSERTION: authorized_keys is\n%q\nwant\n%q", data, want)
	}
}

// qrModules reads the QR code tuios pair drew back into modules. Colour
// output paints each cell with ▀, the top module in the foreground and the
// bottom one in the background. Plain output draws the light modules.
func qrModules(t *testing.T, out string, colour bool) [][]bool {
	t.Helper()
	cell := regexp.MustCompile(`\x1b\[(\d+);(\d+)m▀`)
	var grid [][]bool
	for _, line := range strings.Split(out, "\n") {
		var top, bottom []bool
		if colour {
			cells := cell.FindAllStringSubmatch(line, -1)
			if len(cells) < 21 {
				continue
			}
			for _, c := range cells {
				top = append(top, c[1] == "30")
				bottom = append(bottom, c[2] == "40")
			}
		} else {
			if !strings.HasPrefix(line, "██") {
				continue
			}
			for _, r := range line {
				switch r {
				case '█':
					top, bottom = append(top, false), append(bottom, false)
				case '▀':
					top, bottom = append(top, false), append(bottom, true)
				case '▄':
					top, bottom = append(top, true), append(bottom, false)
				case ' ':
					top, bottom = append(top, true), append(bottom, true)
				}
			}
		}
		grid = append(grid, top, bottom)
	}
	if len(grid) == 0 {
		t.Fatalf("no QR code in the output:\n%s", out)
	}
	return grid
}

// qrPNG writes the modules as a PNG with a wide quiet zone, for a decoder.
func qrPNG(t *testing.T, grid [][]bool, path string) {
	t.Helper()
	const scale, margin = 8, 4
	w, h := len(grid[0]), len(grid)
	img := image.NewGray(image.Rect(0, 0, (w+2*margin)*scale, (h+2*margin)*scale))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	for y, row := range grid {
		for x, dark := range row {
			if !dark {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray((x+margin)*scale+dx, (y+margin)*scale+dy, color.Gray{})
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestPairQRCodeHoldsTheLink decodes the code tuios pair draws, in colour and
// without, with zbarimg, and compares it with the link printed under it.
func TestPairQRCodeHoldsTheLink(t *testing.T) {
	needTools(t, "ssh-keygen", "zbarimg")
	base := t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	dir := artifactDir(t)
	for _, colour := range []bool{true, false} {
		name := map[bool]string{true: "colour", false: "plain"}[colour]
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(tuiosBin, "pair", "--authorized-keys", filepath.Join(base, "ak"), "--listen", "127.0.0.1:0",
				"--host-key", filepath.Join(keys, "host.pub"), "--advertise-ssh", "studio.example.ts.net:2222", "--timeout", "1s")
			cmd.Env = os.Environ()
			for _, key := range xdgKeys {
				cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
			}
			if !colour {
				cmd.Env = append(cmd.Env, "NO_COLOR=1")
			}
			out, _ := cmd.Output()
			var link string
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "tuios://pair?") {
					link = line
				}
			}
			if link == "" {
				t.Fatalf("no link in the output:\n%s", out)
			}
			grid := qrModules(t, string(out), colour)
			pngPath := filepath.Join(dir, "pair-qr-"+name+".png")
			qrPNG(t, grid, pngPath)
			_ = os.WriteFile(filepath.Join(dir, "pair-output-"+name+".txt"), out, 0o644)
			got, err := exec.Command("zbarimg", "--quiet", "--raw", pngPath).Output()
			if err != nil {
				t.Fatalf("ASSERTION: zbarimg cannot read the code tuios drew: %v (%s)", err, pngPath)
			}
			if strings.TrimSpace(string(got)) != link {
				t.Errorf("ASSERTION: the code holds\n%s\nthe text says\n%s", got, link)
			}
		})
	}
}

// startScratchSSHD runs an sshd of this test's own: its own host key, port and
// authorized keys file, run as this user. The sessions it starts get the XDG
// directories of remote, so stdio-proxy reaches the scratch daemon and never
// the person's own.
func startScratchSSHD(t *testing.T, dir, hostKey, ak, port, remote string) string {
	t.Helper()
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/sshd"} {
			if _, err := os.Stat(p); err == nil {
				sshd = p
			}
		}
	}
	if sshd == "" {
		t.Skip("sshd is not installed")
	}
	me, _ := user.Current()
	var env []string
	for _, key := range xdgKeys {
		if key != "HOME" {
			env = append(env, key+"="+xdgDir(remote, key))
		}
	}
	cfg := strings.Join([]string{
		"Port " + port,
		"ListenAddress 127.0.0.1",
		"HostKey " + hostKey,
		"PidFile " + filepath.Join(dir, "sshd.pid"),
		"AuthorizedKeysFile " + ak,
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"PubkeyAuthentication yes",
		"UsePAM no",
		"StrictModes no",
		"AllowUsers " + me.Username,
		"PermitUserEnvironment no",
		"SetEnv " + strings.Join(env, " "),
		"LogLevel VERBOSE",
		"",
	}, "\n")
	cfgPath := filepath.Join(dir, "sshd_config")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "sshd.log")
	cmd := exec.Command(sshd, "-D", "-f", cfgPath, "-E", logPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sshd: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		data, _ := os.ReadFile(logPath)
		_ = os.WriteFile(filepath.Join(artifactDir(t), "sshd.log"), data, 0o644)
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
		if err == nil {
			_ = c.Close()
			return logPath
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(logPath)
			t.Fatalf("sshd never listened on %s:\n%s", port, data)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestPairedKeyOpensOnlyTheLink proves the installed line in a real sshd.
// The phone pairs with --allow list, so the device's table allows listing
// only. Its key then logs in, asks for a shell command and gets the link,
// cannot forward a port, and carries a link from another daemon on which a
// listing works and typing is refused: the name the forced command pins is
// the one the policy is resolved for, whatever the dialling machine calls
// itself.
func TestPairedKeyOpensOnlyTheLink(t *testing.T) {
	needTools(t, "ssh", "ssh-keygen")
	base := t.TempDir()
	remote := remoteMachine(t)
	dir := filepath.Join(base, "sshd")
	mustMkdir(dir)
	hostKey, _ := keygen(t, dir, "host", "-t", "ed25519")
	phoneKey, phoneLine := keygen(t, dir, "phone", "-t", "ecdsa", "-b", "256")
	ak := filepath.Join(dir, "authorized_keys")
	port := strconv.Itoa(freePort(t))
	me, _ := user.Current()

	// Pair against the far machine's config, as the person there would.
	run := startPair(t, remote, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", hostKey+".pub", "--advertise-ssh", "127.0.0.1:"+port, "--allow", "list", "--timeout", "1m")
	ph := run.phone(t)
	_, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
	if err != nil || !reply.OK {
		t.Fatalf("pairing failed: %+v %v\nstderr:\n%s", reply, err, run.stderr)
	}
	if _, err := run.wait(t, 15*time.Second); err != nil {
		t.Fatalf("tuios pair exit: %v\nstderr:\n%s", err, run.stderr)
	}
	// The phone pins the host key from the reply, which it checked against
	// the fingerprint in the code.
	if sha256FP(t, reply.HostKey) != ph.link.Query().Get("fp") {
		t.Fatalf("the reply host key does not match the code")
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte("[127.0.0.1]:"+port+" "+reply.HostKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	startScratchSSHD(t, dir, hostKey, ak, port, remote)

	// ssh that reads nothing of the person's: no config, no agent, no known
	// hosts but the pinned key.
	sshWrap := filepath.Join(dir, "ssh")
	wrap := "#!/bin/sh\nexec ssh -F /dev/null -p " + port +
		" -o UserKnownHostsFile=" + knownHosts + " -o GlobalKnownHostsFile=/dev/null -o StrictHostKeyChecking=yes" +
		" -o IdentityAgent=none -o IdentitiesOnly=yes -o BatchMode=yes -o ControlMaster=no -o ControlPath=none -i " + phoneKey + " \"$@\"\n"
	if err := os.WriteFile(sshWrap, []byte(wrap), 0o700); err != nil {
		t.Fatal(err)
	}
	var tr transcript

	// A command of the client's own choosing runs the link instead.
	cmd := exec.Command(sshWrap, "-T", me.Username+"@127.0.0.1", "echo NOT-FORCED")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	var sshErr lockedBuf
	cmd.Stderr = &sshErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		first <- line
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case line := <-first:
		tr.add("ssh 'echo NOT-FORCED' printed first: %q", line)
		if strings.TrimSpace(line) != "TUIOS-LINK 1" {
			t.Errorf("ASSERTION: the key ran %q, not the link\nssh: %s", line, sshErr.String())
		}
	case <-time.After(20 * time.Second):
		t.Errorf("ASSERTION: no link preamble over ssh\nssh: %s", sshErr.String())
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// restrict: no port forwarding.
	fwd := exec.Command(sshWrap, "-N", "-o", "ExitOnForwardFailure=yes", "-R", "0:127.0.0.1:9", me.Username+"@127.0.0.1")
	fwdDone := make(chan error, 1)
	var fwdOut []byte
	go func() {
		var err error
		fwdOut, err = fwd.CombinedOutput()
		fwdDone <- err
	}()
	select {
	case err := <-fwdDone:
		tr.add("ssh -R: %v %s", err, fwdOut)
		if err == nil {
			t.Errorf("ASSERTION: a port forward was allowed")
		}
	case <-time.After(20 * time.Second):
		_ = fwd.Process.Kill()
		<-fwdDone
		t.Errorf("ASSERTION: ssh -R kept running, so the forward was allowed")
	}

	// A daemon here dials the far one through the same sshd and key.
	cfgDir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(cfgDir)
	hubCfg := "[hosts.studio]\naddr = \"" + me.Username + "@127.0.0.1\"\nconnect_timeout = 10\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(hubCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TUIOS_SSH=" + sshWrap}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	listing := waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "studio") && strings.Contains(s, "up")
	}, "the hub never reported studio up through sshd")
	tr.add("tuios hosts:\n%s", listing)

	out, err := tuiosCLIEnv(t, base, env, "list-windows", "-s", "studio:far")
	tr.add("list-windows -s studio:far: %v\n%s", err, out)
	if err != nil {
		t.Errorf("ASSERTION: a listing over the paired key was refused: %v\n%s", err, out)
	}
	out, err = tuiosCLIEnv(t, base, env, "send-text", "-s", "studio:far", "echo should-not-run")
	tr.add("send-text -s studio:far: %v\n%s", err, out)
	if err == nil {
		t.Errorf("ASSERTION: typing went through, so the policy of phone did not apply:\n%s", out)
	} else if flat := strings.Join(strings.Fields(out), " "); !contains(flat, "(write) on") {
		t.Errorf("the refusal does not name what is missing:\n%s", out)
	}
	data, _ := os.ReadFile(ak)
	tr.add("authorized_keys:\n%s", data)
	tr.save(t, "pair-sshd.txt")
}

// pairSetup makes a scratch base with a host key and an Ed25519 phone key.
// It returns the base, the host key path and the phone key line.
func pairSetup(t *testing.T) (base, hostPub, phoneLine string) {
	t.Helper()
	needTools(t, "ssh-keygen")
	base = t.TempDir()
	keys := filepath.Join(base, "keys")
	mustMkdir(keys)
	keygen(t, keys, "host", "-t", "ed25519")
	_, phoneLine = keygen(t, keys, "phone", "-t", "ed25519")
	return base, filepath.Join(keys, "host.pub"), phoneLine
}

// TestPairReplyReachesAPhoneAfterASlowAnswer: the person takes longer than
// one connection's deadline to compare the fingerprint and type y. The phone
// must still get the reply with the host key and its MAC. Without it the key
// is added, the terminal says paired, and the phone has no host key to pin.
func TestPairReplyReachesAPhoneAfterASlowAnswer(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	ak := filepath.Join(base, "authorized_keys")
	// An *os.File, so exec does not copy it and Wait does not wait for it.
	answer, typing, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer typing.Close()
	defer answer.Close()
	run := startPairStdin(t, base, answer, nil, "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", hostPub, "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m")
	ph := run.phone(t)
	type result struct {
		status int
		reply  pairReplyT
		err    error
	}
	got := make(chan result, 1)
	go func() {
		status, reply, err := postWithin(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine), 40*time.Second)
		got <- result{status, reply, err}
	}()
	// Longer than the 10 second deadline of a connection.
	time.Sleep(13 * time.Second)
	if _, err := io.WriteString(typing, "y\n"); err != nil {
		t.Fatalf("type y: %v", err)
	}
	r := <-got
	last, exitErr := run.wait(t, 20*time.Second)
	_ = os.WriteFile(filepath.Join(artifactDir(t), "slow-answer.txt"),
		fmt.Appendf(nil, "status %d\nreply %+v\nerr %v\nresult %v\nexit %v\nstderr:\n%s", r.status, r.reply, r.err, last, exitErr, run.stderr), 0o644)
	if r.err != nil || !r.reply.OK {
		t.Fatalf("ASSERTION: the phone got no reply after a slow y: %d %+v %v\nstderr:\n%s", r.status, r.reply, r.err, run.stderr)
	}
	if want := pairMACFor(ph.token, pairMessage("-ok", "phone", r.reply.HostKey)); r.reply.MAC != want || sha256FP(t, r.reply.HostKey) != ph.link.Query().Get("fp") {
		t.Errorf("ASSERTION: the late reply does not carry the host key and its MAC: %+v", r.reply)
	}
	if exitErr != nil || last["ok"] != true {
		t.Errorf("ASSERTION: tuios pair ended with %v, exit %v", last, exitErr)
	}
}

// TestPairStopDuringTheQuestionWritesNothing: the person presses Ctrl+C
// while the question is open, and tuios says pairing is stopped. A y typed
// after that must not add the key, or the file changes after tuios said it
// would not.
func TestPairStopDuringTheQuestionWritesNothing(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	ak := filepath.Join(base, "authorized_keys")
	// An *os.File, so exec does not copy it and Wait does not wait for it.
	answer, typing, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer typing.Close()
	defer answer.Close()
	run := startPairStdin(t, base, answer, nil, "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", hostPub, "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m")
	ph := run.phone(t)
	type result struct {
		reply pairReplyT
		err   error
	}
	got := make(chan result, 1)
	go func() {
		_, reply, err := postWithin(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine), 30*time.Second)
		got <- result{reply, err}
	}()
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(run.stderr.String(), "[y/N]") {
		if time.Now().After(deadline) {
			t.Fatalf("the question never showed:\n%s", run.stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := run.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	_, _ = io.WriteString(typing, "y\n")
	last, exitErr := run.wait(t, 20*time.Second)
	r := <-got
	data, _ := os.ReadFile(ak)
	_ = os.WriteFile(filepath.Join(artifactDir(t), "stop-during-question.txt"),
		fmt.Appendf(nil, "reply %+v err %v\nresult %v exit %v\nauthorized_keys %q\nstderr:\n%s", r.reply, r.err, last, exitErr, data, run.stderr), 0o644)
	if len(data) != 0 {
		t.Errorf("ASSERTION: a y after Ctrl+C added the key, and tuios said %v:\n%s", last, data)
	}
	if r.err == nil && r.reply.OK {
		t.Errorf("ASSERTION: the phone was told it paired after Ctrl+C: %+v", r.reply)
	}
	if exitErr == nil || last["ok"] != false {
		t.Errorf("ASSERTION: tuios pair ended with %v, exit %v; want a stop", last, exitErr)
	}
}

// TestPairIgnoresConnectionsClosedUnused: a phone that races a connection to
// each address in the code closes the ones it does not use before it sends a
// byte. A port scan does the same. These are not wrong requests, so five of
// them must not stop the pairing before the phone's request arrives.
func TestPairIgnoresConnectionsClosedUnused(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	ak := filepath.Join(base, "authorized_keys")
	run := startPair(t, base, nil, "--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", hostPub, "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m")
	ph := run.phone(t)
	for i := 0; i < 5; i++ {
		c, err := net.DialTimeout("tcp", ph.addr, 5*time.Second)
		if err != nil {
			t.Fatalf("ASSERTION: connection %d was refused, so the listener stopped: %v\nstderr:\n%s", i+1, err, run.stderr)
		}
		_ = c.Close()
		// Longer than the pause after a wrong request, so a build that
		// counts these counts each one and does not answer some with 429.
		time.Sleep(400 * time.Millisecond)
	}
	status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
	if err != nil || !reply.OK {
		t.Errorf("ASSERTION: the phone could not pair after five unused connections: %d %+v %v\nstderr:\n%s", status, reply, err, run.stderr)
	}
	last, exitErr := run.wait(t, 15*time.Second)
	if exitErr != nil || last["ok"] != true {
		t.Errorf("ASSERTION: tuios pair ended with %v, exit %v", last, exitErr)
	}
}

// TestPairRefusesAKeysFileAnyoneCanWrite: sshd ignores an authorized keys
// file that anyone can write to, or that sits in a folder anyone can write
// to. Pairing into one ends with a key that does not log in, so tuios stops
// before it shows a code. A file the group can write gets a warning.
func TestPairRefusesAKeysFileAnyoneCanWrite(t *testing.T) {
	base, hostPub, _ := pairSetup(t)
	common := []string{"--yes", "--listen", "127.0.0.1:0", "--host-key", hostPub,
		"--advertise-ssh", "127.0.0.1:2222", "--timeout", "1s"}

	open := filepath.Join(base, "open")
	mustMkdir(open)
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(base, "ak-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o666); err != nil {
		t.Fatal(err)
	}
	for name, ak := range map[string]string{"file": file, "folder": filepath.Join(open, "authorized_keys")} {
		out, err := tuiosCLI(t, base, append([]string{"pair", "--authorized-keys", ak}, common...)...)
		if err == nil || !strings.Contains(out, "anyone can write") || strings.Contains(out, "tuios://pair") {
			t.Errorf("ASSERTION: a %s anyone can write was not refused before the code: err %v\n%s", name, err, out)
		}
	}

	group := filepath.Join(base, "ak-group")
	if err := os.WriteFile(group, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(group, 0o660); err != nil {
		t.Fatal(err)
	}
	run := startPair(t, base, nil, append([]string{"--authorized-keys", group}, common...)...)
	_, _ = run.wait(t, 15*time.Second)
	if !strings.Contains(run.stderr.String(), "Warning: the group can write to "+group) {
		t.Errorf("ASSERTION: a file the group can write gave no warning:\n%s", run.stderr)
	}
}

// TestPairMachineNameInTheCode: --machine sets the m field the phone shows,
// and a name that is not a machine name is refused before the code shows.
func TestPairMachineNameInTheCode(t *testing.T) {
	base, hostPub, _ := pairSetup(t)
	common := []string{"--yes", "--authorized-keys", filepath.Join(base, "ak"), "--listen", "127.0.0.1:0",
		"--host-key", hostPub, "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1s"}
	run := startPair(t, base, nil, append([]string{"--machine", "studio-demo"}, common...)...)
	if m := run.phone(t).link.Query().Get("m"); m != "studio-demo" {
		t.Errorf("ASSERTION: --machine studio-demo gave m=%q", m)
	}
	_, _ = run.wait(t, 15*time.Second)
	out, err := tuiosCLI(t, base, append([]string{"pair", "--machine", "bad name&t=x"}, common...)...)
	if err == nil || !strings.Contains(out, "--machine") || strings.Contains(out, "tuios://pair") {
		t.Errorf("ASSERTION: --machine with a space and & was not refused: err %v\n%s", err, out)
	}
}

// pairArgs are the flags every test run of tuios pair here shares.
func pairArgs(ak, hostPub string, more ...string) []string {
	return append([]string{"--yes", "--authorized-keys", ak, "--listen", "127.0.0.1:0",
		"--host-key", hostPub, "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m"}, more...)
}

// TestPairWritesThePolicyBeforeTheKey: when the [hosts.DEVICE] table cannot be
// written, the key is not added. A key without its table would get
// [hosts."*"] or the default policy, which can start processes and type into
// panes. The config folder and file are read-only, so the table write fails.
func TestPairWritesThePolicyBeforeTheKey(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	ak := filepath.Join(base, "authorized_keys")
	cfgDir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(cfgDir)
	cfg := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfg, []byte("# read-only\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfgDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cfgDir, 0o700) })
	if f, err := os.OpenFile(cfg, os.O_WRONLY, 0); err == nil {
		_ = f.Close()
		t.Skip("this user can write a read-only file (root?), so the write cannot be made to fail")
	}

	run := startPair(t, base, nil, pairArgs(ak, hostPub)...)
	ph := run.phone(t)
	status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
	last, exitErr := run.wait(t, 15*time.Second)
	data, _ := os.ReadFile(ak)
	_ = os.WriteFile(filepath.Join(artifactDir(t), "policy-first.txt"),
		fmt.Appendf(nil, "status %d reply %+v err %v\nresult %v exit %v\nauthorized_keys %q\nstderr:\n%s", status, reply, err, last, exitErr, data, run.stderr), 0o644)
	if len(data) != 0 {
		t.Errorf("ASSERTION: the key was added though its [hosts.phone] table was not written:\n%s", data)
	}
	if err != nil || reply.OK || status != http.StatusInternalServerError {
		t.Errorf("ASSERTION: the phone got %d %+v %v; want 500 and ok false", status, reply, err)
	}
	if exitErr == nil || last["ok"] != false || !strings.Contains(fmt.Sprint(last["error"]), "did not add the key") {
		t.Errorf("ASSERTION: tuios pair ended with %v, exit %v; want an error that says the key was not added", last, exitErr)
	}
}

// TestPairRefusesANameInUse: a device name that a [hosts] table has, that a
// pinned key in authorized_keys opens links as, or that is this machine's
// name, is refused in any case, and the code stays good. --name with such a
// name is refused before the code shows.
func TestPairRefusesANameInUse(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	keys := filepath.Join(base, "keys")
	_, oldLine := keygen(t, keys, "old", "-t", "ed25519")
	ak := filepath.Join(base, "authorized_keys")
	before := `command="/usr/bin/tuios stdio-proxy --as Tablet",restrict ` + keyBody(oldLine) + " tuios-pair:Tablet\n"
	if err := os.WriteFile(ak, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(cfgDir)
	cfgBefore := "[hosts.studio]\naddr = \"me@studio.invalid\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBefore), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Studio", "TABLET", "desk"} {
		out, err := tuiosCLI(t, base, append([]string{"pair", "--name", name, "--machine", "Desk"}, pairArgs(ak, hostPub)...)...)
		if err == nil || !strings.Contains(out, "another name") || strings.Contains(out, "tuios://pair") {
			t.Errorf("ASSERTION: --name %s was not refused before the code: err %v\n%s", name, err, out)
		}
	}

	// The runs above add the harness's pinned settings to the file.
	cfgBefore, _ = func() (string, error) {
		b, err := os.ReadFile(filepath.Join(cfgDir, "config.toml"))
		return string(b), err
	}()
	run := startPair(t, base, nil, pairArgs(ak, hostPub, "--machine", "Desk")...)
	ph := run.phone(t)
	var tr transcript
	for _, name := range []string{"STUDIO", "tablet", "DESK"} {
		status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, name, phoneLine))
		tr.add("%s: %d %+v %v", name, status, reply, err)
		if err != nil || status != http.StatusConflict || reply.OK {
			t.Errorf("ASSERTION: the name %s: %d %+v %v; want 409 and ok false", name, status, reply, err)
		}
	}
	if data, _ := os.ReadFile(ak); string(data) != before {
		t.Fatalf("ASSERTION: a refused name changed authorized_keys:\n%s", data)
	}
	if data, _ := os.ReadFile(filepath.Join(cfgDir, "config.toml")); string(data) != cfgBefore {
		t.Fatalf("ASSERTION: a refused name changed config.toml:\n%s", data)
	}
	status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
	tr.add("phone: %d %+v %v", status, reply, err)
	if err != nil || !reply.OK {
		t.Errorf("ASSERTION: a free name after the refused ones: %d %+v %v\nstderr:\n%s", status, reply, err, run.stderr)
	}
	_, _ = run.wait(t, 15*time.Second)
	tr.save(t, "pair-names.txt")
}

// TestPairRefusesRequestsFromThisMachine: a request with the code that comes
// from this machine, or from a machine in [hosts], is from a program that
// read the code, not from the phone. It is refused, the code is spent, and
// nothing is written. --accept-local lets this machine through and never a
// host.
func TestPairRefusesRequestsFromThisMachine(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)

	// An address of this machine that is not loopback, when it has one.
	var lan string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
				lan = ipn.IP.String()
				break
			}
		}
	}

	type source struct {
		name, listen string
		local        bool
		config       string
	}
	cases := []source{
		{name: "loopback", listen: "127.0.0.1:0"},
		{name: "host_in_config", listen: "127.0.0.1:0", local: true, config: "[hosts.studio]\naddr = \"me@127.0.0.1:22\"\n"},
	}
	if lan != "" {
		cases = append(cases, source{name: "own_lan_address", listen: net.JoinHostPort(lan, "0")})
	}
	var tr transcript
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub := filepath.Join(base, c.name)
			mustMkdir(sub)
			if c.config != "" {
				cfgDir := filepath.Join(xdgDir(sub, "XDG_CONFIG_HOME"), "tuios")
				mustMkdir(cfgDir)
				if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(c.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ak := filepath.Join(sub, "authorized_keys")
			args := []string{"--yes", "--authorized-keys", ak, "--listen", c.listen, "--listen-all",
				"--host-key", hostPub, "--advertise-ssh", "127.0.0.1:2222", "--timeout", "1m"}
			if c.local {
				args = append(args, "--accept-local")
			}
			run := startPairFromHere(t, sub, strings.NewReader(""), nil, args...)
			ph := run.phone(t)
			status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
			last, exitErr := run.wait(t, 15*time.Second)
			tr.add("%s: %d %+v %v\nresult %v exit %v", c.name, status, reply, err, last, exitErr)
			if err != nil || status != http.StatusForbidden || reply.OK {
				t.Errorf("ASSERTION: a request from %s got %d %+v %v; want 403", c.listen, status, reply, err)
			}
			if exitErr == nil || last["ok"] != false || !strings.Contains(fmt.Sprint(last["error"]), "may have read the code") {
				t.Errorf("ASSERTION: tuios pair ended with %v, exit %v; want a stop that says why", last, exitErr)
			}
			if _, err := os.Stat(ak); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("ASSERTION: authorized_keys was written")
			}
		})
	}
	tr.save(t, "pair-sources.txt")
}

// TestPairCountsOnlyPairingRequests: requests that are not pairing requests,
// such as a browser, a scanner or a probe sends, are refused and do not count
// toward the three wrong requests. After six of them the phone still pairs.
func TestPairCountsOnlyPairingRequests(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	ak := filepath.Join(base, "authorized_keys")
	run := startPair(t, base, nil, pairArgs(ak, hostPub)...)
	ph := run.phone(t)
	raw := func(text string) string {
		c, err := net.DialTimeout("tcp", ph.addr, 5*time.Second)
		if err != nil {
			return "dial: " + err.Error()
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(c, text)
		line, _ := bufio.NewReader(c).ReadString('\n')
		return strings.TrimSpace(line)
	}
	var tr transcript
	for _, c := range []struct {
		name, method, path string
		body               []byte
	}{
		{"get", http.MethodGet, "/", nil},
		{"wrong path", http.MethodPost, "/v1/other", pairBody(ph.token, "phone", phoneLine)},
		{"not json", http.MethodPost, "/v1/pair", []byte("device=phone")},
		{"no mac", http.MethodPost, "/v1/pair", []byte(`{"device":"phone","key":"x"}`)},
	} {
		status, reply, err := post(ph.addr, c.method, c.path, c.body)
		tr.add("%s: %d %+v %v", c.name, status, reply, err)
		if err != nil || reply.OK {
			t.Errorf("ASSERTION: %s: %d %+v %v", c.name, status, reply, err)
		}
	}
	for _, text := range []string{"hello\r\n\r\n", "POST /v1/pair HTTP/1.1\r\nHost: x\r\n\r\n"} {
		got := raw(text)
		tr.add("raw %q: %s", text, got)
		if !strings.HasPrefix(got, "HTTP/1.1 4") {
			t.Errorf("ASSERTION: raw %q got %q, want a 4xx", text, got)
		}
	}
	status, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone", phoneLine))
	tr.add("pair: %d %+v %v", status, reply, err)
	if err != nil || !reply.OK {
		t.Errorf("ASSERTION: the phone could not pair after six requests that are not pairing requests: %d %+v %v\nstderr:\n%s", status, reply, err, run.stderr)
	}
	_, _ = run.wait(t, 15*time.Second)
	tr.save(t, "pair-not-counted.txt")
}

// TestPairForcedCommandUsesTheTuiosOnPath: the forced command names tuios as
// PATH names it, a link that a package manager keeps current, not the file
// the link points to today, which an upgrade replaces. A tuios on PATH that
// is another binary is not used, and tuios says so.
func TestPairForcedCommandUsesTheTuiosOnPath(t *testing.T) {
	base, hostPub, phoneLine := pairSetup(t)
	bin := filepath.Join(base, "profile", "bin")
	mustMkdir(bin)
	if err := os.Symlink(tuiosBin, filepath.Join(bin, "tuios")); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other", "bin")
	mustMkdir(other)
	if err := os.WriteFile(filepath.Join(other, "tuios"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, path, want string }{
		{"link_on_path", bin, filepath.Join(bin, "tuios")},
		{"other_tuios_on_path", other, tuiosBin},
	} {
		t.Run(c.name, func(t *testing.T) {
			ak := filepath.Join(base, "ak-"+c.name)
			env := []string{"PATH=" + c.path + string(os.PathListSeparator) + os.Getenv("PATH")}
			run := startPair(t, base, env, pairArgs(ak, hostPub)...)
			ph := run.phone(t)
			_, reply, err := post(ph.addr, http.MethodPost, "/v1/pair", pairBody(ph.token, "phone-"+strings.ReplaceAll(c.name, "_", "-"), phoneLine))
			_, _ = run.wait(t, 15*time.Second)
			data, _ := os.ReadFile(ak)
			if err != nil || !reply.OK || !strings.HasPrefix(reply.Command, c.want+" stdio-proxy --as ") || !strings.HasPrefix(string(data), `command="`+c.want+" ") {
				t.Errorf("ASSERTION: with %s first on PATH the forced command is %q, want %s\nauthorized_keys %q\nstderr:\n%s", c.path, reply.Command, c.want, data, run.stderr)
			}
			warned := strings.Contains(run.stderr.String(), "Warning: tuios is not on PATH, or the tuios on PATH is another binary")
			if warned != (c.want == tuiosBin) {
				t.Errorf("ASSERTION: warning shown %v, want %v:\n%s", warned, c.want == tuiosBin, run.stderr)
			}
		})
	}
}

// TestPairChecksTheHomeFolder: sshd refuses keys under a home folder that
// anyone can write to, so tuios refuses it before the code shows, for the
// default ~/.ssh/authorized_keys. A home the group can write gets a warning.
func TestPairChecksTheHomeFolder(t *testing.T) {
	_, hostPub, _ := pairSetup(t)
	for _, c := range []struct {
		name string
		mode os.FileMode
	}{{"anyone", 0o777}, {"group", 0o770}} {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			home := xdgDir(base, "HOME")
			if err := os.Chmod(home, c.mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
			args := []string{"pair", "--yes", "--listen", "127.0.0.1:0", "--host-key", hostPub,
				"--advertise-ssh", "127.0.0.1:2222", "--timeout", "1s", "--accept-local"}
			out, err := tuiosCLI(t, base, args...)
			if c.name == "anyone" {
				if err == nil || !strings.Contains(out, "anyone can write to "+home) || strings.Contains(out, "tuios://pair") {
					t.Errorf("ASSERTION: a home anyone can write was not refused before the code: err %v\n%s", err, out)
				}
				return
			}
			if !strings.Contains(out, "Warning: the group can write to "+home) {
				t.Errorf("ASSERTION: a home the group can write gave no warning: err %v\n%s", err, out)
			}
		})
	}
}
