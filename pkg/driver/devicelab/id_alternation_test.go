package devicelab

import (
	"strings"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// An id written as a regex alternation keeps both alternatives inside the
// wildcards (duckduckgo/Android's `id: "omnibarTextInput|inputField"`).
func TestIDAlternationStaysGrouped(t *testing.T) {
	strategies, err := buildSelectorsWithOptions(flow.Selector{ID: "omnibarTextInput|inputField"}, 0, false)
	if err != nil {
		t.Fatalf("buildSelectorsWithOptions: %v", err)
	}
	want := `resourceIdMatches(".*(?:omnibarTextInput|inputField).*")`
	for _, s := range strategies {
		if strings.Contains(s.Value, want) {
			return
		}
	}
	t.Errorf("no strategy contains %s", want)
}

// A regex text selector is tried with its case as written first, then
// ignoring case as Maestro matches: `(let's get started!|...)` must still find
// "Let's get started!", and `^SIGN OUT$` must still prefer "SIGN OUT" (#151).
func TestRegexTextTriesExactCaseThenIgnoreCase(t *testing.T) {
	strategies, err := buildSelectorsWithOptions(flow.Selector{Text: "(let's get started!|Let's do it!)"}, 0, false)
	if err != nil {
		t.Fatalf("buildSelectorsWithOptions: %v", err)
	}
	exact, ignore := -1, -1
	for i, s := range strategies {
		if exact < 0 && strings.Contains(s.Value, `textMatches("(?s)(let's`) {
			exact = i
		}
		if ignore < 0 && strings.Contains(s.Value, `textMatches("(?is)(let's`) {
			ignore = i
		}
	}
	if exact < 0 || ignore < 0 || exact > ignore {
		t.Fatalf("want an exact-case textMatches before an ignore-case one; got exact=%d ignore=%d", exact, ignore)
	}
}
