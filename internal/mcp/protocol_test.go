package mcp

import (
	"slices"
	"testing"
)

const modernMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

// TestInitializeNegotiatesEachHandshakeVersion: a client that names a version
// from the handshake era gets it back, and one that names the stateless
// version, which has no initialize, gets the newest handshake version.
func TestInitializeNegotiatesEachHandshakeVersion(t *testing.T) {
	for _, v := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"} {
		c := startServer(t, Options{})
		res := c.rpc(1, "initialize", map[string]any{"protocolVersion": v})["result"].(map[string]any)
		if res["protocolVersion"] != v {
			t.Errorf("initialize %s answered %v", v, res["protocolVersion"])
		}
	}
	c := startServer(t, Options{})
	res := c.rpc(1, "initialize", map[string]any{"protocolVersion": "2026-07-28"})["result"].(map[string]any)
	if res["protocolVersion"] != "2025-11-25" {
		t.Errorf("initialize 2026-07-28 answered %v, want 2025-11-25", res["protocolVersion"])
	}
}

func TestServerDiscoverListsEveryVersion(t *testing.T) {
	c := startServer(t, Options{Name: "tuios", Version: "1"})
	res := c.rpc(1, "server/discover", nil)["result"].(map[string]any)
	var got []string
	for _, v := range res["supportedVersions"].([]any) {
		got = append(got, v.(string))
	}
	if !slices.Equal(got, []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}) {
		t.Errorf("supportedVersions = %v", got)
	}
	if res["resultType"] != "complete" || res["cacheScope"] == nil || res["ttlMs"] == nil {
		t.Errorf("discover result lacks resultType, ttlMs or cacheScope: %v", res)
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("capabilities = %v", res["capabilities"])
	}
}

// TestStatelessRequestsCarryResultType: without initialize, a request that
// names the version in _meta is answered, every result says resultType, and
// the list says how long it stays fresh.
func TestStatelessRequestsCarryResultType(t *testing.T) {
	f := &fakeDaemon{}
	c := startServer(t, Options{Verbs: testVerbs, Dial: f.dial})
	c.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + modernMeta + `}}`)
	res := c.next()["result"].(map[string]any)
	if res["resultType"] != "complete" || res["ttlMs"] != float64(0) || res["cacheScope"] != "private" {
		t.Errorf("tools/list result = %v", res)
	}
	if len(res["tools"].([]any)) == 0 {
		t.Error("no tools listed")
	}
	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tuios_list_windows","arguments":{},` + modernMeta + `}}`)
	call := c.next()["result"].(map[string]any)
	if call["resultType"] != "complete" {
		t.Errorf("tools/call result = %v", call)
	}
	if _, ok := call["structuredContent"]; !ok {
		t.Errorf("tools/call result lacks structuredContent: %v", call)
	}
}

func TestStatelessRequestErrors(t *testing.T) {
	c := startServer(t, Options{})
	c.send(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	if e := c.next()["error"].(map[string]any); e["code"] != float64(rpcInvalidParams) {
		t.Errorf("missing capabilities error = %v, want invalid params", e)
	}
	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1999-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`)
	e := c.next()["error"].(map[string]any)
	if e["code"] != float64(-32022) {
		t.Fatalf("unsupported version error = %v", e)
	}
	data := e["data"].(map[string]any)
	if data["requested"] != "1999-01-01" || len(data["supported"].([]any)) != 5 {
		t.Errorf("error data = %v", data)
	}
}

// TestBatchesAreRefusedFromTheVersionThatRemovedThem.
func TestBatchesAreRefusedFromTheVersionThatRemovedThem(t *testing.T) {
	batch := `[{"jsonrpc":"2.0","id":3,"method":"ping"}]`
	for _, v := range []string{"2025-11-25", "2025-06-18"} {
		c := startServer(t, Options{})
		c.rpc(1, "initialize", map[string]any{"protocolVersion": v})
		c.send(batch)
		if e := c.next()["error"].(map[string]any); e["code"] != float64(rpcInvalidRequest) {
			t.Errorf("%s: batch answer = %v, want invalid request", v, e)
		}
	}
	for _, v := range []string{"2025-03-26", "2024-11-05"} {
		c := startServer(t, Options{})
		c.rpc(1, "initialize", map[string]any{"protocolVersion": v})
		c.send(batch)
		if _, ok := c.next()["batch"]; !ok {
			t.Errorf("%s: batch not answered", v)
		}
	}
	c := startServer(t, Options{})
	c.send(`[{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{` + modernMeta + `}}]`)
	if e := c.next()["error"].(map[string]any); e["code"] != float64(rpcInvalidRequest) {
		t.Errorf("stateless batch answer = %v, want invalid request", e)
	}
}
