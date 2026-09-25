package config

import (
	"testing"
)

// loadFromTOML parses text the way LoadUserConfig does, without the XDG lookup:
// unmarshal, then fill the defaults in. That order is the whole point of the
// encoding, so a test that skipped the fill would prove nothing, and it is the
// one parse function's order rather than a copy of it.
func loadFromTOML(t *testing.T, text string) *UserConfig {
	t.Helper()
	cfg, err := ParseUserConfig([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cfg
}
