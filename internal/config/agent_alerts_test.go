package config

import (
	"testing"
)

func TestParseQuietHours(t *testing.T) {
	tests := []struct {
		in       string
		from, to int
		wantErr  bool
	}{
		{in: "", from: 0, to: 0},
		{in: "22:00-08:00", from: 22 * 60, to: 8 * 60},
		{in: " 09:30 - 17:45 ", from: 9*60 + 30, to: 17*60 + 45},
		{in: "22:00", wantErr: true},
		{in: "25:00-08:00", wantErr: true},
		{in: "22:60-08:00", wantErr: true},
		{in: "ten-eleven", wantErr: true},
	}
	for _, tc := range tests {
		from, to, err := ParseQuietHours(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseQuietHours(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if err == nil && (from != tc.from || to != tc.to) {
			t.Errorf("ParseQuietHours(%q) = %d,%d want %d,%d", tc.in, from, to, tc.from, tc.to)
		}
	}
}
