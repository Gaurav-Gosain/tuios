package main

// kerodStale reports a daemon older than this app. The tuios daemon tells
// its clients itself (tuios daemon-upgrade), so guios never asks.
func kerodStale(app, fingerprint string) bool { return false }
