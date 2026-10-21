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

// The node script reads float64 inputs and writes Math.acos of each, twice, the second
// time after the loop is hot, so that the optimizing compiler's code is checked as well.
const acosScript = `
const fs = require('fs');
const [,, input, output] = process.argv;
const buf = fs.readFileSync(input);
const xs = new Float64Array(buf.buffer, buf.byteOffset, buf.length / 8);
const out = new Float64Array(xs.length);
for (let round = 0; round < 2; round++) {
  for (let i = 0; i < xs.length; i++) out[i] = Math.acos(xs[i]);
}
fs.writeFileSync(output, Buffer.from(out.buffer));
`

func acosInputs() []float64 {
	rng := rand.New(rand.NewPCG(7, 8))
	var xs []float64
	// Every exponent, any mantissa.
	for range 200_000 {
		xs = append(xs, randomBits(rng))
	}
	// The domain, evenly, and near its ends and its middle.
	for range 1_000_000 {
		xs = append(xs, 2*rng.Float64()-1)
	}
	for range 200_000 {
		d := math.Ldexp(rng.Float64(), -rng.IntN(60))
		xs = append(xs, 1-d, d-1, d, -d)
	}
	// A few units in the last place either way of every bound fdlibm branches on.
	for _, b := range []float64{0.5, 1, math.Ldexp(1, -57), math.Float64frombits(0x3c60000000000000)} {
		lo, hi := b, b
		for range 500 {
			xs = append(xs, lo, hi, -lo, -hi)
			lo, hi = math.Nextafter(lo, 0), math.Nextafter(hi, math.Inf(1))
		}
	}
	// What venn asks for: a cosine from the sides of a triangle of circles.
	for range 300_000 {
		d := 1 + 300*rng.Float64()
		r1, r2 := 1+200*rng.Float64(), 1+200*rng.Float64()
		xs = append(xs, (d*d+r1*r1-r2*r2)/(2*d*r1))
	}
	xs = append(xs, 0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), math.NaN(), 1, -1, 2, -2)
	return xs
}

// TestAcosAgainstNode checks Acos against node bit for bit. Go's math.Acos is the
// negative control: it must differ from node somewhere, or the inputs do not reach what
// the port is for.
// Run it with node on the path: go test -tags differential -run TestAcosAgainstNode ./internal/js/
func TestAcosAgainstNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on the path")
	}
	xs := acosInputs()
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in"), filepath.Join(dir, "out"), filepath.Join(dir, "acos.cjs")
	writeFloats(t, in, xs)
	if err := os.WriteFile(script, []byte(acosScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, script, in, out).CombinedOutput(); err != nil {
		t.Fatalf("node: %v: %s", err, msg)
	}
	res := readFloats(t, out, len(xs))
	bad, goBad := 0, 0
	for i, x := range xs {
		if got, want := Acos(x), res[i]; !same(got, want) {
			if bad++; bad <= 10 {
				t.Errorf("Acos(%v) = %v, node %v", x, got, want)
			}
		}
		if !same(math.Acos(x), res[i]) {
			goBad++
		}
	}
	t.Logf("Acos: %d inputs, %d differences. Go's math, the control: %d differences", len(xs), bad, goBad)
	if goBad == 0 {
		t.Error("Go's math agrees with node everywhere: the inputs do not test the port")
	}
}
