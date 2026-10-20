package transcriptview

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestMain isolates the whole test binary from the developer's own XDG
// directories. See testutil.RunIsolated for why this cannot be a per-test
// helper. With fakeOpenCodeEnv set, the binary is a fake opencode instead.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeOpenCodeEnv); mode != "" {
		fakeOpenCodeMain(mode)
		return
	}
	os.Exit(testutil.RunIsolated(m))
}

// fakeOpenCodeEnv makes the test binary act as opencode. "tree" starts a
// child, as opencode starts its private server, and hangs. "child" writes
// its pid to the file fakeOpenCodePIDEnv names and hangs.
const (
	fakeOpenCodeEnv    = "TUIOS_TEST_FAKE_OPENCODE"
	fakeOpenCodePIDEnv = "TUIOS_TEST_FAKE_OPENCODE_PID"
)

func fakeOpenCodeMain(mode string) {
	switch mode {
	case "tree":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), fakeOpenCodeEnv+"=child")
		if child.Start() != nil {
			os.Exit(2)
		}
	case "child":
		if os.WriteFile(os.Getenv(fakeOpenCodePIDEnv), []byte(strconv.Itoa(os.Getpid())), 0o600) != nil {
			os.Exit(2)
		}
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
