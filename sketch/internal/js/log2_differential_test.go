//go:build differential

package js

import (
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The node script reads float64 inputs and writes Math.log2 of each, twice, the second
// time after the loop is hot, so that the optimizing compiler's code is checked as well.
const log2Script = `
const fs = require('fs');
const [,, input, output] = process.argv;
const buf = fs.readFileSync(input);
const xs = new Float64Array(buf.buffer, buf.byteOffset, buf.length / 8);
const out = new Float64Array(xs.length);
for (let round = 0; round < 2; round++) {
  for (let i = 0; i < xs.length; i++) out[i] = Math.log2(xs[i]);
}
fs.writeFileSync(output, Buffer.from(out.buffer));
`

// log2Inputs are the inputs of logInputs, every whole number up to 2^20, and every power
// of two with the doubles either side of it: packet takes the log2 of a whole count of bits
// and floors it, so a power of two must give its exponent exactly.
func log2Inputs() []float64 {
	xs := logInputs()
	for i := 1; i <= 1<<20; i++ {
		xs = append(xs, float64(i))
	}
	for k := -1074; k <= 1023; k++ {
		p := math.Ldexp(1, k)
		x, y := p, p
		for range 20 {
			xs = append(xs, x, y)
			x, y = math.Nextafter(x, 0), math.Nextafter(y, math.Inf(1))
		}
	}
	rng := rand.New(rand.NewPCG(3, 4))
	for range 200_000 {
		xs = append(xs, math.Floor(rng.Float64()*math.Ldexp(1, rng.IntN(60))))
	}
	return xs
}

// TestLog2AgainstNode checks Log2 against node bit for bit.
// Run it with node on the path: go test -tags differential -run TestLog2AgainstNode ./internal/js/
func TestLog2AgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	xs := log2Inputs()
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in"), filepath.Join(dir, "out"), filepath.Join(dir, "log2.cjs")
	writeFloats(t, in, xs)
	if err := os.WriteFile(script, []byte(log2Script), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, out).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	want := readFloats(t, out, len(xs))
	bad, goBad := 0, 0
	for i, x := range xs {
		if got := Log2(x); !same(got, want[i]) {
			if bad++; bad <= 10 {
				t.Errorf("Log2(%v) = %v, node %v", x, got, want[i])
			}
		}
		if !same(math.Log2(x), want[i]) {
			goBad++
		}
	}
	t.Logf("Log2: %d inputs, %d differences; Go's math.Log2 differs on %d", len(xs), bad, goBad)
}
