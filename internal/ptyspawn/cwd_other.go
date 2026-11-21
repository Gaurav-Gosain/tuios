//go:build !linux && !darwin

package ptyspawn

func processCwd(int) (string, bool) { return "", false }
