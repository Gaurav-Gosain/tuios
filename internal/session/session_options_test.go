package session

import (
	"sync"
	"testing"
)

func TestSessionOptionsSurviveStateSync(t *testing.T) {
	sess, err := NewSession("opt-sync", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	sess.SetOption("theme", "dracula")

	// A TUI state sync never populates Options; the daemon must preserve them.
	sess.UpdateState(&SessionState{Name: "opt-sync", CurrentWorkspace: 1})

	if v, ok := sess.GetOption("theme"); !ok || v != "dracula" {
		t.Fatalf("option lost across UpdateState: %q,%v", v, ok)
	}

	// An explicit non-nil Options in the incoming state replaces the bag.
	sess.UpdateState(&SessionState{Name: "opt-sync", Options: map[string]string{"theme": "nord"}})
	if v, _ := sess.GetOption("theme"); v != "nord" {
		t.Fatalf("explicit Options did not replace bag: %q", v)
	}
}

func TestSessionOptionsConcurrent(t *testing.T) {
	sess, err := NewSession("opt-race", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				sess.SetOption("k", "v")
				_, _ = sess.GetOption("k")
				_ = sess.GetState()
			}
		})
	}
	wg.Wait()
}
