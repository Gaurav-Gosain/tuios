package config

import "testing"

func TestAnimationSpeedScalesDurations(t *testing.T) {
	cases := []struct {
		speed int
		want  int64 // ms for DefaultAnimationDuration
	}{
		{0, 300}, {100, 300}, {200, 150}, {50, 600}, {1000, 75}, {10, 1200},
	}
	for _, c := range cases {
		s := DefaultSettings()
		s.AnimationSpeed = c.speed
		if got := s.GetAnimationDuration().Milliseconds(); got != c.want {
			t.Errorf("speed %d: got %dms, want %dms", c.speed, got, c.want)
		}
		if got, want := s.GetFastAnimationDuration().Milliseconds(), c.want*2/3; got != want {
			t.Errorf("speed %d fast: got %dms, want %dms", c.speed, got, want)
		}
	}
}
