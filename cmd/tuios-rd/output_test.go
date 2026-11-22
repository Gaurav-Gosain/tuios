//go:build linux || darwin

package main

import "testing"

// TestOutputQuery checks that --output reaches every URL that captures or sends input.
func TestOutputQuery(t *testing.T) {
	a := &app{cfg: config{base: "http://127.0.0.1:8093", output: "DP-3"}}
	if got, want := a.wsURL(false), "ws://127.0.0.1:8093/stream?view=1&output=DP-3"; got != want {
		t.Fatalf("view URL %q, want %q", got, want)
	}
	if got, want := a.wsURL(true), "ws://127.0.0.1:8093/stream?take=1&output=DP-3"; got != want {
		t.Fatalf("take URL %q, want %q", got, want)
	}
	a.useTiles.Store(true)
	if got, want := a.wsURL(false), "ws://127.0.0.1:8093/stream?view=1&output=DP-3&codec=tiles"; got != want {
		t.Fatalf("tiles URL %q, want %q", got, want)
	}
	if got := a.outputQuery("?"); got != "?output=DP-3" {
		t.Fatalf("outputQuery: %q", got)
	}
	a.cfg.output = ""
	if got := a.outputQuery("?"); got != "" {
		t.Fatalf("outputQuery without --output: %q", got)
	}
}

// TestDisplayLabel checks the display names in the status line.
func TestDisplayLabel(t *testing.T) {
	a := &app{cfg: config{output: "all"}}
	a.outputs.Store([]outputInfo{{Name: "DP-2", Width: 1920, Height: 1080, Scale: 1}, {Name: "DP-3", X: 1920, Width: 1920, Height: 1080, Scale: 1}})
	if got := a.displayLabel(); got != "DP-2 DP-3" {
		t.Fatalf("no pointer yet: %q", got)
	}
	a.pointerX.Store(49152 + 1) // three quarters across: on DP-3
	a.pointerY.Store(32768 + 1)
	if got := a.displayLabel(); got != "DP-2 [DP-3]" {
		t.Fatalf("pointer on DP-3: %q", got)
	}
	a.pointerX.Store(1000 + 1)
	if got := a.displayLabel(); got != "[DP-2] DP-3" {
		t.Fatalf("pointer on DP-2: %q", got)
	}
	a.cfg.output = "DP-3"
	if got := a.displayLabel(); got != "DP-3" {
		t.Fatalf("one display: %q", got)
	}
}
