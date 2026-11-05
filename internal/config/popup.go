package config

import (
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// PopupDim is an optional popup dim override. A pointer to this struct keeps
// an explicit zero distinct from absence in gob, which flattens *int(0) to an
// omitted field. JSON still carries the override as a plain integer.
type PopupDim struct {
	Percent int
}

// MarshalJSON writes the percentage, rather than the wire presence wrapper.
func (d PopupDim) MarshalJSON() ([]byte, error) { return jsonx.Marshal(d.Percent) }

// UnmarshalJSON reads an integer percentage. A nil *PopupDim means inherit.
func (d *PopupDim) UnmarshalJSON(data []byte) error { return jsonx.Unmarshal(data, &d.Percent) }
