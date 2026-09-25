package main

import (
	"reflect"
	"testing"
)

// TestStartAgentEnvOmitsPathForAnotherMachine checks that start-agent does
// not send the PATH it adds on its own to a session on another machine. The
// far daemon refuses env over a link, so sending it made every
// `start-agent -s host:session` fail with no flag to turn it off.
func TestStartAgentEnvOmitsPathForAnotherMachine(t *testing.T) {
	pathOnly := map[string]string{"PATH": "/usr/bin"}
	withFlag := map[string]string{"PATH": "/usr/bin", "API_KEY": "k"}
	tests := []struct {
		name string
		opts startAgentOptions
		host string
		want map[string]string
	}{
		{"this machine sends the PATH", startAgentOptions{env: pathOnly}, "", pathOnly},
		{"this machine sends --env", startAgentOptions{env: withFlag, explicitEnv: true}, "", withFlag},
		{"another machine gets no PATH", startAgentOptions{env: pathOnly}, "buildbox", nil},
		{"another machine still gets --env, to refuse it plainly", startAgentOptions{env: withFlag, explicitEnv: true}, "buildbox", withFlag},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := startAgentEnv(tt.opts, tt.host); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("startAgentEnv(host %q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}
