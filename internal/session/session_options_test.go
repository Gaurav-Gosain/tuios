package session

import (
	"sync"
	"testing"
)

func TestSessionOptionsSetGet(t *testing.T) {
	sess, err := NewSession("opt-test", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Stop()

	if _, ok := sess.GetOption("border_style"); ok {
		t.Fatalf("expected unset option to report ok=false")
	}

	sess.SetOption("border_style", "rounded")
	v, ok := sess.GetOption("border_style")
	if !ok || v != "rounded" {
		t.Fatalf("GetOption = %q,%v; want rounded,true", v, ok)
	}

	sess.SetOption("border_style", "double")
	if v, _ := sess.GetOption("border_style"); v != "double" {
		t.Fatalf("overwrite failed: got %q", v)
	}

	// GetState must expose a copy of Options, not the live map.
	state := sess.GetState()
	if state.Options["border_style"] != "double" {
		t.Fatalf("GetState Options missing value: %+v", state.Options)
	}
	state.Options["border_style"] = "mutated"
	if v, _ := sess.GetOption("border_style"); v != "double" {
		t.Fatalf("GetState returned a live map reference; option was mutated to %q", v)
	}
}

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
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sess.SetOption("k", "v")
				_, _ = sess.GetOption("k")
				_ = sess.AllOptions()
				_ = sess.GetState()
			}
		}()
	}
	wg.Wait()
}
