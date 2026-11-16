package tuie2e

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

// The test switches of internal/testknob come in one variable,
// TUIOS_TEST_KNOBS. Two entries of one variable in an environment keep only
// the last, so a knob is always added to the ones this process already has
// (TestMain sets card_check for every daemon), and an environment with two
// entries is merged with withKnobs.

// knob is the TUIOS_TEST_KNOBS entry with the knobs of this process and
// pairs, each name=value.
func knob(pairs ...string) string {
	return testknob.Env + "=" + testknob.Merge(os.Getenv(testknob.Env), pairs...)
}

// setKnobs adds pairs to the knobs of this process for the test, so every
// tuios it starts gets them.
func setKnobs(t *testing.T, pairs ...string) {
	t.Helper()
	t.Setenv(testknob.Env, testknob.Merge(os.Getenv(testknob.Env), pairs...))
}

// withKnobs merges every TUIOS_TEST_KNOBS entry of env into one, in the
// place of the last, so no entry hides the knobs of another.
func withKnobs(env []string) []string {
	prefix := testknob.Env + "="
	merged, last := "", -1
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, prefix); ok {
			merged = testknob.Merge(merged, v)
			last = len(out)
			out = append(out, "")
			continue
		}
		out = append(out, kv)
	}
	if last < 0 {
		return env
	}
	out[last] = prefix + merged
	// Drop the places the earlier entries held.
	keep := out[:0]
	for i, kv := range out {
		if kv != "" || i == last {
			keep = append(keep, kv)
		}
	}
	return keep
}
