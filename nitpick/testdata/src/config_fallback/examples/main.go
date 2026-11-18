package main

import "os"

// An example may fall back to a local server.
func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "localhost:8080"
	}
	println(addr)
}
