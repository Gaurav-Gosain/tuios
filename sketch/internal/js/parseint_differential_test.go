//go:build differential

package js

import (
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestParseIntAgainstNode compares ParseIntPrefix with parseInt(s, 10) in node on runs of
// digits of every length to 40, and on numbers just past 2^53.
// Run it with node on the path: go test -tags differential -run TestParseIntAgainstNode ./internal/js/
func TestParseIntAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	rng := rand.New(rand.NewPCG(9, 10))
	var inputs []string
	for range 200_000 {
		var b strings.Builder
		n := 1 + rng.IntN(40)
		for range n {
			b.WriteByte(byte('0' + rng.IntN(10)))
		}
		if rng.IntN(4) == 0 {
			b.WriteString(".5")
		}
		inputs = append(inputs, b.String())
	}
	for k := range 100_000 {
		inputs = append(inputs, strconv.FormatUint(1<<53+uint64(k), 10), strconv.FormatUint(1<<60-uint64(k), 10))
	}
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in"), filepath.Join(dir, "out"), filepath.Join(dir, "p.cjs")
	if err := os.WriteFile(in, []byte(strings.Join(inputs, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	const js = `const fs = require('fs');
const [,, input, output] = process.argv;
const xs = fs.readFileSync(input, 'utf8').split('\n');
const out = new Float64Array(xs.length);
xs.forEach((x, i) => { out[i] = parseInt(x, 10); });
fs.writeFileSync(output, Buffer.from(out.buffer));`
	if err := os.WriteFile(script, []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, out).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	res := readFloats(t, out, len(inputs))
	bad := 0
	for i, s := range inputs {
		if got := ParseIntPrefix(s, 10); math.Float64bits(got) != math.Float64bits(res[i]) {
			if bad++; bad <= 10 {
				t.Errorf("ParseIntPrefix(%q) = %v, node %v", s, got, res[i])
			}
		}
	}
	t.Logf("parseInt: %d inputs, %d differences", len(inputs), bad)
}
