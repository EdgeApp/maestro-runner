package wda

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// The reported failure (#161): a field whose value reads "7000.00" contains
// "0" and beat the switch whose label is exactly "0".
func TestFilterBySelector_PrefersExactText(t *testing.T) {
	elements := []*ParsedElement{{Value: "7000.00"}, {Label: "0"}, {Label: "Limit"}}
	got := FilterBySelector(elements, flow.Selector{Text: "0"})
	if len(got) != 1 || got[0].Label != "0" {
		t.Fatalf("expected only the exact match, got %d", len(got))
	}
}

func TestFilterBySelector_ExactPlaceholderCounts(t *testing.T) {
	elements := []*ParsedElement{{Label: "Email address"}, {PlaceholderValue: "Email"}}
	got := FilterBySelector(elements, flow.Selector{Text: "Email"})
	if len(got) != 1 || got[0].PlaceholderValue != "Email" {
		t.Fatalf("exact placeholder should win, got %d", len(got))
	}
}

func TestFilterBySelector_FallsBackToContains(t *testing.T) {
	elements := []*ParsedElement{{Label: "Good till Cancel"}, {Label: "Cancel order"}}
	got := FilterBySelector(elements, flow.Selector{Text: "Cancel"})
	if len(got) != 2 {
		t.Fatalf("expected both contains matches to survive, got %d", len(got))
	}
}

// An exact text match must not bypass the rest of the selector (#157/#158).
func TestFilterBySelector_ExactTextStillRequiresID(t *testing.T) {
	elements := []*ParsedElement{
		{Name: "cart-button", Label: "Cart"},
		{Name: "product-item-Appium", Label: "product-item-Appium"},
	}
	sel := flow.Selector{ID: "cart-button", Text: "product-item-Appium"}
	if got := FilterBySelector(elements, sel); len(got) != 0 {
		t.Errorf("id and text are on different elements; expected no match, got %+v", got[0])
	}
}

func TestFilterBySelector_RegexUnaffectedByExactPreference(t *testing.T) {
	elements := []*ParsedElement{{Label: "7000.00"}, {Label: "0"}}
	got := FilterBySelector(elements, flow.Selector{Text: ".*000.*"})
	if len(got) != 1 || got[0].Label != "7000.00" {
		t.Fatalf("regex should match the price only, got %d", len(got))
	}
}
