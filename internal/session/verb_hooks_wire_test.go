package session

import (
	"reflect"
	"testing"
)

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

	encoded, err := encodePayload(&CommandResultPayload{
		Success: true,
		Data:    map[string]any{"type": "hook_list", "hooks": sent},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got CommandResultPayload
	if err := decodePayload(encoded, &got); err != nil {
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
	for _, tc := range []struct {
		name string
		sent []map[string]any
	}{
		{"a client with no hooks sends an empty slice", []map[string]any{}},
		{"a client with no table sends nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := encodePayload(&CommandResultPayload{
				Success: true,
				Data:    map[string]any{"type": "hook_list", "hooks": tc.sent},
			})
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var got CommandResultPayload
			if err := decodePayload(encoded, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if rows := clientHookRows(got.Data["hooks"]); len(rows) != 0 {
				t.Errorf("an empty table came back as %d rows", len(rows))
			}
		})
	}

	if rows := clientHookRows(nil); len(rows) != 0 {
		t.Errorf("a missing hooks field came back as %d rows", len(rows))
	}
}
