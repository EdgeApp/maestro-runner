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
