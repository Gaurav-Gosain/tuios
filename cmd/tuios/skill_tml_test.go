package main

import (
	"regexp"
	"testing"

	"tuios.dev/tml"
)

// TestSkillTMLBlocksValidate checks every TML block of the tml topic as
// tml-open would. An agent copies these blocks, so one that fails would teach
// the error the topic exists to prevent.
func TestSkillTMLBlocksValidate(t *testing.T) {
	text := skillText(t, "tml")
	blocks := regexp.MustCompile("(?s)```xml\n(.*?)```").FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		t.Fatal("the tml topic has no xml block to check")
	}
	lim := tml.DefaultLimits()
	for i, b := range blocks {
		if _, problems, err := tml.Check([]byte(b[1]), &tml.ValidateOptions{Limits: &lim}); err != nil || len(problems) > 0 {
			t.Errorf("block %d of the tml topic does not validate cleanly: %v %v", i+1, err, problems)
		}
	}
}
