package main

import (
	"strings"
	"testing"
)

func TestCheckTransferShapeRefusesWhatBundleWorktreeCannotSend(t *testing.T) {
	good := bundleReply{Token: "t", Branch: "feat/x", Head: strings.Repeat("a", 40), BaseCommit: strings.Repeat("b", 40), BundleBytes: 10, PatchBytes: 5, Size: 15}
	if err := checkTransferShape(good); err != nil {
		t.Fatalf("a good reply was refused: %v", err)
	}
	bad := map[string]func(r *bundleReply){
		"branch with a colon":  func(r *bundleReply) { r.Branch = "a:refs/heads/main" },
		"branch as an option":  func(r *bundleReply) { r.Branch = "-x" },
		"head as an option":    func(r *bundleReply) { r.Head = "--upload-pack=x" },
		"short head":           func(r *bundleReply) { r.Head = "abc" },
		"base not a hash":      func(r *bundleReply) { r.BaseCommit = "main" },
		"sizes do not add up":  func(r *bundleReply) { r.Size = 99 },
		"past the cap":         func(r *bundleReply) { r.BundleBytes = pullMaxBytes; r.Size = pullMaxBytes + 5 },
		"no token to read on":  func(r *bundleReply) { r.Token = "" },
		"negative patch bytes": func(r *bundleReply) { r.PatchBytes = -5; r.BundleBytes = 20 },
	}
	for name, mutate := range bad {
		r := good
		mutate(&r)
		if err := checkTransferShape(r); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
