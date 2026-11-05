//go:build !linux && !darwin && !windows

package service

// Render, Install and the rest need systemd, launchd or Task Scheduler.
// Elsewhere a client starts the daemon when it needs one.

func Render(Spec) (string, string, error)    { return "", "", ErrUnsupported }
func ManagerEnv() (map[string]string, error) { return nil, ErrUnsupported }
func Install(Spec, bool) error               { return ErrUnsupported }
func Uninstall(string) error                 { return nil }
func Serves(string, string) bool             { return false }
func Start(string) error                     { return ErrUnsupported }
func Get(name string) Status                 { return Status{Name: name, Linger: true} }
func Lingering(string) bool                  { return true }
func UsesLinger() bool                       { return false }

func unescapeMarker(s string) string { return s }
