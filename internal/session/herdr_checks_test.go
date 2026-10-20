package session

import (
	"slices"
	"testing"
)

// TestHerdrMethodsNameTheirCheck holds the herdr socket to the rule the JSON
// verbs have: every method it answers names its check, and the table names
// no method the socket does not answer. A method missing here is refused at
// run time, so this test is what tells its author.
func TestHerdrMethodsNameTheirCheck(t *testing.T) {
	answered := herdrAnswered()
	for _, m := range answered {
		if herdrChecks[m] == herdrUnclassified {
			t.Errorf("herdr method %s has no entry in herdrChecks", m)
		}
	}
	for m := range herdrChecks {
		if !slices.Contains(answered, m) {
			t.Errorf("herdrChecks names %s, which the herdr socket does not answer", m)
		}
	}
}
