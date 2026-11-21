package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

var shippedGoFlags = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("sh", "../../scripts/goflags.sh").Output()
	return strings.TrimSpace(string(out)), err
})

func goBuild(args ...string) *exec.Cmd {
	flags, err := shippedGoFlags()
	if err != nil {
		return exec.Command("sh", "-c", `echo "scripts/goflags.sh: $1" >&2; exit 1`, "sh", err.Error())
	}
	cmd := exec.Command("go", append([]string{"build"}, args...)...)
	cmd.Env = append(os.Environ(), "GOFLAGS="+flags)
	return cmd
}
