//go:build windows

package tmuxcompat

import "errors"

// waitFor is not available on Windows, where the shim does not run.
func (s *Shim) waitFor(name string, args []string) (string, []string, error) {
	if _, err := parseFlags(name, specs[name], args); err != nil {
		return OutcomeUnsupported, nil, err
	}
	return OutcomeUnsupported, nil, errors.New("wait-for: not supported on Windows")
}
