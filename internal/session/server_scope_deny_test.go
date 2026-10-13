package session

import (
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/adrg/xdg"
)

// A link to one server must not reach another server's state: the grants
// record, the saved sessions and the daemon log of every server are tuios's
// own files, whichever server the link arrived at. Before named servers the
// state deny entry was xdg.StateHome/tuios, and scoping xdg.StateHome moved it
// under tuios-servers/<name>, which would have opened the default server's
// folder to a link that reached a named one.
//
// The ways this can fail, written down first:
//   - the deny entry follows the scoped state home, so the default server's
//     folder is open to a named server's link;
//   - the tuios-servers folder is not denied, so a named server's folder is
//     open to the default server's link, or to another named server's;
//   - the read list and the write list disagree;
//   - the deny is so wide that an ordinary file beside them is refused.
func TestLinkDenyCoversEveryServersState(t *testing.T) {
	base := serverscope.BaseStateHome()
	t.Cleanup(func() { _ = serverscope.Apply("") })
	// The isolated test tree points every XDG folder at one place, which would
	// let the config folder's deny entry stand in for the state folder's. Each
	// gets a folder of its own inside the tree here.
	config, data := xdg.ConfigHome, xdg.DataHome
	xdg.ConfigHome, xdg.DataHome = filepath.Join(config, "config-home"), filepath.Join(data, "data-home")
	t.Cleanup(func() { xdg.ConfigHome, xdg.DataHome = config, data })

	own := []string{
		filepath.Join(base, "tuios", "sessions", "grants", "applied.json"),
		filepath.Join(base, serverscope.StateDirName, "foo", "tuios", "sessions", "grants", "applied.json"),
		filepath.Join(base, serverscope.StateDirName, "bar", "tuios", "daemon.log"),
	}
	ordinary := filepath.Join(base, "some-other-program", "history")

	for _, server := range []string{"", "foo"} {
		t.Run("server="+server, func(t *testing.T) {
			if err := serverscope.Apply(server); err != nil {
				t.Fatal(err)
			}
			for _, p := range own {
				rp := realPath(p)
				if !linkDenied(rp, "", linkPrivate{}) {
					t.Errorf("a link to server %q may write %s", server, p)
				}
				if !linkReadDenied(rp, "", linkPrivate{}) {
					t.Errorf("a link to server %q may read %s", server, p)
				}
			}
			rp := realPath(ordinary)
			if linkDenied(rp, "", linkPrivate{}) || linkReadDenied(rp, "", linkPrivate{}) {
				t.Errorf("a link to server %q is refused %s, which is not tuios's", server, ordinary)
			}
		})
	}
}
