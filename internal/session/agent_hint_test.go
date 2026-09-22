package session

import (
	"encoding/binary"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestEnvironVar(t *testing.T) {
	block := []byte("PATH=/bin\x00TUIOS_AGENT=codex\x00TUIOS_AGENTX=no\x00")
	if v, ok := environVar(block, "TUIOS_AGENT"); !ok || v != "codex" {
		t.Fatalf("environVar = %q %v, want codex", v, ok)
	}
	if _, ok := environVar(block, "HOME"); ok {
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
			v, ok := procargsEnvVar(tc.buf, "TUIOS_AGENT")
			if v != tc.want || ok != tc.ok {
				t.Fatalf("got %q %v, want %q %v", v, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestAgentHintNamesAPaneBehindAnOpaqueWrapper is P27: a sandbox wrapper runs
// an agent no process walk can see, and TUIOS_AGENT on the wrapper names it.
func TestAgentHintNamesAPaneBehindAnOpaqueWrapper(t *testing.T) {
	m := newAgentMatcher(nil)
	hint := func(v string) func() string { return func() string { return v } }

	wrapper := foregroundInfo{comm: "docker", argv: []string{"docker", "run", "-it", "box"}, pid: 42, hint: hint("claude-code")}
	d, ok := m.identifyDetail(wrapper)
	if !ok || d.harness != "claude-code" || d.tier != identityHint {
		t.Fatalf("wrapper with a hint: %+v %v", d, ok)
	}

	// The program name a manifest detects works as well as the id.
	wrapper.hint = hint("codex")
	if d, ok := m.identifyDetail(wrapper); !ok || d.harness != "codex" {
		t.Fatalf("hint by program name: %+v %v", d, ok)
	}

	// A hint naming no manifest is not trusted into a claim.
	wrapper.hint = hint("not-an-agent")
	if d, ok := m.identifyDetail(wrapper); ok {
		t.Fatalf("an unknown hint matched: %+v", d)
	}

	// A process that is itself an agent is recognised as what it is.
	real := foregroundInfo{comm: "codex", argv: []string{"codex"}, pid: 43, hint: hint("claude-code")}
	if d, ok := m.identifyDetail(real); !ok || d.harness != "codex" || d.tier == identityHint {
		t.Fatalf("a real agent with a stray hint: %+v %v", d, ok)
	}
}

// TestReadAgentHintEnvFromALiveProcess reads the variable from a real child,
// which is what checks the procargs2 layout against the kernel on darwin.
//
// The child is this test binary rather than sleep: macOS hides the environment
// of its own platform binaries from kern.procargs2, and a wrapper a user runs
// is not one of those.
func TestReadAgentHintEnvFromALiveProcess(t *testing.T) {
	if os.Getenv("TUIOS_HINT_TEST_CHILD") == "1" {
		time.Sleep(5 * time.Second)
		return
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no process environment reader on this platform")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadAgentHintEnvFromALiveProcess$")
	cmd.Env = append(os.Environ(), "TUIOS_HINT_TEST_CHILD=1", "TUIOS_AGENT=Claude-Code ")
	if err := cmd.Start(); err != nil {
		t.Skipf("start sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := agentHint(readAgentHintEnv, cmd.Process.Pid); got == "claude-code" {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("agentHint = %q, want claude-code", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
