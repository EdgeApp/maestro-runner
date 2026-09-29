package devicelab

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// The reported failure (#161): a price field reading "7000.00" contains "0"
// and beat the switch whose text is exactly "0", so the tap landed wrong.
func TestFilterBySelector_PrefersExactText(t *testing.T) {
	elements := []*ParsedElement{{Text: "7000.00", Displayed: true}, {Text: "0", Displayed: true}, {Text: "Limit", Displayed: true}}
	got := FilterBySelector(elements, flow.Selector{Text: "0"})
	if len(got) != 1 || got[0].Text != "0" {
		t.Fatalf("expected only the exact match, got %d", len(got))
	}
}

func TestFilterBySelector_ExactContentDescCounts(t *testing.T) {
	elements := []*ParsedElement{{ContentDesc: "Add to cart", Displayed: true}, {ContentDesc: "Add", Displayed: true}}
	got := FilterBySelector(elements, flow.Selector{Text: "Add"})
	if len(got) != 1 || got[0].ContentDesc != "Add" {
		t.Fatalf("exact content-desc should win, got %d", len(got))
	}
}

// No element's text IS the pattern, so the substring behaviour flows rely on
// stands.
func TestFilterBySelector_FallsBackToContains(t *testing.T) {
	elements := []*ParsedElement{{Text: "Good till Cancel", Displayed: true}, {Text: "Cancel order", Displayed: true}}
	got := FilterBySelector(elements, flow.Selector{Text: "Cancel"})
	if len(got) != 2 {
		t.Fatalf("expected both contains matches to survive, got %d", len(got))
	}
}

// The guard on the fix: an exact text match must not bypass the rest of the
// selector — that is the OR behaviour removed in #157/#158/#160.
func TestFilterBySelector_ExactTextStillRequiresID(t *testing.T) {
	elements := []*ParsedElement{
		{ResourceID: "cart-button", Text: "Cart", Displayed: true},
		{ResourceID: "product-item-Appium", Text: "product-item-Appium", Displayed: true},
	}
	sel := flow.Selector{ID: "cart-button", Text: "product-item-Appium"}
	if got := FilterBySelector(elements, sel); len(got) != 0 {
		t.Errorf("id and text are on different elements; expected no match, got %+v", got[0])
	}
}

// A regex selector is not a literal and keeps regex semantics.
func TestFilterBySelector_RegexUnaffectedByExactPreference(t *testing.T) {
	elements := []*ParsedElement{{Text: "7000.00", Displayed: true}, {Text: "0", Displayed: true}}
	got := FilterBySelector(elements, flow.Selector{Text: ".*000.*"})
	if len(got) != 1 || got[0].Text != "7000.00" {
		t.Fatalf("regex should match the price only, got %d", len(got))
	}
}
