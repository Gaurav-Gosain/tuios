package vt

import (
	"io"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestKittyKeyboardState(t *testing.T) {
	t.Run("initial state", func(t *testing.T) {
		s := newKittyKeyboardState()
		if s.CurrentFlags() != 0 {
			t.Errorf("expected initial flags=0, got %d", s.CurrentFlags())
		}
	})

	t.Run("push and pop", func(t *testing.T) {
		s := newKittyKeyboardState()
		s.Push(ansi.KittyDisambiguateEscapeCodes)
		if s.CurrentFlags() != ansi.KittyDisambiguateEscapeCodes {
			t.Errorf("expected flags=%d after push, got %d", ansi.KittyDisambiguateEscapeCodes, s.CurrentFlags())
		}

		s.Push(ansi.KittyReportEventTypes | ansi.KittyDisambiguateEscapeCodes)
		if s.CurrentFlags() != ansi.KittyReportEventTypes|ansi.KittyDisambiguateEscapeCodes {
			t.Errorf("expected flags=%d after second push, got %d",
				ansi.KittyReportEventTypes|ansi.KittyDisambiguateEscapeCodes, s.CurrentFlags())
		}

		s.Pop(1)
		if s.CurrentFlags() != ansi.KittyDisambiguateEscapeCodes {
			t.Errorf("expected flags=%d after pop, got %d", ansi.KittyDisambiguateEscapeCodes, s.CurrentFlags())
		}

		s.Pop(1)
		if s.CurrentFlags() != 0 {
			t.Errorf("expected flags=0 after second pop, got %d", s.CurrentFlags())
		}

		// Can't pop below base
		s.Pop(5)
		if s.CurrentFlags() != 0 {
			t.Errorf("expected flags=0 after over-pop, got %d", s.CurrentFlags())
		}
	})

	t.Run("set modes", func(t *testing.T) {
		s := newKittyKeyboardState()
		s.Push(ansi.KittyDisambiguateEscapeCodes)

		// Mode 1: set given flags, unset all others
		s.Set(ansi.KittyReportEventTypes, 1)
		if s.CurrentFlags() != ansi.KittyReportEventTypes {
			t.Errorf("mode 1: expected flags=%d, got %d", ansi.KittyReportEventTypes, s.CurrentFlags())
		}

		// Mode 2: set given flags, keep existing
		s.Set(ansi.KittyDisambiguateEscapeCodes, 2)
		expected := ansi.KittyReportEventTypes | ansi.KittyDisambiguateEscapeCodes
		if s.CurrentFlags() != expected {
			t.Errorf("mode 2: expected flags=%d, got %d", expected, s.CurrentFlags())
		}

		// Mode 3: unset given flags, keep existing
		s.Set(ansi.KittyReportEventTypes, 3)
		if s.CurrentFlags() != ansi.KittyDisambiguateEscapeCodes {
			t.Errorf("mode 3: expected flags=%d, got %d", ansi.KittyDisambiguateEscapeCodes, s.CurrentFlags())
		}
	})

	t.Run("reset", func(t *testing.T) {
		s := newKittyKeyboardState()
		s.Push(ansi.KittyAllFlags)
		s.Push(ansi.KittyDisambiguateEscapeCodes)
		s.Reset()
		if s.CurrentFlags() != 0 {
			t.Errorf("expected flags=0 after reset, got %d", s.CurrentFlags())
		}
		if len(s.stack) != 1 {
			t.Errorf("expected stack depth=1 after reset, got %d", len(s.stack))
		}
	})

	t.Run("flag helpers", func(t *testing.T) {
		s := newKittyKeyboardState()
		s.Push(ansi.KittyDisambiguateEscapeCodes | ansi.KittyReportEventTypes)

		if !s.HasDisambiguate() {
			t.Error("expected HasDisambiguate=true")
		}
		if !s.HasReportEvents() {
			t.Error("expected HasReportEvents=true")
		}
		if s.HasReportAlternateKeys() {
			t.Error("expected HasReportAlternateKeys=false")
		}
		if s.HasReportAllKeys() {
			t.Error("expected HasReportAllKeys=false")
		}
	})
}

func TestKittyKeyboardCSIHandlers(t *testing.T) {
	t.Run("push via CSI > u", func(t *testing.T) {
		e := NewEmulator(80, 24)
		defer e.Close()

		// Push flags=1 (disambiguate)
		e.Write([]byte("\x1b[>1u"))
		if e.KittyKeyboardFlags() != 1 {
			t.Errorf("expected flags=1 after push, got %d", e.KittyKeyboardFlags())
		}
	})

	t.Run("pop via CSI < u", func(t *testing.T) {
		e := NewEmulator(80, 24)
		defer e.Close()

		e.Write([]byte("\x1b[>3u"))  // Push flags=3
		e.Write([]byte("\x1b[>15u")) // Push flags=15
		e.Write([]byte("\x1b[<1u"))  // Pop 1
		if e.KittyKeyboardFlags() != 3 {
			t.Errorf("expected flags=3 after pop, got %d", e.KittyKeyboardFlags())
		}
	})

	t.Run("query via CSI ? u", func(t *testing.T) {
		e := NewEmulator(80, 24)
		defer e.Close()

		e.Write([]byte("\x1b[>5u")) // Push flags=5

		// Read response in goroutine
		responseChan := make(chan string, 1)
		errChan := make(chan error, 1)
		go func() {
			buf := make([]byte, 256)
			n, err := e.Read(buf)
			if err != nil && err != io.EOF {
				errChan <- err
				return
			}
			responseChan <- string(buf[:n])
		}()

		e.Write([]byte("\x1b[?u")) // Query

		select {
		case response := <-responseChan:
			expected := "\x1b[?5u"
			if response != expected {
				t.Errorf("expected response %q, got %q", expected, response)
			}
		case err := <-errChan:
			t.Fatalf("Read error: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for response")
		}
	})

	t.Run("set via CSI = u", func(t *testing.T) {
		e := NewEmulator(80, 24)
		defer e.Close()

		e.Write([]byte("\x1b[>1u"))   // Push flags=1
		e.Write([]byte("\x1b[=3;2u")) // Set flags=3, mode=2 (OR into existing)
		if e.KittyKeyboardFlags() != 3 {
			t.Errorf("expected flags=3 after set mode=2, got %d", e.KittyKeyboardFlags())
		}
	})

	t.Run("full reset clears kitty keyboard", func(t *testing.T) {
		e := NewEmulator(80, 24)
		defer e.Close()

		e.Write([]byte("\x1b[>15u")) // Push flags
		e.Write([]byte("\x1bc"))     // Full reset (RIS)
		if e.KittyKeyboardFlags() != 0 {
			t.Errorf("expected flags=0 after full reset, got %d", e.KittyKeyboardFlags())
		}
	})
}

func TestEncodeKeyCSIu(t *testing.T) {
	tests := []struct {
		name     string
		key      KeyPressEvent
		flags    int
		expected string
	}{
		{
			name:     "regular char without flags",
			key:      KeyPressEvent{Code: 'a'},
			flags:    0,
			expected: "",
		},
		{
			name:     "regular char with disambiguate - no mod",
			key:      KeyPressEvent{Code: 'a'},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "",
		},
		{
			name:     "regular char with report-all-keys",
			key:      KeyPressEvent{Code: 'a'},
			flags:    ansi.KittyReportAllKeysAsEscapeCodes,
			expected: "\x1b[97u",
		},
		{
			name:     "ctrl+a with disambiguate",
			key:      KeyPressEvent{Code: 'a', Mod: ModCtrl},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[97;5u",
		},
		{
			name:     "enter with disambiguate",
			key:      KeyPressEvent{Code: KeyEnter},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[13u",
		},
		{
			name:     "escape with disambiguate",
			key:      KeyPressEvent{Code: KeyEscape},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[27u",
		},
		{
			name:     "up arrow without modifiers",
			key:      KeyPressEvent{Code: KeyUp},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[A",
		},
		{
			name:     "shift+up arrow",
			key:      KeyPressEvent{Code: KeyUp, Mod: ModShift},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[1;2A",
		},
		{
			name:     "ctrl+shift+up arrow",
			key:      KeyPressEvent{Code: KeyUp, Mod: ModCtrl | ModShift},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[1;6A",
		},
		{
			name:     "F5 without modifiers",
			key:      KeyPressEvent{Code: KeyF5},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[15~",
		},
		{
			name:     "ctrl+F5",
			key:      KeyPressEvent{Code: KeyF5, Mod: ModCtrl},
			flags:    ansi.KittyDisambiguateEscapeCodes,
			expected: "\x1b[15;5~",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EncodeKeyCSIu(tt.key, tt.flags)
			if result != tt.expected {
				t.Errorf("EncodeKeyCSIu(%v, %d) = %q, want %q", tt.key, tt.flags, result, tt.expected)
			}
		})
	}
}

func TestKittyModParam(t *testing.T) {
	tests := []struct {
		mod      KeyMod
		expected int
	}{
		{0, 1},
		{ModShift, 2},
		{ModAlt, 3},
		{ModCtrl, 5},
		{ModMeta, 9},
		{ModShift | ModCtrl, 6},
		{ModShift | ModAlt | ModCtrl, 8},
	}

	for _, tt := range tests {
		result := kittyModParam(tt.mod)
		if result != tt.expected {
			t.Errorf("kittyModParam(%d) = %d, want %d", tt.mod, result, tt.expected)
		}
	}
}
