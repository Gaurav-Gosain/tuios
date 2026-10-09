package herdrplugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// helperEnv selects a helper mode. The modes run this test binary as a
// plugin's processes, for the tests in proc_test.go.
const helperEnv = "TUIOS_HERDRPLUGIN_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(testutil.RunIsolated(m))
	case "parent", "parent-exits":
		// Start a child that sleeps, write its pid, then sleep or exit.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=child")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		_ = os.WriteFile(os.Getenv("TUIOS_HERDRPLUGIN_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		if os.Getenv(helperEnv) == "parent-exits" {
			os.Exit(0)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "host":
		// A daemon: a runner with one plugin command, which never stops it.
		r := NewRunner()
		self, _ := os.Executable()
		_, perr := r.Start(Job{
			Plugin:  &Plugin{PluginID: "test.host", PluginRoot: filepath.Dir(self)},
			Command: []string{self},
			Env:     []string{helperEnv + "=parent", "TUIOS_HERDRPLUGIN_PIDFILE=" + os.Getenv("TUIOS_HERDRPLUGIN_PIDFILE")},
		})
		if perr != nil {
			os.Exit(4)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}
