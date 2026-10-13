package procinfo

import (
	"encoding/binary"
	"testing"
)

func TestEnvironVar(t *testing.T) {
	block := []byte("PATH=/bin\x00TUIOS_AGENT=codex\x00TUIOS_AGENTX=no\x00")
	if v, ok := EnvironVar(block, "TUIOS_AGENT"); !ok || v != "codex" {
		t.Fatalf("EnvironVar = %q %v, want codex", v, ok)
	}
	if _, ok := EnvironVar(block, "HOME"); ok {
		t.Fatal("found a variable that is not there")
	}
}

func TestProcargsEnvVar(t *testing.T) {
	build := func(argc int, rest string) []byte {
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, uint32(argc))
		return append(buf, rest...)
	}
	cases := []struct {
		name string
		buf  []byte
		want string
		ok   bool
	}{
		{"after argv", build(2, "/bin/sh\x00\x00\x00sh\x00TUIOS_AGENT=fake\x00TUIOS_AGENT=codex\x00\x00"), "codex", true},
		// An argument that looks like the variable is not the environment.
		{"argv is not env", build(2, "/bin/sh\x00\x00sh\x00TUIOS_AGENT=fake\x00HOME=/h\x00\x00"), "", false},
		{"apple strings after env are not env", build(1, "/bin/sh\x00\x00sh\x00HOME=/h\x00\x00TUIOS_AGENT=apple\x00"), "", false},
		{"truncated", []byte{1, 0}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := ProcargsEnvVar(tc.buf, "TUIOS_AGENT")
			if v != tc.want || ok != tc.ok {
				t.Fatalf("got %q %v, want %q %v", v, ok, tc.want, tc.ok)
			}
		})
	}
}
