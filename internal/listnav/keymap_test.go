package listnav

import "testing"

func TestDefaultKeysMatchTheShippedBindings(t *testing.T) {
	cases := []struct {
		key     string
		letters bool
		want    Motion
	}{
		{"up", false, Up}, {"ctrl+p", false, Up}, {"k", true, Up},
		{"down", false, Down}, {"ctrl+n", false, Down}, {"j", true, Down},
		{"pgup", false, PageUp}, {"pgdown", false, PageDown},
		{"home", false, Home}, {"g", true, Home},
		{"end", false, End}, {"G", true, End}, {"shift+g", true, End},
		// A list with a filter keeps its letters for the filter.
		{"k", false, None}, {"j", false, None}, {"g", false, None}, {"G", false, None},
		{"ctrl+j", false, None},
	}
	for _, c := range cases {
		if got := Keys(c.key, c.letters); got != c.want {
			t.Errorf("Keys(%q, %v) = %v, want %v", c.key, c.letters, got, c.want)
		}
		if got := (*Keymap)(nil).Keys(c.key, c.letters); got != c.want {
			t.Errorf("nil Keymap.Keys(%q, %v) = %v, want %v", c.key, c.letters, got, c.want)
		}
	}
}

func TestConfiguredKeysReplaceTheDefaultsOfTheirMotionOnly(t *testing.T) {
	k := NewKeymap(map[Motion][]string{
		Down: {"ctrl+j", "down"},
		Up:   {"ctrl+k", "up"},
	})
	for key, want := range map[string]Motion{
		"ctrl+j": Down, "down": Down, "ctrl+k": Up, "up": Up,
		// The rest of the keys stay where they were.
		"pgup": PageUp, "pgdown": PageDown, "home": Home, "end": End,
	} {
		if got := k.Keys(key, false); got != want {
			t.Errorf("Keys(%q) = %v, want %v", key, got, want)
		}
	}
	// ctrl+n and ctrl+p were Down's and Up's defaults and are handed back.
	for _, key := range []string{"ctrl+n", "ctrl+p"} {
		if got := k.Keys(key, false); got != None {
			t.Errorf("Keys(%q) = %v, want None once the action names other keys", key, got)
		}
	}
	// j and k belong to Down and Up too, so they go with them. Home and End keep
	// their letters.
	if got := k.Keys("j", true); got != None {
		t.Errorf(`Keys("j", true) = %v, want None`, got)
	}
	if got := k.Keys("g", true); got != Home {
		t.Errorf(`Keys("g", true) = %v, want Home`, got)
	}
}

// A configured key that is another motion's default goes to the configured
// motion, every time: map order must not decide it.
func TestAConfiguredKeyWinsOverAnotherMotionsDefault(t *testing.T) {
	for range 50 {
		k := NewKeymap(map[Motion][]string{Up: {"ctrl+n"}})
		if got := k.Keys("ctrl+n", false); got != Up {
			t.Fatalf(`Keys("ctrl+n") = %v, want Up`, got)
		}
	}
}

func TestAnActionSetToNoKeysHasNone(t *testing.T) {
	k := NewKeymap(map[Motion][]string{PageDown: {}})
	if got := k.Keys("pgdown", false); got != None {
		t.Errorf(`Keys("pgdown") = %v, want None`, got)
	}
	if got := k.Keys("pgup", false); got != PageUp {
		t.Errorf(`Keys("pgup") = %v, want PageUp`, got)
	}
}

func TestALetterKeyIsReadOnlyByAListWithNoFilter(t *testing.T) {
	k := NewKeymap(map[Motion][]string{Down: {"n"}})
	if got := k.Keys("n", false); got != None {
		t.Errorf(`Keys("n", false) = %v, want None: a filter owns its letters`, got)
	}
	if got := k.Keys("n", true); got != Down {
		t.Errorf(`Keys("n", true) = %v, want Down`, got)
	}
}
