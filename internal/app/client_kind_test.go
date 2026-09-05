package app

import (
	"fmt"
	"go/ast"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestClientKindDerivesTheClientFlags pins what each kind implies. The flags
// are what the code reads; the kind is what an entry point sets, and the table
// here is the one place the two are written down together.
func TestClientKindDerivesTheClientFlags(t *testing.T) {
	cfg := config.DefaultConfig()
	cases := []struct {
		kind                                                       ClientKind
		readOnly, remote, ssh, browser, graphicsRemote, forceGraph bool
	}{
		{ClientUnknown, false, false, false, false, false, false},
		{ClientLocal, false, false, false, false, false, false},
		{ClientSSH, true, true, true, false, true, false},
		{ClientBrowser, true, true, false, true, false, true},
	}
	for _, c := range cases {
		t.Run(c.kind.String(), func(t *testing.T) {
			opts := OSOptions{Client: c.kind, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)}
			o := NewOS(opts)
			opts.applyClientKind()
			if o.Client != c.kind {
				t.Errorf("Client = %v, want %v", o.Client, c.kind)
			}
			if o.ConfigReadOnly != c.readOnly {
				t.Errorf("ConfigReadOnly = %v, want %v", o.ConfigReadOnly, c.readOnly)
			}
			if o.RemoteClient != c.remote {
				t.Errorf("RemoteClient = %v, want %v", o.RemoteClient, c.remote)
			}
			if o.IsSSHMode != c.ssh {
				t.Errorf("IsSSHMode = %v, want %v", o.IsSSHMode, c.ssh)
			}
			if o.BrowserClient != c.browser {
				t.Errorf("BrowserClient = %v, want %v", o.BrowserClient, c.browser)
			}
			if opts.GraphicsRemoteClient != c.graphicsRemote {
				t.Errorf("GraphicsRemoteClient = %v, want %v", opts.GraphicsRemoteClient, c.graphicsRemote)
			}
			if opts.ForceGraphicsEnabled != c.forceGraph {
				t.Errorf("ForceGraphicsEnabled = %v, want %v", opts.ForceGraphicsEnabled, c.forceGraph)
			}
		})
	}
}

// TestClientKindKeepsAFlagSetByHand: a test that sets one flag directly keeps
// it. The kind adds, it does not overwrite.
func TestClientKindKeepsAFlagSetByHand(t *testing.T) {
	cfg := config.DefaultConfig()
	o := NewOS(OSOptions{Client: ClientLocal, ConfigReadOnly: true, UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	if !o.ConfigReadOnly {
		t.Error("the local kind cleared a ConfigReadOnly the caller set")
	}
}

// entryPointKinds is the kind each entry point's directory must name. A
// literal in one of these directories that names another kind, or none, is a
// client built by hand, which is what the kind exists to end.
var entryPointKinds = map[string]string{
	"cmd/tuios/":       "ClientLocal",
	"internal/server/": "ClientSSH",
	"cmd/tuios-web/":   "ClientBrowser",
}

// TestEveryEntryPointNamesItsClientKind holds every app.OSOptions literal
// outside this package to a Client kind, and to the right one for where it
// is. The sites come from the tree, so a fourth entry point written later is
// held to it too. The fuzz harness and the library wrapper build models of no
// fixed kind and are not entry points.
func TestEveryEntryPointNamesItsClientKind(t *testing.T) {
	fset, files := moduleSource(t)
	found := 0
	for path, file := range files {
		if strings.HasPrefix(path, "internal/app/") || strings.HasPrefix(path, "internal/fuzz/") || strings.HasPrefix(path, "pkg/") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "OSOptions" {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); !ok || ident.Name != "app" {
				return true
			}
			found++
			where := fmt.Sprintf("%s:%d", path, fset.Position(lit.Pos()).Line)
			want := ""
			for dir, kind := range entryPointKinds {
				if strings.HasPrefix(path, dir) {
					want = kind
				}
			}
			got := literalField(lit, "Client")
			switch {
			case got == "":
				t.Errorf("%s: app.OSOptions does not name a Client kind; the flags a kind implies are missing here", where)
			case want != "":
				if got != "app."+want {
					t.Errorf("%s: app.OSOptions names %s, this directory serves app.%s", where, got, want)
				}
			}
			return true
		})
	}
	if found < 6 {
		t.Fatalf("found %d app.OSOptions literals, expected the local, attach, tape, two SSH and two web ones", found)
	}
}

// literalField returns the source of the named field's value in a composite
// literal, or "" when it is not set.
func literalField(lit *ast.CompositeLit, field string) string {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		switch v := kv.Value.(type) {
		case *ast.Ident:
			return v.Name
		case *ast.SelectorExpr:
			if x, ok := v.X.(*ast.Ident); ok {
				return x.Name + "." + v.Sel.Name
			}
		}
		return "?"
	}
	return ""
}
