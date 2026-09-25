package main

import (
	"testing"
)

// TestFanCallerEnvSendsPathAndWhatWasAskedFor: PATH always goes, NAME takes
// this process's value, NAME=VALUE sets one, and a NAME this process does not
// have is refused rather than sent empty.
func TestFanCallerEnvSendsPathAndWhatWasAskedFor(t *testing.T) {
	vars := map[string]string{"PATH": "/opt/bin:/usr/bin", "ANTHROPIC_API_KEY": "k", "EMPTY": ""}
	getenv := func(k string) string { return vars[k] }
	environ := []string{"PATH=/opt/bin:/usr/bin", "ANTHROPIC_API_KEY=k", "EMPTY="}
	env, err := fanCallerEnv([]string{"ANTHROPIC_API_KEY", "MODE=fast", "EMPTY"}, getenv, environ)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"PATH": "/opt/bin:/usr/bin", "ANTHROPIC_API_KEY": "k", "MODE": "fast", "EMPTY": ""}
	if len(env) != len(want) {
		t.Errorf("env = %v, want %v", env, want)
	}
	for k, v := range want {
		if got, ok := env[k]; !ok || got != v {
			t.Errorf("env[%s] = %q, want %q", k, got, v)
		}
	}
	if _, err := fanCallerEnv([]string{"NOT_SET_HERE"}, getenv, environ); err == nil {
		t.Error("an --env name this shell does not have was sent")
	}
}
