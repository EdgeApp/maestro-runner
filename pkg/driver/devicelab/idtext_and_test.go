package devicelab

import (
	"strings"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// TestBuildSelectors_IDAndTextAreANDed is the correctness guarantee behind the
// Android half of #130: a selector naming both an id and a text must match one
// element carrying both.
//
// Before this, the builder emitted resourceId-only strategies and then
// text-only ones as separate candidates, and tryFindElementFast returns on the
// first that finds anything — so the id matched, the text was never read, and a
// wrong text: passed green against the right element.
func TestBuildSelectors_IDAndTextAreANDed(t *testing.T) {
	strategies, err := buildSelectors(flow.Selector{ID: "boss.hp", Text: "7 misses"}, 5000)
	if err != nil {
		t.Fatalf("buildSelectors failed: %v", err)
	}
	if len(strategies) == 0 {
		t.Fatal("expected at least one strategy")
	}

	for _, s := range strategies {
		hasID := strings.Contains(s.Value, "resourceId")
		hasText := strings.Contains(s.Value, "text") ||
			strings.Contains(s.Value, "description") ||
			strings.Contains(s.Value, "hint")
		if !hasID || !hasText {
			t.Errorf("every strategy must constrain both id and text, got: %s", s.Value)
		}
	}
}

// The single-attribute paths are unchanged: given only one of the two, the
// builder still emits the queries it always did.
func TestBuildSelectors_SingleAttributeUnchanged(t *testing.T) {
	idOnly, err := buildSelectors(flow.Selector{ID: "boss.hp"}, 5000)
	if err != nil {
		t.Fatalf("buildSelectors failed: %v", err)
	}
	// Exact resourceId, then the substring fallback.
	if len(idOnly) != 2 {
		t.Errorf("expected 2 id strategies, got %d", len(idOnly))
	}
	for _, s := range idOnly {
		if strings.Contains(s.Value, "textContains") || strings.Contains(s.Value, "textMatches") {
			t.Errorf("id-only selector must not constrain text, got: %s", s.Value)
		}
	}

	textOnly, err := buildSelectors(flow.Selector{Text: "7 misses"}, 5000)
	if err != nil {
		t.Fatalf("buildSelectors failed: %v", err)
	}
	// text/description/hint matched whole, then containing it, then the
	// same three case-insensitively.
	if len(textOnly) != 9 {
		t.Errorf("expected 9 text strategies, got %d", len(textOnly))
	}
	for _, s := range textOnly {
		if strings.Contains(s.Value, "resourceId") {
			t.Errorf("text-only selector must not constrain id, got: %s", s.Value)
		}
	}
}

// A tap prefers a clickable element, and that preference has to survive the
// combination rather than being dropped along with the separate strategies.
func TestBuildSelectorsForTap_CombinedKeepsClickableFirst(t *testing.T) {
	strategies, err := buildSelectorsForTap(flow.Selector{ID: "boss.hp", Text: "7 misses"}, 5000)
	if err != nil {
		t.Fatalf("buildSelectorsForTap failed: %v", err)
	}
	if len(strategies) == 0 {
		t.Fatal("expected at least one strategy")
	}
	if !strings.Contains(strategies[0].Value, "clickable(true)") {
		t.Errorf("expected a clickable-first strategy, got: %s", strategies[0].Value)
	}
	if !strings.Contains(strategies[0].Value, "resourceId") ||
		!strings.Contains(strategies[0].Value, `textMatches("(?is)\Q7 misses\E")`) {
		t.Errorf("clickable strategy must still carry both id and text, got: %s", strategies[0].Value)
	}
}

// A plain text selector tries the whole text before a substring, and a tap
// tries every whole-text strategy, clickable or not, before any substring
// one: an address bar showing ".../registration-username" must not beat the
// form label that is exactly "Username".
func TestTapStrategies_ExactTextFirst(t *testing.T) {
	clickable, err := buildClickableOnlyStrategies(flow.Selector{Text: "Username"})
	if err != nil {
		t.Fatal(err)
	}
	all, err := buildSelectors(flow.Selector{Text: "Username"}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	strategies := exactTextFirst(append(clickable, all...))
	sawSubstring := false
	exactCount := 0
	for _, s := range strategies {
		if isExactTextStrategy(s) {
			exactCount++
			if sawSubstring {
				t.Fatalf("whole-text strategy after a substring one: %s", s.Value)
			}
		} else {
			sawSubstring = true
		}
	}
	if exactCount != 6 {
		t.Errorf("expected 6 whole-text strategies (3 clickable, 3 any), got %d", exactCount)
	}
	if !strings.Contains(strategies[0].Value, `textMatches("(?is)\QUsername\E").clickable(true)`) {
		t.Errorf("first strategy should be the clickable whole-text match, got %s", strategies[0].Value)
	}

	// A regex selector keeps its own whole-match patterns only.
	regex, _ := buildSelectors(flow.Selector{Text: "Passwords.*"}, 5000)
	for _, s := range regex {
		if isExactTextStrategy(s) {
			t.Errorf("regex selector got a literal whole-text strategy: %s", s.Value)
		}
	}
}
