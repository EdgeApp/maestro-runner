package core

import (
	"reflect"
	"testing"
)

func TestMatchSelectorText(t *testing.T) {
	cases := []struct {
		pattern string
		values  []string
		want    bool
	}{
		{"sign in", []string{"Sign In"}, true},                           // plain: case-insensitive substring
		{"Sign", []string{"Please Sign In"}, true},                       // plain: substring
		{"line two", []string{"line\ntwo"}, true},                        // newline read as space
		{"Passwords.*", []string{"Passwords"}, true},                     // regex: whole value
		{"Passwords.*", []string{"Import Passwords from Google"}, false}, // regex: not a substring
		{"(let's get started!|x)", []string{"Let's get started!"}, true}, // regex: case-insensitive
		{"DDG.", []string{"Not DDG."}, false},                            // dotted plain: whole match only
		{"Protections.activated!", []string{"Protections activated!"}, true},
		{"a.*b", []string{"a\nb"}, true},       // dot matches newline
		{"([", []string{"has ([ in it"}, true}, // invalid regex falls back to substring
		{"", []string{"x"}, false},
		{"x", []string{""}, false},
	}
	for _, c := range cases {
		if got := MatchSelectorText(c.pattern, c.values...); got != c.want {
			t.Errorf("MatchSelectorText(%q, %q) = %v, want %v", c.pattern, c.values, got, c.want)
		}
	}
}

func TestMatchSelectorTextExactCase(t *testing.T) {
	if !MatchSelectorTextExactCase("^SIGN OUT$", "SIGN OUT") {
		t.Error("regex in its own case should match")
	}
	if MatchSelectorTextExactCase("^SIGN OUT$", "Sign out") {
		t.Error("regex in another case should not match exactly")
	}
	if !MatchSelectorTextExactCase("Login", "Login") || MatchSelectorTextExactCase("Login", "login") {
		t.Error("plain exact-case match wrong")
	}
	if MatchSelectorTextExactCase("([", "([") {
		t.Error("invalid regex should not match")
	}
}

func TestMatchSelectorID(t *testing.T) {
	cases := []struct {
		pattern, id string
		want        bool
	}{
		{"Flatlist", "FlatList", true},
		{`^auth\.login$`, "com.app:id/auth.login", true},
		{"login", "login_button", true},
		{"([", "a([b", true},
		{"x", "", false},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := MatchSelectorID(c.pattern, c.id); got != c.want {
			t.Errorf("MatchSelectorID(%q, %q) = %v, want %v", c.pattern, c.id, got, c.want)
		}
	}
}

func TestLooksLikeRegex(t *testing.T) {
	for text, want := range map[string]bool{
		"plain": false, "Mr. Smith": false, "a.*": true, "^start": true, "end$": true,
		"a|b": true, `\.`: true, `a\b`: false, "mid^dle": false, "(x)": true,
	} {
		if got := LooksLikeRegex(text); got != want {
			t.Errorf("LooksLikeRegex(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestLiteralNeedles(t *testing.T) {
	cases := map[string][]string{
		"Sign In":            {"sign", "in"},
		"line\ntwo":          {"line", "two"},
		"Welcome back.*":     {"welcome", "back"},
		`Total: \$10`:        {"total:", "$10"},
		"colou?r scheme":     {"r", "scheme"},
		"a|b":                nil,
		"(?i)hello world":    {"hello", "world"},
		"café?s menu":        {"s", "menu"},
		"x{100}yz tail":      {"yz", "tail"},
		"x.*":                nil,
		"":                   nil,
		"Terms of Service.+": {"terms", "of", "service"},
	}
	for pattern, want := range cases {
		if got := LiteralNeedles(pattern); !reflect.DeepEqual(got, want) {
			t.Errorf("LiteralNeedles(%q) = %q, want %q", pattern, got, want)
		}
	}
}
