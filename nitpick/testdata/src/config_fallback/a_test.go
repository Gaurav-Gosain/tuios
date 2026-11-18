package config_fallback

import "os"

// A test may fall back to a local server.
func testAddr() string {
	a := os.Getenv("TEST_ADDR")
	if a == "" {
		a = "localhost:1"
	}
	return a
}
