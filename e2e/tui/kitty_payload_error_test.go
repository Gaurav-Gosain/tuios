package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestKittyPayloadErrorReplies sends kitty graphics commands with payloads
// that are not base64 and checks the replies the pane reads back. The daemon
// parses only the control keys of a graphics command and leaves the payload
// undecoded, except where the payload decides a reply. This holds it to the
// replies the standalone TUI gives, which decodes every payload:
//
//   - a transmission that names an image and does not ask for silence gets
//     EINVAL;
//   - a query with a payload that is not base64 gets EINVAL, and a query with a
//     good one gets OK;
//   - the same transmission sent with q=2 gets nothing, and so does a later
//     chunk, which names no image.
func TestKittyPayloadErrorReplies(t *testing.T) {
	const einval = "EINVAL:payload is not valid base64"
	// Each command is sent once. '!' is never base64.
	probe := `\033_Ga=t,f=24,s=1,v=1,i=41;AA!A\033\\` +
		`\033_Ga=q,f=24,s=1,v=1,t=d,i=42;AA!A\033\\` +
		`\033_Ga=q,f=24,s=1,v=1,t=d,i=43;AAAA\033\\` +
		`\033_Ga=t,f=24,s=1,v=1,q=2,i=44;AA!A\033\\` +
		`\033_Ga=t,f=24,s=1,v=1,i=45,q=2,m=1;AAAA\033\\` +
		`\033_Gm=0;AA!A\033\\` +
		`\033[5n`
	for _, daemon := range []bool{false, true} {
		name := "standalone"
		if daemon {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			term, _ := startGraphicsPane(t, daemon)
			dir := t.TempDir()
			out := filepath.Join(dir, "reply")
			script := filepath.Join(dir, "probe.sh")
			// Raw mode so the replies reach the file unchanged and unechoed.
			// With min 0 time 10 a read returns nothing after a second of
			// silence, so cat collects every reply and then sees end of file.
			body := "stty raw -echo min 0 time 10\n" +
				"printf '" + probe + "'\n" +
				"cat > " + out + "\n" +
				"stty sane\n" +
				"echo PROBE\"\"-DONE\n"
			if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			runInShell(t, term, "sh "+script, "PROBE-DONE", 15*time.Second)

			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read reply: %v", err)
			}
			got := string(raw)
			// DSR 5 is answered after every graphics command before it, so its
			// reply proves the pane read them all.
			if !strings.HasSuffix(got, "\x1b[0n") {
				t.Fatalf("the replies do not end with the status report: %q", got)
			}
			for _, want := range []string{
				"\x1b_Gi=41;" + einval + "\x1b\\",
				"\x1b_Gi=42;" + einval + "\x1b\\",
				"\x1b_Gi=43;OK\x1b\\",
			} {
				if n := strings.Count(got, want); n != 1 {
					t.Errorf("the pane read %q %d times, want once: %q", want, n, got)
				}
			}
			for _, id := range []string{"i=44", "i=45"} {
				if strings.Contains(got, "\x1b_G"+id) {
					t.Errorf("the pane read a reply for %s, which asked for silence: %q", id, got)
				}
			}
			if n := strings.Count(got, "\x1b_G"); n != 3 {
				t.Errorf("the pane read %d graphics replies, want 3: %q", n, got)
			}
		})
	}
}
