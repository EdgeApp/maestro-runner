package wda

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// Regex text selectors match ignoring case, as Maestro's do (IGNORE_CASE), and
// anchors still mean the whole string.
func TestMatchesTextHonoursAnchorsIgnoringCase(t *testing.T) {
	if !matchesText("^SIGN OUT$", "SIGN OUT") {
		t.Error("should match the text it was written for")
	}
	if !matchesText("^SIGN OUT$", "Sign out") {
		t.Error("a regex should match ignoring case, as in Maestro")
	}
	if matchesText("^SIGN OUT$", "Sign out?") {
		t.Error("an anchored pattern must not match a longer string")
	}
	if !matchesText("(let's get started!|Let's do it!)", "Let's get started!") {
		t.Error("an alternation should match ignoring case (duckduckgo/Android's onboarding flow)")
	}
}

// When both a "Sign out" row and a "SIGN OUT" button match `^SIGN OUT$`, the one
// in the pattern's own case comes first, so the tap lands on it (#151).
func TestFilterBySelectorPrefersExactCase(t *testing.T) {
	row := &ParsedElement{Label: "Sign out"}
	button := &ParsedElement{Label: "SIGN OUT"}
	got := FilterBySelector([]*ParsedElement{row, button}, flow.Selector{Text: "^SIGN OUT$"})
	if len(got) != 2 || got[0] != button {
		t.Fatalf("got %v, want the SIGN OUT button first, then the row", got)
	}
}

// Plain text is not a regex and keeps the case-insensitive contains behaviour
// flows already rely on — the change is scoped to patterns someone wrote
// deliberately as a regex.
func TestMatchesTextKeepsPlainTextCaseInsensitive(t *testing.T) {
	if !matchesText("sign out", "Sign Out") {
		t.Error("plain text should still match case-insensitively")
	}
	if !matchesText("SIGN", "Sign out") {
		t.Error("plain text should still match as a substring")
	}
}

// Case-sensitivity applies to the pattern as written; a regex asking for
// insensitivity can still say so.
func TestMatchesTextAllowsExplicitInsensitivity(t *testing.T) {
	if !matchesText("(?i)^sign out$", "SIGN OUT") {
		t.Error("an explicit (?i) should still be honoured")
	}
}
