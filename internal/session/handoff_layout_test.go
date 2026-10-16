package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// handoffLayout is the recorded shape of the handoff manifest's records at
// HandoffVersion. See TestHandoffLayoutMovesWithTheVersion.
const handoffLayout = "1:8a8d88479c3288f9"

// TestHandoffLayoutMovesWithTheVersion fails when a record of the handoff
// manifest changes shape without HandoffVersion moving.
//
// A daemon image reads the manifest its predecessor wrote, and until the
// version range widens they must be the same version. A record that gains,
// loses or retypes a field under the same version would be read by a build
// that does not know it: gob drops what it does not know and zeroes what it
// does not find, and the pane comes back missing that state with no error. The
// layout covers the handoff's own types and names the types it carries from
// elsewhere (SessionState, TerminalState, the events and the mail), whose
// fields are only ever added to and which have compatibility rules of their
// own.
func TestHandoffLayoutMovesWithTheVersion(t *testing.T) {
	var b strings.Builder
	seen := map[reflect.Type]bool{}
	for _, v := range []any{handoffHeader{}, handoffSession{}, handoffPane{}, handoffDaemon{}} {
		describeHandoffType(&b, reflect.TypeOf(v), seen)
	}
	sum := sha256.Sum256([]byte(b.String()))
	got := fmt.Sprintf("%d:%s", HandoffVersion, hex.EncodeToString(sum[:8]))
	if got != handoffLayout {
		t.Fatalf("the handoff records changed shape: layout %s, recorded %s.\n"+
			"If the previous image cannot read the new records, or this image the old ones, move HandoffVersion. Then record the new layout in handoffLayout.\n\n%s",
			got, handoffLayout, b.String())
	}
}

// describeHandoffType writes the fields of a handoff type, and of the handoff
// types it holds, one per line.
func describeHandoffType(b *strings.Builder, t reflect.Type, seen map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] || !strings.HasPrefix(t.Name(), "handoff") {
		return
	}
	seen[t] = true
	fmt.Fprintf(b, "%s\n", t.Name())
	for i := range t.NumField() {
		f := t.Field(i)
		fmt.Fprintf(b, "  %s %s %q\n", f.Name, f.Type, f.Tag)
	}
	for i := range t.NumField() {
		describeHandoffType(b, t.Field(i).Type, seen)
	}
}
