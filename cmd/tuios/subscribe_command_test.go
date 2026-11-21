package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/adrg/xdg"
)

func startSubscribeDaemon(t *testing.T) *session.VerbClient {
	t.Helper()
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_RUNTIME_DIR", testutil.RuntimeDir(t))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	xdg.Reload()

	d := session.NewDaemon(&session.DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	c, err := session.DialVerbClientAs("test")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
