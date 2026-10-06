package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Tailscale SSH in front of a host, against the ssh stand-in of the sync
// tests.
//
// A wrapper goes in front of that stand-in and plays Tailscale for two
// addresses:
//
//	someone@gatedbox   check mode. A new connection prints the two banner
//	                   lines real Tailscale prints, with a URL of its own,
//	                   and then waits until the test writes approved-ID.
//	                   It prints the "Authentication checked" line, and the
//	                   call goes on to the real host behind it. A connection
//	                   that carries a ControlPath which exists shares a
//	                   master that was already approved, so it is let
//	                   through, which is what ssh does.
//	someone@policybox  the policy refuses the login as root, and ssh exits
//	                   255, as real Tailscale does.
//	someone@quickbox   check mode, but Tailscale ends the wait after
//	                   quickHold: ssh prints that the connection closed and
//	                   exits 255. Each dial asks for a new sign-in.
//	someone@localbox   check mode, with a banner that names a page on this
//	                   machine (http://127.0.0.1:631/admin), as anything on
//	                   the host could print.
//	someone@lookalikebox  the same, with an https host that only starts like
//	                   Tailscale's (login.tailscale.com.evil.example).
//
// Once the test writes signedin, every connection to a gated host goes on, and
// a connection that waits goes on at once: the person signed in, and Tailscale
// keeps that for a while.
//
// The banner text is copied from a real run against a host in check mode:
//
//	# Tailscale SSH requires an additional check.
//	# To authenticate, visit: https://login.tailscale.com/a/l1d9c7e392d3020
//
// Every URL the wrapper gives out is appended to urls, one per line, so a test
// can count the approvals a run asked for.

// tailscaleGate is the wrapper's state folder.
type tailscaleGate struct {
	dir string
}

func (g tailscaleGate) urls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(g.dir, "urls"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read the URLs: %v", err)
	}
	return strings.Fields(string(data))
}

// approve approves the check behind url.
func (g tailscaleGate) approve(t *testing.T, url string) {
	t.Helper()
	id := url[strings.LastIndex(url, "/")+1:]
	if err := os.WriteFile(filepath.Join(g.dir, "approved-"+id), nil, 0o600); err != nil {
		t.Fatalf("approve %s: %v", url, err)
	}
}

// signIn marks the person as signed in: every gated connection goes on.
func (g tailscaleGate) signIn(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(g.dir, "signedin"), nil, 0o600); err != nil {
		t.Fatalf("sign in: %v", err)
	}
}

// quickHold is how many tenths of a second the quickbox wrapper waits for a
// sign-in before Tailscale ends the connection.
const quickHold = 60

// gateFleet puts the Tailscale wrapper in front of the fleet's ssh stand-in.
func gateFleet(t *testing.T, f *syncFleet) tailscaleGate {
	t.Helper()
	path, g := writeTailscaleSSH(t, f.here, strings.TrimPrefix(f.env[0], "TUIOS_SSH="))
	f.env = []string{"TUIOS_SSH=" + path}
	return g
}

// writeTailscaleSSH puts the Tailscale wrapper in dir, in front of the ssh
// stand-in inner, and returns its path.
func writeTailscaleSSH(t *testing.T, dir, inner string) (string, tailscaleGate) {
	t.Helper()
	g := tailscaleGate{dir: filepath.Join(dir, "tailscale")}
	if err := os.MkdirAll(g.dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := `#!/bin/sh
state='` + g.dir + `'
cp=; op=; prev=; addr=; seen=
for a in "$@"; do
  if [ -n "$seen" ]; then addr="$a"; break; fi
  case "$prev" in
    -o) case "$a" in ControlPath=*) cp="${a#ControlPath=}" ;; esac ;;
    -O) op="$a" ;;
  esac
  if [ "$a" = "--" ] && [ "$prev" != "-o" ]; then seen=1; fi
  prev="$a"
done
if [ -n "$op" ]; then
  if [ "$op" = exit ] && [ -n "$cp" ]; then rm -f "$cp"; echo "$cp" >> "$state/stopped"; fi
  exit 0
fi
case "$addr" in
  someone@policybox)
    echo 'tailnet policy does not permit you to SSH as user "root"' >&2
    exit 255 ;;
  someone@gatedbox|someone@quickbox|someone@localbox|someone@lookalikebox)
    if [ ! -e "$state/signedin" ] && { [ -z "$cp" ] || [ ! -e "$cp" ]; }; then
      id="e2e$$"
      url="https://login.tailscale.com/a/$id"
      case "$addr" in
        someone@localbox) url="http://127.0.0.1:631/admin" ;;
        someone@lookalikebox) url="https://login.tailscale.com.evil.example/a/$id" ;;
      esac
      echo "$url" >> "$state/urls"
      printf '# Tailscale SSH requires an additional check.\n# To authenticate, visit: %s\n' "$url" >&2
      limit=1200
      if [ "$addr" = someone@quickbox ]; then limit=` + strconv.Itoa(quickHold) + `; fi
      n=0
      while [ ! -e "$state/approved-$id" ] && [ ! -e "$state/signedin" ]; do
        n=$((n+1))
        if [ $n -gt $limit ]; then echo "Connection to 100.64.0.9 port 22 closed by remote host." >&2; exit 255; fi
        sleep 0.1
      done
      printf '# Authentication checked with Tailscale SSH.\r\n' >&2
      if [ -n "$cp" ]; then : > "$cp"; fi
    fi ;;
esac
exec '` + inner + `' "$@"
`
	path := filepath.Join(dir, "fake-ssh-tailscale")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil { //nolint:gosec // an ssh stand-in this test runs
		t.Fatalf("write the Tailscale wrapper: %v", err)
	}
	return path, g
}

// gateRow is the part of a sync row these tests read.
type gateRow struct {
	Host        string `json:"host"`
	Action      string `json:"action"`
	Error       string `json:"error"`
	ErrorKind   string `json:"error_kind"`
	ApprovalURL string `json:"approval_url"`
}

func (f *syncFleet) gateRows(t *testing.T, args ...string) (map[string]gateRow, string) {
	t.Helper()
	out, _ := tuiosCLIEnv(t, f.here, f.env, append([]string{"hosts", "sync", "--json"}, args...)...)
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("sync printed no JSON:\n%s", out)
	}
	var rep struct {
		Hosts []gateRow `json:"hosts"`
	}
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&rep); err != nil {
		t.Fatalf("read the JSON: %v\n%s", err, out)
	}
	rows := map[string]gateRow{}
	for _, r := range rep.Hosts {
		rows[r.Host] = r
	}
	return rows, out
}

// TestHostsSyncReportsATailscaleCheck: off a terminal, a host that Tailscale
// holds for an approval fails at once with the URL, in the JSON and in the
// table, and the link of 'hosts test' and of the daemon says the same. The
// host beside it, with no gate, syncs as usual.
func TestHostsSyncReportsATailscaleCheck(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"open", "gated"}, nil)
	f.hosts["open"].install(t, tuiosBin)
	f.hosts["gated"].install(t, tuiosBin)
	g := gateFleet(t, f)

	began := time.Now()
	rows, out := f.gateRows(t, "--binary", next, "--dry-run")
	took := time.Since(began)
	saveSyncArtifact(t, "check.json", out)
	urls := g.urls(t)
	if len(urls) == 0 {
		t.Fatalf("the wrapper gave out no URL, so the gate was never reached:\n%s", out)
	}
	r := rows["gated"]
	if r.ErrorKind != "tailscale_check" || r.ApprovalURL != urls[len(urls)-1] {
		t.Errorf("ASSERTION: gated reports kind %q and URL %q, want tailscale_check and %s\n%s", r.ErrorKind, r.ApprovalURL, urls[len(urls)-1], out)
	}
	if !strings.Contains(r.Error, "Tailscale needs you to approve this login") || !strings.Contains(r.Error, r.ApprovalURL) {
		t.Errorf("ASSERTION: the error of gated does not say what to do: %q", r.Error)
	}
	if took > 30*time.Second {
		t.Errorf("ASSERTION: sync took %v against a held login; it must fail at once off a terminal", took)
	}
	if o := rows["open"]; o.Error != "" || o.Action != "would update" {
		t.Errorf("ASSERTION: the host with no gate did not plan its update: %+v\n%s", o, out)
	}

	out, err := tuiosCLIEnv(t, f.here, f.env, "hosts", "sync", "--binary", next, "--dry-run")
	saveSyncArtifact(t, "check-table.txt", out)
	if err == nil {
		t.Errorf("ASSERTION: sync exited 0 with a host that waits for an approval:\n%s", out)
	}
	last := g.urls(t)
	if !strings.Contains(out, "needs approval") || !strings.Contains(out, "Tailscale needs you to approve this login. Open "+last[len(last)-1]) {
		t.Errorf("ASSERTION: the table does not show the approval and its URL:\n%s", out)
	}

	// hosts test dials a link of its own.
	out, err = tuiosCLIEnv(t, f.here, f.env, "hosts", "test", "gated", "--json")
	saveSyncArtifact(t, "check-hosts-test.json", out)
	if err == nil {
		t.Errorf("ASSERTION: hosts test exited 0 against a held login:\n%s", out)
	}
	var rep struct {
		Status      string `json:"status"`
		ApprovalURL string `json:"approval_url"`
	}
	if start := strings.Index(out, "{"); start < 0 || json.NewDecoder(strings.NewReader(out[start:])).Decode(&rep) != nil {
		t.Fatalf("hosts test printed no JSON:\n%s", out)
	}
	last = g.urls(t)
	if rep.Status != "tailscale_check" || rep.ApprovalURL != last[len(last)-1] {
		t.Errorf("ASSERTION: hosts test reports %q with URL %q, want tailscale_check and %s\n%s", rep.Status, rep.ApprovalURL, last[len(last)-1], out)
	}

	// The daemon's link waits, and its listing says so with the URL.
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new", "hub", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	var listing string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		listing, _ = tuiosCLIEnv(t, f.here, f.env, "hosts")
		if strings.Contains(listing, "Tailscale SSH needs you to sign in") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	saveSyncArtifact(t, "check-hosts.txt", listing)
	last = g.urls(t)
	if !strings.Contains(listing, "sign in") || !strings.Contains(listing, "Open "+last[len(last)-1]+" to sign in.") ||
		!strings.Contains(listing, "Run 'tuios hosts signin gated' to open the sign-in page.") {
		t.Errorf("ASSERTION: the daemon's listing does not show the approval the link waits for:\n%s", listing)
	}
	if !strings.Contains(listing, "open") || !strings.Contains(listing, "no_daemon") {
		t.Errorf("ASSERTION: the host with no gate is not listed as reached:\n%s", listing)
	}

	// The link goes on when the person approves, with no new dial.
	g.approve(t, last[len(last)-1])
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		listing, _ = tuiosCLIEnv(t, f.here, f.env, "hosts")
		if !strings.Contains(listing, "sign in") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	saveSyncArtifact(t, "approved-hosts.txt", listing)
	if strings.Contains(listing, "sign in") || !strings.Contains(listing, "gated") {
		t.Errorf("ASSERTION: the link still waits after the approval:\n%s", listing)
	}
	if n := len(g.urls(t)); n != len(last) {
		t.Errorf("ASSERTION: the link dialed again for the approval: %d URLs, want %d", n, len(last))
	}
}

// TestHostsSyncReportsATailscalePolicyRefusal: a login the tailnet policy
// refuses names the user and where to change it.
func TestHostsSyncReportsATailscalePolicyRefusal(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"open"}, []string{"policy"})
	f.hosts["open"].install(t, tuiosBin)
	gateFleet(t, f)

	rows, out := f.gateRows(t, "--binary", next, "--dry-run")
	saveSyncArtifact(t, "policy.json", out)
	r := rows["policy"]
	if r.ErrorKind != "tailscale_policy" || !strings.Contains(r.Error, `the user "root"`) || !strings.Contains(r.Error, "[hosts]") {
		t.Errorf("ASSERTION: the policy refusal is %q (kind %q), want one that names the user and [hosts]\n%s", r.Error, r.ErrorKind, out)
	}
	if o := rows["open"]; o.Error != "" {
		t.Errorf("ASSERTION: the host with no gate failed: %+v", o)
	}

	out, err := tuiosCLIEnv(t, f.here, f.env, "hosts", "test", "policy")
	saveSyncArtifact(t, "policy-hosts-test.txt", out)
	if err == nil || !strings.Contains(out, `The tailnet policy does not let you log in as the user "root"`) {
		t.Errorf("ASSERTION: hosts test does not name the refused user: %v\n%s", err, out)
	}
}

// TestHostsSyncOneApprovalCoversTheRun: on a terminal, sync lists the URL,
// offers to wait, and goes on when the person approves. The probe, the link
// and the install of the host share one connection, so they ask for one
// approval between them, and the shared connection is stopped at the end.
func TestHostsSyncOneApprovalCoversTheRun(t *testing.T) {
	next := syncNextBinary(t)
	f := newSyncFleet(t, []string{"gated"}, nil)
	gated := f.hosts["gated"]
	gated.install(t, tuiosBin)
	g := gateFleet(t, f)

	pinPreV080Looks(t, f.here)
	env := append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(f.here, key))
	}
	env = append(env, f.env...)
	term := tuitest.StartT(t, []string{tuiosBin, "hosts", "sync", "--binary", next},
		tuitest.WithSize(160, 40), tuitest.WithEnv(env...), tuitest.WithDir(workDirIn(t, f.here)))

	if err := term.WaitForText("Wait up to 5 minutes for the approvals?", 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: sync never offered to wait for the approval: %v\n%s", err, term.Snapshot())
	}
	urls := g.urls(t)
	if len(urls) != 1 || !strings.Contains(term.Snapshot(), urls[0]) {
		t.Fatalf("ASSERTION: the offer does not show the one URL %v:\n%s", urls, term.Snapshot())
	}
	if err := term.Type("\r"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	g.approve(t, urls[0])
	code, err := term.WaitExit(120 * time.Second)
	saveSyncArtifact(t, "one-approval.txt", term.Snapshot())
	if err != nil || code != 0 {
		t.Fatalf("ASSERTION: sync did not finish after the approval: code %d, %v\n%s", code, err, term.Snapshot())
	}
	screen := term.Snapshot()
	if !strings.Contains(screen, "gated: Tailscale approved the login.") || !strings.Contains(screen, "installed") {
		t.Errorf("ASSERTION: the screen does not show the approval and the install:\n%s", screen)
	}
	if v, err := gated.run(t, "--version"); err != nil || !strings.Contains(v, syncNextVersion) {
		t.Errorf("ASSERTION: the binary on gated is not the new version: %v\n%s", err, v)
	}
	if got := g.urls(t); len(got) != 1 {
		t.Errorf("ASSERTION: the run asked for %d approvals, want 1: %v", len(got), got)
	}
	stopped, _ := os.ReadFile(filepath.Join(g.dir, "stopped"))
	if strings.TrimSpace(string(stopped)) == "" {
		t.Errorf("ASSERTION: sync did not stop the shared connection")
	}
	if left, _ := filepath.Glob(filepath.Join(xdgDir(f.here, "XDG_RUNTIME_DIR"), "tuios-ssh-*")); len(left) != 0 {
		t.Errorf("ASSERTION: sync left its connection folder behind: %v", left)
	}
}
