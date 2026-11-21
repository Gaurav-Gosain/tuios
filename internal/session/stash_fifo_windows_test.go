//go:build windows

package session

import "errors"

func makeFIFO(string) error { return errors.New("no fifos on windows") }
