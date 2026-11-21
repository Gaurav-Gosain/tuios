package session

import (
	"testing"
)

func newTestSession(t *testing.T) *Session {
	t.Helper()
	sess, err := NewSession("ops-test", &SessionConfig{}, 80, 24)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	t.Cleanup(sess.Stop)
	return sess
}

func TestFindWindowStateIndexAmbiguity(t *testing.T) {
	windows := []WindowState{
		{ID: "aaa-1", CustomName: "dup"},
		{ID: "aaa-2", CustomName: "dup"},
		{ID: "bbb-3", Title: "unique"},
	}

	if _, err := findWindowStateIndex(windows, "aaa"); err == nil {
		t.Error("expected ambiguous prefix error")
	}
	if _, err := findWindowStateIndex(windows, "dup"); err == nil {
		t.Error("expected ambiguous name error")
	}
	if idx, err := findWindowStateIndex(windows, "bbb-3"); err != nil || idx != 2 {
		t.Errorf("exact ID match: idx=%d err=%v", idx, err)
	}
	if idx, err := findWindowStateIndex(windows, "unique"); err != nil || idx != 2 {
		t.Errorf("title match: idx=%d err=%v", idx, err)
	}
	if _, err := findWindowStateIndex(windows, "nope"); err == nil {
		t.Error("expected no-match error")
	}
}

func TestFindWindowStateIndexByPosition(t *testing.T) {
	windows := []WindowState{
		{ID: "aaa-x", Title: "first"},
		{ID: "17b2c3d4-aaaa", Title: "second"},
		{ID: "ccc-z", Title: "third"},
	}

	if idx, err := findWindowStateIndex(windows, "1"); err != nil || idx != 1 {
		t.Errorf("index target 1: idx=%d err=%v", idx, err)
	}
	if idx, err := findWindowStateIndex(windows, "0"); err != nil || idx != 0 {
		t.Errorf("index target 0: idx=%d err=%v", idx, err)
	}
	if idx, err := findWindowStateIndex(windows, "17"); err != nil || idx != 1 {
		t.Errorf("digit prefix 17: idx=%d err=%v", idx, err)
	}
	if _, err := findWindowStateIndex(windows, "9"); err == nil {
		t.Error("expected no-match error for out-of-range index")
	}
	named := []WindowState{{ID: "aaa", CustomName: "1"}, {ID: "bbb"}}
	if idx, err := findWindowStateIndex(named, "1"); err != nil || idx != 1 {
		t.Errorf("index beats digit name: idx=%d err=%v", idx, err)
	}
}
