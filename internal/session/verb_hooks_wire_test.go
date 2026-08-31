package session

import (
	"reflect"
	"testing"
)

// The client's half of the hook table crosses the gob codec inside a
// map[string]any, which is where this codebase has been bitten before. gob
// carries the concrete type, and []map[string]any is registered, so what a
// caller gets back is not what an obvious type assertion expects. A verb that
// asserted only []any would drop every client row with no error at all, which
// is the exact failure list-hooks exists to prevent.

// TestClientHookRowsSurviveTheCodec sends the rows the way the client sends
// them and reads them back the way the verb reads them.
func TestClientHookRowsSurviveTheCodec(t *testing.T) {
	sent := []map[string]any{{
		"event":      "after-attach",
		"side":       "client",
		"command":    "banner.sh",
		"runs":       2,
		"last_exit":  1,
		"last_run":   "2026-08-31T21:16:32+04:00",
		"last_error": "no such file",
		"last_ms":    int64(7),
	}}

	codec := DefaultCodec()
	encoded, err := codec.Encode(&CommandResultPayload{
		Success: true,
		Data:    map[string]any{"type": "hook_list", "hooks": sent},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got CommandResultPayload
	if err := codec.Decode(encoded, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	rows := clientHookRows(got.Data["hooks"])
	if len(rows) != 1 {
		t.Fatalf("the codec delivered %d rows, want 1. The wire shape is %T",
			len(rows), got.Data["hooks"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("a row came back as %T, not a map", rows[0])
	}
	for key, want := range sent[0] {
		if !reflect.DeepEqual(row[key], want) {
			t.Errorf("row[%q] = %#v, want %#v", key, row[key], want)
		}
	}
}

// TestAnEmptyClientHookTableSurvivesTheCodec is the zero value across the
// socket. gob drops a nil map and drops an empty slice, so a client that holds
// no hooks at all must not make the verb fail or report rows it never sent.
func TestAnEmptyClientHookTableSurvivesTheCodec(t *testing.T) {
	codec := DefaultCodec()
	for _, tc := range []struct {
		name string
		sent []map[string]any
	}{
		{"a client with no hooks sends an empty slice", []map[string]any{}},
		{"a client with no table sends nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := codec.Encode(&CommandResultPayload{
				Success: true,
				Data:    map[string]any{"type": "hook_list", "hooks": tc.sent},
			})
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var got CommandResultPayload
			if err := codec.Decode(encoded, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if rows := clientHookRows(got.Data["hooks"]); len(rows) != 0 {
				t.Errorf("an empty table came back as %d rows", len(rows))
			}
		})
	}

	// And a result that carried no data at all, which is what a client that
	// failed the request sends.
	if rows := clientHookRows(nil); len(rows) != 0 {
		t.Errorf("a missing hooks field came back as %d rows", len(rows))
	}
}

// TestTheDaemonRowsAndTheClientRowsHaveTheSameKeys stops the two halves of one
// table describing themselves differently, which would make the listing
// unreadable the moment a client attached.
func TestTheDaemonRowsAndTheClientRowsHaveTheSameKeys(t *testing.T) {
	m := newHookTableForTest()
	daemon := m.Rows("session")
	client := m.Rows("client")
	if len(daemon) != 1 || len(client) != 1 {
		t.Fatalf("expected one row per side, got %d and %d", len(daemon), len(client))
	}
	for key := range daemon[0] {
		if _, ok := client[0][key]; !ok {
			t.Errorf("the client rows have no %q", key)
		}
	}
	if daemon[0]["side"] != "session" || client[0]["side"] != "client" {
		t.Errorf("side is not reported per half: %v and %v", daemon[0]["side"], client[0]["side"])
	}
}
