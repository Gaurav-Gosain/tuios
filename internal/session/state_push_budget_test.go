package session

import (
	"bytes"
	"encoding/gob"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// pushBudget is the most a state push or an attach reply may weigh for the
// thousand cards below once their prompts, histories and verify output stay
// behind. The cards without those come to about 175 KB.
const pushBudget = 256 << 10

// The bodies every card of pushCards carries.
var (
	pushPrompt = pushFill("Reproduce the flake, find the race and fix it. ", 16<<10)
	pushTail   = pushFill("--- FAIL: TestReconnect (0.31s) not ok yet\n", 2<<10)
)

// pushFill repeats s to exactly n bytes.
func pushFill(s string, n int) string {
	return strings.Repeat(s, n/len(s)+1)[:n]
}

// pushCards is a state with n cards as an agent board holds them after a
// failed verify: a 16 KiB prompt, 13 history entries and a 2 KiB verify tail.
func pushCards(n int) *SessionState {
	const at = int64(1_762_000_000)
	st := &SessionState{Name: "board"}
	for i := range n {
		id := "t" + strconv.Itoa(i+1)
		hist := make([]TaskEvent, 13)
		for j := range hist {
			hist[j] = TaskEvent{Action: TaskActionClaimed, By: "cli", At: at + int64(j), Stage: TaskStageDoing}
		}
		exit := 3
		st.Tasks = append(st.Tasks, TaskState{
			ID:       id,
			Title:    "fix the flaky reconnect test in the e2e suite, card " + strconv.Itoa(i+1),
			Prompt:   pushPrompt,
			Stage:    TaskStageTodo,
			StageAt:  at,
			By:       "cli",
			At:       at,
			Attempts: 1,
			Note:     "exit 3: not ok yet",
			History:  hist,
			Attachments: []TaskAttachment{{
				Name: "after.png", Kind: AttachImage, MediaType: "image/png", Size: 1 << 20,
				Hash: strings.Repeat("ab", 32), Note: "the layout after the fix", By: "cli", At: at,
			}},
			Verify: &TaskVerify{
				State: "failed", Exit: &exit, Note: "exit 3: not ok yet",
				Tail: pushTail, StartedAt: at, FinishedAt: at + 5,
			},
		})
	}
	return st
}

// TestStatePushLeavesCardBodies: every change to a session pushes the whole
// state to its clients, and the attach reply carries it too. No client reads
// a card's prompt, history or verify output from a pushed state (the board
// lists cards with list-tasks), so a push carries the cards without them and
// stays far under the 16 MiB frame a client accepts, while the daemon's own
// cards keep every body. A session without cards is sent exactly as before.
//
// Measured with the encoded length of the message, not the heap.
func TestStatePushLeavesCardBodies(t *testing.T) {
	st := pushCards(1000)

	t.Run("state sync, 1000 cards", func(t *testing.T) {
		msg, err := NewMessage(MsgStateSync, &StateSyncPayload{State: st})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("state sync of 1000 cards: %d bytes", len(msg.Payload))
		if len(msg.Payload) > pushBudget {
			t.Errorf("ASSERTION: a state sync of 1000 cards is %d bytes, want at most %d", len(msg.Payload), pushBudget)
		}
	})

	t.Run("attach reply, 1000 cards", func(t *testing.T) {
		msg, err := NewMessage(MsgAttached, &AttachedPayload{SessionName: st.Name, Width: 120, Height: 40, State: st})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("attach reply of 1000 cards: %d bytes", len(msg.Payload))
		if len(msg.Payload) > pushBudget {
			t.Errorf("ASSERTION: an attach reply of 1000 cards is %d bytes, want at most %d", len(msg.Payload), pushBudget)
		}
	})

	t.Run("the state keeps its cards", func(t *testing.T) {
		st := pushCards(1000)
		sync, err := NewMessage(MsgStateSync, &StateSyncPayload{State: st})
		if err != nil {
			t.Fatal(err)
		}
		attach, err := NewMessage(MsgAttached, &AttachedPayload{SessionName: st.Name, Width: 120, Height: 40, State: st})
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range st.Tasks {
			if tk.Prompt != pushPrompt || len(tk.History) != 13 || tk.Verify == nil || tk.Verify.Tail != pushTail {
				t.Fatalf("ASSERTION: after the pushes the state's card %s lost a body: prompt %d bytes, %d history entries, verify %+v",
					tk.ID, len(tk.Prompt), len(tk.History), tk.Verify)
			}
		}
		var gotSync StateSyncPayload
		if err := sync.ParsePayload(&gotSync); err != nil {
			t.Fatal(err)
		}
		var gotAttach AttachedPayload
		if err := attach.ParsePayload(&gotAttach); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name string
			got  *SessionState
		}{{"state sync", gotSync.State}, {"attach reply", gotAttach.State}} {
			name, got := c.name, c.got
			if got == nil || len(got.Tasks) != len(st.Tasks) {
				t.Fatalf("ASSERTION: the %s does not carry the %d cards", name, len(st.Tasks))
			}
			for i, tk := range got.Tasks {
				want := st.Tasks[i]
				wantCard := want
				wantCard.Prompt = ""
				wantCard.History = nil
				wantCard.Attachments = nil
				v := *want.Verify
				v.Tail = ""
				wantCard.Verify = &v
				if tk.Prompt != "" || len(tk.History) != 0 || tk.Verify.Tail != "" || len(tk.Attachments) != 0 {
					t.Fatalf("ASSERTION: the %s's card %s carries its bodies: prompt %d bytes, %d history entries, tail %d bytes, %d attachments",
						name, tk.ID, len(tk.Prompt), len(tk.History), len(tk.Verify.Tail), len(tk.Attachments))
				}
				if !reflect.DeepEqual(tk, wantCard) {
					t.Fatalf("ASSERTION: the %s's card %d is %+v (verify %+v), want %+v (verify %+v)",
						name, i, tk, tk.Verify, wantCard, wantCard.Verify)
				}
			}
		}
	})

	t.Run("no cards", func(t *testing.T) {
		st := &SessionState{Name: "plain", Width: 120, Height: 40, Windows: []WindowState{{ID: "w1", Title: "shell", Width: 120, Height: 39}}}
		for _, c := range []struct {
			name    string
			typ     MessageType
			payload any
		}{
			{"state sync", MsgStateSync, &StateSyncPayload{State: st, TriggerType: "window"}},
			{"attach reply", MsgAttached, &AttachedPayload{SessionName: st.Name, Width: 120, Height: 40, State: st}},
		} {
			name, payload := c.name, c.payload
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(payload); err != nil {
				t.Fatal(err)
			}
			msg, err := NewMessage(c.typ, payload)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(msg.Payload, buf.Bytes()) {
				t.Errorf("ASSERTION: the %s of a state with no cards is %d bytes, not the %d of a plain gob encode", name, len(msg.Payload), buf.Len())
			}
		}
	})
}
