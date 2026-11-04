package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// A linked machine's files, through its daemon's file verbs (file-list,
// file-stat, file-read) over the link. The far machine decides what a link
// may read (its link policy), and refuses the rest with forbidden. Writes go
// through tuios cp and the transfers, never through these.

// remoteFSList lists a folder of the host.
func (c *Client) remoteFSList(p proto.FSListParams) (proto.FSList, error) {
	dir := p.Dir
	if dir == "" {
		dir = "~"
	}
	raw, err := c.verb("file-list", map[string]any{"dir": dir, "hidden": p.Hidden, "limit": maxEntries})
	if err != nil {
		return proto.FSList{}, err
	}
	var res struct {
		Dir     string `json:"dir"`
		Parent  string `json:"parent"`
		Home    string `json:"home"`
		Entries []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			LinkKind string `json:"link_kind"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return proto.FSList{}, err
	}
	out := proto.FSList{Dir: res.Dir, Parent: res.Parent, Home: res.Home, Entries: make([]proto.FSEntry, 0, len(res.Entries))}
	for _, e := range res.Entries {
		out.Entries = append(out.Entries, proto.FSEntry{Name: e.Name, Dir: e.Kind == "dir" || e.Kind == "symlink" && e.LinkKind == "dir"})
	}
	return out, nil
}

// remoteFSStat describes a path printed in a pane of the host: relative to
// the pane's folder unless absolute.
func (c *Client) remoteFSStat(p proto.FSStatParams) (proto.FSStat, error) {
	pth := strings.TrimSpace(p.Path)
	if !path.IsAbs(pth) && !strings.HasPrefix(pth, "~") && p.Base != "" {
		pth = path.Join(p.Base, pth)
	}
	raw, err := c.verb("file-stat", map[string]any{"path": pth})
	if err != nil {
		return proto.FSStat{Path: pth}, err
	}
	var res struct {
		Path   string `json:"path"`
		Exists bool   `json:"exists"`
		Size   int64  `json:"size"`
		Info   struct {
			Kind     string `json:"kind"`
			LinkKind string `json:"link_kind"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return proto.FSStat{Path: pth}, err
	}
	dir := res.Info.Kind == "dir" || res.Info.Kind == "symlink" && res.Info.LinkKind == "dir"
	return proto.FSStat{Path: res.Path, Exists: res.Exists, Dir: dir, Size: res.Size}, nil
}

// remoteFSRead reads a file of the host, at most p.Max bytes.
func (c *Client) remoteFSRead(p proto.FSReadParams) (proto.FSRead, error) {
	params := map[string]any{"path": p.Path}
	if p.Max > 0 {
		params["length"] = p.Max
	}
	raw, err := c.verb("file-read", params)
	if err != nil {
		return proto.FSRead{}, err
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return proto.FSRead{}, err
	}
	b, err := base64.StdEncoding.DecodeString(res.Content)
	return proto.FSRead{Data: b}, err
}

// errRemoteWrite answers a file write on a linked machine.
func (c *Client) errRemoteWrite() error {
	return fmt.Errorf("guios does not change files on %s from here. Copy files there with Copy to %s", c.host, c.host)
}

// HostInfo is what the app knows of the machine of the client's daemon:
// its OS, from the hello, and its home folder, which a link may read from
// file-list ("" when the far machine does not say).
func (c *Client) HostInfo() (goos, home string) {
	c.mu.Lock()
	if d := c.vc.Daemon(); d != nil {
		goos = d.OS
	}
	c.mu.Unlock()
	raw, err := c.verb("file-list", map[string]any{"dir": "~", "limit": 1})
	if err != nil {
		return goos, ""
	}
	var res struct {
		Dir string `json:"dir"`
	}
	_ = json.Unmarshal(raw, &res)
	return goos, res.Dir
}
