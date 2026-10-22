package session

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A scalar pointer loses explicit zero through gob. An old client omits the
// field on its next push, and an unchanged fingerprint can suppress the update.
func TestPopupDimWireAndRetention(t *testing.T) {
	seen := map[uint64]bool{}
	for _, dim := range []*config.PopupDim{nil, {}, {Percent: 50}} {
		state := SessionState{Windows: []WindowState{{ID: "popup", Popup: true, PopupDim: dim}}}
		hash := StateFingerprint(&state)
		if seen[hash] {
			t.Fatal("absent, zero and nonzero dim share a fingerprint")
		}
		seen[hash] = true
		var wire bytes.Buffer
		if err := gob.NewEncoder(&wire).Encode(state); err != nil {
			t.Fatal(err)
		}
		var peer SessionState
		if err := gob.NewDecoder(&wire).Decode(&peer); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		var saved SessionState
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		older := SessionState{Windows: []WindowState{{ID: "popup"}}}
		retainDaemonExclusive(&older, &state)
		for _, got := range []*SessionState{&peer, &saved, &older} {
			value := got.Windows[0].PopupDim
			if (value == nil) != (dim == nil) || (dim != nil && value.Percent != dim.Percent) {
				t.Fatalf("dim %v became %v after wire, save or old-client sync", dim, value)
			}
		}
	}
}
