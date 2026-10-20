//go:build differential

package sankey

import (
	"math"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestPullAgainstNode checks PULL against Math.pow(0.99, pass) in node, bit for bit. These six
// are the only arguments pele passes to Math.pow. Run it with node on the path:
// go test -tags differential -run TestPullAgainstNode ./internal/diagrams/sankey/
func TestPullAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	script := `const b = Buffer.alloc(8); for (let pass = 0; pass < ` + strconv.Itoa(PASSES) + `; pass++) { b.writeDoubleLE(Math.pow(0.99, pass)); console.log(b.readBigUInt64LE().toString(16)); }`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v: %s", err, out)
	}
	lines := strings.Fields(string(out))
	if len(lines) != PASSES {
		t.Fatalf("node printed %q", out)
	}
	goPow := 0
	for pass, line := range lines {
		bits, err := strconv.ParseUint(line, 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := math.Float64bits(PULL[pass]); got != bits {
			t.Errorf("PULL[%d] is %x, node %x", pass, got, bits)
		}
		if math.Float64bits(math.Pow(0.99, float64(pass))) != bits {
			goPow++
		}
	}
	t.Logf("%d powers match node; math.Pow differs from node on %d of them", PASSES, goPow)
}
