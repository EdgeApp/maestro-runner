package uiautomator2

import "testing"

// A regex text selector must match the whole string, as in Maestro:
// `Passwords.*` is the "Passwords" row, not "Import Passwords from Google".
func TestMatchesTextRegexMatchesWholeString(t *testing.T) {
	cases := []struct {
		pattern, text string
		want          bool
	}{
		{"Passwords.*", "Import Passwords from Google", false},
		{"Passwords.*", "Passwords", true},
		{"Passwords.*", "passwords & Autofill", true},
		{"Last updated.*", "Last updated\nyesterday", true},
		{"(Sign in|Log in)", "Sign in with Google", false},
		{".*Google", "Import Passwords from Google", true},
		// A lone dot is a wildcard too, and plain text still matches by contains.
		{"Protections.activated!", "Protections activated!", true},
		{"Protections.activated!", "Protections activated! Search privately", false},
		// A dotted selector matches whole, as in Maestro: no substring.
		{"Mr. Smith", "Mr. Smith", true},
		{"Mr. Smith", "Hello Mr. Smith", false},
		{"DDG.", "Not DDG.\n", false},
		{"DDG.", "DDG!", true},
	}
	for _, c := range cases {
		if got := matchesText(c.pattern, c.text, "", ""); got != c.want {
			t.Errorf("matchesText(%q, %q) = %v, want %v", c.pattern, c.text, got, c.want)
		}
	}
}
