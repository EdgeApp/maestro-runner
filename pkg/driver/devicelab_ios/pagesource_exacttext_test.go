package devicelab_ios

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// preferExactText mirrors preferExactID for text (#161): when a literal text
// selector matches both an exact node and a superset, keep the exact one.
func TestPreferExactText(t *testing.T) {
	price := SnapshotNode{Value: "7000.00"}
	zero := SnapshotNode{Label: "0"}
	hits := []SnapshotNode{price, zero}

	t.Run("exact wins over substring superset", func(t *testing.T) {
		got := preferExactText(hits, flow.Selector{Text: "0"})
		if len(got) != 1 || got[0].Label != "0" {
			t.Errorf("got %d hits, want only the exact '0'", len(got))
		}
	})
	t.Run("placeholder counts as text", func(t *testing.T) {
		got := preferExactText([]SnapshotNode{{Label: "Email address"}, {PlaceholderValue: "Email"}}, flow.Selector{Text: "Email"})
		if len(got) != 1 || got[0].PlaceholderValue != "Email" {
			t.Errorf("got %d hits, want the exact placeholder", len(got))
		}
	})
	t.Run("no exact match keeps lenient set", func(t *testing.T) {
		got := preferExactText([]SnapshotNode{{Label: "Good till Cancel"}, {Label: "Cancel order"}}, flow.Selector{Text: "Cancel"})
		if len(got) != 2 {
			t.Errorf("got %d hits, want both contains matches", len(got))
		}
	})
	t.Run("regex text is untouched", func(t *testing.T) {
		if got := preferExactText(hits, flow.Selector{Text: ".*0.*"}); len(got) != 2 {
			t.Errorf("regex should keep all matches, got %d", len(got))
		}
	})
	t.Run("single hit untouched", func(t *testing.T) {
		if got := preferExactText([]SnapshotNode{price}, flow.Selector{Text: "0"}); len(got) != 1 {
			t.Errorf("got %d, want the single hit kept", len(got))
		}
	})
}
