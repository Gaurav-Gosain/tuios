package error_to_empty_success

import (
	"errors"
	"log"
	"os"
)

type T struct{ n int }

func check() error { return errors.New("x") }

func load() ([]byte, error) {
	b, err := os.ReadFile("x")
	if err != nil {
		return nil, nil // want "returns empty values and a nil error"
	}
	return b, nil
}

func count() (int, string, error) {
	_, err := os.Stat("x")
	if err != nil {
		return 0, "", nil // want "returns empty values"
	}
	return 1, "a", nil
}

func value() (T, bool, error) {
	if err := check(); err != nil {
		return T{}, false, nil // want "returns empty values"
	}
	return T{n: 1}, true, nil
}

func only() error {
	if err := check(); err != nil {
		return nil
	}
	return nil
}

func closure() {
	f := func() (int, error) {
		if err := check(); err != nil {
			return 0, nil // want "returns empty values"
		}
		return 1, nil
	}
	_, _ = f()
}

// Not reported: the error is passed on, logged, explained, or replaced by a
// real value, or the function has no error result.

func wrapped() ([]byte, error) {
	b, err := os.ReadFile("x")
	if err != nil {
		return nil, err
	}
	return b, nil
}

func logged() error {
	if err := check(); err != nil {
		log.Print(err)
		return nil
	}
	return nil
}

func explained() ([]byte, error) {
	b, err := os.ReadFile("x")
	if err != nil {
		// A missing file means an empty list.
		return nil, nil
	}
	return b, nil
}

func fallback() (string, error) {
	if err := check(); err != nil {
		return "default", nil
	}
	return "", nil
}

type errEmpty struct{}

func (errEmpty) Error() string { return "empty" }

func newError() (*T, error) {
	if err := check(); err != nil {
		return nil, errEmpty{}
	}
	return &T{}, nil
}

func noErrorResult() *T {
	if err := check(); err != nil {
		return nil
	}
	return &T{}
}
