package tuie2e

import (
	"os"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

func knob(pairs ...string) string {
	return testknob.Env + "=" + testknob.Merge(os.Getenv(testknob.Env), pairs...)
}

func setKnobs(t *testing.T, pairs ...string) {
	t.Helper()
	t.Setenv(testknob.Env, testknob.Merge(os.Getenv(testknob.Env), pairs...))
}

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
	keep := out[:0]
	for i, kv := range out {
		if kv != "" || i == last {
			keep = append(keep, kv)
		}
	}
	return keep
}
