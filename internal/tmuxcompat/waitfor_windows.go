//go:build windows

package tmuxcompat

import "errors"

func (s *Shim) waitFor(name string, args []string) (string, []string, error) {
	if _, err := parseFlags(name, specs[name], args); err != nil {
		return OutcomeUnsupported, nil, err
	}
	return OutcomeUnsupported, nil, errors.New("wait-for: not supported on Windows")
}
