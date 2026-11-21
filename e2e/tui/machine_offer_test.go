package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

const offerFakeSSH = `#!/bin/sh
if [ "$1" = "-G" ]; then
	for a in "$@"; do d=$a; done
	case "$d" in *@*) u=${d%@*}; h=${d#*@} ;; *) u=$(id -un); h=$d ;; esac
	printf 'hostname %s\nuser %s\nport 22\n' "$h" "$u"
	exit 0
fi
for a in "$@"; do case "$a" in -*) ;; *) d=$a; break ;; esac; done
printf 'Last login: Wed Nov  4 21:14:07 2026 from 192.168.1.20\n'
printf 'Welcome to %s.\n' "${d#*@}"
printf '%s ~ $ ' "$d"
while IFS= read -r l; do
	[ "$l" = exit ] && exit 0
	printf '%s ~ $ ' "$d"
done
`

const offerFakeSCP = `#!/bin/sh
ssh -t gaurav@scpbox scp -t /tmp
`

func TestMachineOfferOnSSHLogin(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"ssh": offerFakeSSH, "scp": offerFakeSCP} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil { //nolint:gosec // stand-ins this test runs
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := configPathIn(base)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[daemon]\nagent_detect_seconds = 1\n\n[agents]\nenabled = false\n\n[notifications]\nduration = 60\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", "offer", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	const cols, rows = 140, 32
	term := attachIn(t, base, "offer", startOpts{cols: cols, rows: rows, shippedLooks: true})

	run := func(line, up string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", "offer", line+"\n"); err != nil {
			t.Fatalf("type %q: %v\n%s", line, err, out)
		}
		if err := term.WaitForText(up, uiTimeout); err != nil {
			t.Fatalf("%q never started: %v\n%s", line, err, term.Snapshot())
		}
	}
	leave := func() {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", "offer", "exit\n"); err != nil {
			t.Fatalf("leave the fake ssh: %v\n%s", err, out)
		}
	}
	noOffer := func(tt *tuitest.Terminal, machine, why string) {
		t.Helper()
		deadline := time.Now().Add(9 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(tt.Screen().Text(), machine+" is not a machine here") {
				t.Fatalf("ASSERTION: %s brought up the offer\n%s", why, tt.Snapshot())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	const offerWait = 20 * time.Second

	run("ssh -N gaurav@tunnelbox", "gaurav@tunnelbox ~ $")
	noOffer(term, "tunnelbox", "ssh -N")
	leave()

	run("scp", "gaurav@scpbox ~ $")
	noOffer(term, "scpbox", "ssh run by scp")
	leave()

	run("clear; ssh gaurav@buildbox", "gaurav@buildbox ~ $")
	offer := "buildbox is not a machine here. Add it?"
	if err := term.WaitForText(offer, offerWait); err != nil {
		t.Fatalf("ASSERTION: the login never brought up the offer: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("Add  Not now  Never", uiTimeout); err != nil {
		t.Fatalf("the offer has no choices: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitFor(func(s tuitest.Screen) bool {
		r := findRow(s, offer)
		return r < 0 || !strings.Contains(s.Line(r), "+1")
	}, uiTimeout)
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "machine-offer")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "machine-offer")
	t.Logf("frame machine-offer saved under %s", dir)
	if n := strings.Count(term.Screen().Text(), offer); n != 1 {
		t.Fatalf("ASSERTION: the offer shows %d times\n%s", n, term.Snapshot())
	}

	row, col := findText(t, term, "Not now  Never")
	mouseClick(t, term, col+10, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), offer) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Never did not take the offer away: %v\n%s", err, term.Snapshot())
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(sidebarStatePath(base))
		if strings.Contains(string(data), `"machine_offers_never":["gaurav@buildbox:22"]`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: sidebar.json does not hold the Never:\n%s", data)
		}
		time.Sleep(100 * time.Millisecond)
	}
	noOffer(term, "buildbox", "the same login, after Never")
	if err := term.Close(); err != nil {
		t.Logf("close the client: %v", err)
	}

	second := attachIn(t, base, "offer", startOpts{cols: cols, rows: rows, shippedLooks: true})
	noOffer(second, "buildbox", "a new client after Never")
	term = second
	leave()
	run("clear; ssh gaurav@otherbox", "gaurav@otherbox ~ $")
	if err := second.WaitForText("otherbox is not a machine here. Add it?", offerWait); err != nil {
		t.Fatalf("ASSERTION: the new client never offered another machine: %v\n%s", err, second.Snapshot())
	}
	saveArtifact(t, second, dir, "machine-offer-second-client")

	row, col = findText(t, second, "Add  Not now")
	mouseClick(t, second, col, row, tuitest.MouseLeft, 0)
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Add a machine") && strings.Contains(text, "✓ Pick") && !strings.Contains(text, "otherbox is not a machine here")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: Add did not open the sheet at its test step: %v\n%s", err, second.Snapshot())
	}
	saveArtifact(t, second, dir, "machine-offer-add-sheet")
}
