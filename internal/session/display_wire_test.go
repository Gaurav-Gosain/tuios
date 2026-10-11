package session

import "testing"

// TestHelloDisplayWireCompat is a wire compatibility check for
// HelloPayload.Display. A daemon that predates the field must still read a
// new client's hello, and a new daemon must read an old client's hello as one
// with no display. gob matches fields by name, so the old shapes are spelled
// as local structs.
func TestHelloDisplayWireCompat(t *testing.T) {
	type oldHello struct {
		Version     string
		SSHAuthSock string
	}
	t.Run("new client, old daemon", func(t *testing.T) {
		data, err := encodePayload(&HelloPayload{
			Version:     "v1",
			SSHAuthSock: "/a",
			Display:     ClientDisplay{WaylandDisplay: "wayland-1", Display: ":0"},
		})
		if err != nil {
			t.Fatal(err)
		}
		var got oldHello
		if err := decodePayload(data, &got); err != nil {
			t.Fatalf("an old daemon cannot read the hello: %v", err)
		}
		if got.Version != "v1" || got.SSHAuthSock != "/a" {
			t.Fatalf("an old daemon read %+v", got)
		}
	})
	t.Run("old client, new daemon", func(t *testing.T) {
		data, err := encodePayload(&oldHello{Version: "v1", SSHAuthSock: "/a"})
		if err != nil {
			t.Fatal(err)
		}
		var got HelloPayload
		if err := decodePayload(data, &got); err != nil {
			t.Fatalf("a new daemon cannot read an old hello: %v", err)
		}
		if got.Version != "v1" || !got.Display.empty() {
			t.Fatalf("a new daemon read %+v", got)
		}
	})
	t.Run("round trip", func(t *testing.T) {
		want := ClientDisplay{WaylandDisplay: "/run/user/1000/wayland-1", XDGRuntimeDir: "/run/user/1000", Display: ":1", XAuthority: "/x"}
		data, err := encodePayload(&HelloPayload{Version: "v1", Display: want})
		if err != nil {
			t.Fatal(err)
		}
		var got HelloPayload
		if err := decodePayload(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Display != want {
			t.Fatalf("display = %+v, want %+v", got.Display, want)
		}
	})
}
