package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRigLetsATapeActForThePerson is issue #2253. The docs rig plays tapes
// against a throwaway daemon, and a tape's keys do not act for the person,
// so the rig could not drive a TML form, the Inbox thread or the dock key.
//
// A rig is a daemon and a client that both run with TUIOS_RIG=1, with a
// socket, a config and a state folder that are not the person's (the suite
// gives every test its own). There, a tape's Press of Down and Enter picks a
// row of a TML surface in a pane, and the daemon's copy of the surface's
// state (tml get) holds the pick. The same tape with no TUIOS_RIG is refused
// with the notice, and the daemon's state keeps "none": that is the guard as
// it was. So is a tape in a client with no TUIOS_RIG attached to a rig. The client draws the pick either way, so the screen alone does not
// tell the two apart.
//
// What it must not open, and where that is tested: send-keys still never
// acts for the person, in a rig too (below), and a daemon whose socket or
// folders are the person's is not a rig (TestRigRefusal in internal/session).
//
// Negative control: see NEGATIVE_CONTROLS.md, "The docs rig".
func TestRigLetsATapeActForThePerson(t *testing.T) {
	const refused = "A key that send-keys or a tape typed does not act"
	for _, c := range []struct {
		name, rig, picked string
		// clientRig is the client's TUIOS_RIG, when it differs from the
		// daemon's.
		clientRig *string
	}{
		{"rig", "1", "TestAttachReplay", nil},
		{"no rig", "", "none", nil},
		// The person's own client has no TUIOS_RIG. Attached to a rig, its
		// tape keys stay refused: both ends must say rig.
		{"rig daemon, plain client", "1", "none", new(string)},
	} {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			killDaemon(t, base)
			t.Setenv("TUIOS_RIG", c.rig)
			const sess = "e2e-rig"
			mustCLI(t, base, "new", sess, "--detach")
			clientRig := c.rig
			if c.clientRig != nil {
				clientRig = *c.clientRig
			}
			term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", sess}, env: []string{"TUIOS_RIG=" + clientRig}})
			waitWindowCount(t, term, 1, "the first pane")
			doc := writeTMLDoc(t, base, "run.tml", tmlRunDoc)
			var opened struct {
				Surface string `json:"surface"`
			}
			if out := mustCLI(t, base, "tml", "show", doc, "--pane", "--json", "-s", sess); json.Unmarshal([]byte(out), &opened) != nil || opened.Surface == "" {
				t.Fatalf("tuios tml show --pane printed %q", out)
			}
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return screenHas(s, "Summary", "Picked: none")
			}, uiTimeout); err != nil {
				t.Fatalf("the pane never drew the surface: %v\n%s", err, term.Snapshot())
			}
			// picked is the daemon's live state of the surface, not the
			// document's first state, which tml get prints too.
			picked := func() string {
				var got struct {
					State struct {
						Picked string `json:"picked"`
					} `json:"state"`
				}
				_ = json.Unmarshal([]byte(mustCLI(t, base, "tml", "get", opened.Surface)), &got)
				return got.State.Picked
			}
			// waitPicked waits for the daemon's state to hold want, or, for
			// a refusal, for the notice and then checks the state kept want.
			waitPicked := func(want, what string) {
				t.Helper()
				deadline := time.Now().Add(uiTimeout)
				for picked() != want {
					if time.Now().After(deadline) {
						t.Fatalf("ASSERTION: %s: the surface's state holds %q, want %q\n%s", what, picked(), want, term.Snapshot())
					}
					time.Sleep(50 * time.Millisecond)
				}
			}
			tape := filepath.Join(base, "pick.tape")
			if err := os.WriteFile(tape, []byte("Press Down Enter\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			mustCLI(t, base, "tape", "exec", "-s", sess, tape)
			if clientRig == "" {
				if err := term.WaitForText(refused, uiTimeout); err != nil {
					t.Fatalf("ASSERTION: the tape was not refused: %v\n%s", err, term.Snapshot())
				}
				waitPicked("none", "a tape outside a rig")
				saveArtifact(t, term, artifactDir(t), "rig-off")
				return
			}
			waitPicked(c.picked, "a tape in a rig")
			if strings.Contains(term.Screen().Text(), refused) {
				t.Fatalf("ASSERTION: a tape was refused in a rig\n%s", term.Snapshot())
			}
			saveArtifact(t, term, artifactDir(t), "rig-on")
			// send-keys never acts for the person, in a rig too.
			mustCLI(t, base, "send-keys", "-s", sess, "Up,Enter")
			if err := term.WaitForText(refused, uiTimeout); err != nil {
				t.Fatalf("ASSERTION: send-keys was not refused in a rig: %v\n%s", err, term.Snapshot())
			}
			waitPicked(c.picked, "send-keys in a rig")
		})
	}
}
