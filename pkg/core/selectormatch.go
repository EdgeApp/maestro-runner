package core

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Selector text and id matching with Maestro's semantics, shared so drivers
// that receive raw candidates from a device agent all decide alike.
//
// Text: a pattern with regex syntax must match a whole value, case-insensitive,
// with "." matching newlines (Maestro compiles with IGNORE_CASE and
// DOT_MATCHES_ALL and calls Regex.matches). A value also matches with its line
// breaks read as spaces. Every pattern is such a regex, plain text included, so
// "Example" does not match "Examples" (the substring match the other
// maestro-runner drivers still use is the open #161 question). An invalid
// regex is compared as literal text, whole and case-insensitive.
//
// ID: a case-insensitive regex, matched anywhere, also against the part after a
// "/" package prefix; an invalid regex falls back to a substring.

// MatchSelectorText reports whether pattern matches any of the values.
func MatchSelectorText(pattern string, values ...string) bool {
	if pattern == "" {
		return false
	}
	re, err := regexp.Compile(`(?is)\A(?:` + pattern + `)\z`)
	if err != nil {
		re = regexp.MustCompile(`(?is)\A` + regexp.QuoteMeta(pattern) + `\z`)
	}
	for _, v := range values {
		if v == "" {
			continue
		}
		flat := strings.ReplaceAll(v, "\n", " ")
		if re.MatchString(v) || re.MatchString(flat) || v == pattern || flat == pattern {
			return true
		}
	}
	return false
}

// MatchSelectorTextExactCase reports whether pattern matches a value in its
// own case, so of several matches the one written as in the selector wins.
func MatchSelectorTextExactCase(pattern string, values ...string) bool {
	if LooksLikeRegex(pattern) {
		re, err := regexp.Compile(`(?s)\A(?:` + pattern + `)\z`)
		if err != nil {
			return false
		}
		for _, v := range values {
			if v != "" && (re.MatchString(v) || re.MatchString(strings.ReplaceAll(v, "\n", " "))) {
				return true
			}
		}
		return false
	}
	for _, v := range values {
		if v == pattern || strings.ReplaceAll(v, "\n", " ") == pattern {
			return true
		}
	}
	return false
}

// MatchSelectorID reports whether an id selector matches identifier.
func MatchSelectorID(pattern, identifier string) bool {
	if pattern == "" || identifier == "" {
		return false
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return strings.Contains(strings.ToLower(identifier), strings.ToLower(pattern))
	}
	return re.MatchString(identifier) || re.MatchString(identifier[strings.LastIndex(identifier, "/")+1:])
}

// LooksLikeRegex reports whether text uses regex syntax beyond a lone dot.
func LooksLikeRegex(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if i > 0 && text[i-1] == '\\' {
			switch c {
			case '.', '*', '+', '?', '[', ']', '{', '}', '|', '(', ')', '^', '$', '\\':
				return true
			}
			continue
		}
		switch c {
		case '.':
			if i+1 < len(text) && (text[i+1] == '*' || text[i+1] == '+' || text[i+1] == '?') {
				return true
			}
		case '*', '+', '?', '[', ']', '{', '}', '|', '(', ')':
			return true
		case '^':
			if i == 0 {
				return true
			}
		case '$':
			if i == len(text)-1 {
				return true
			}
		}
	}
	return false
}

// LiteralNeedles returns lowercase substrings every match of pattern must
// contain, for coarse filtering on a device: the words of the pattern when
// plain, otherwise the words of its longest regex-free run (nil when there is
// none worth using). Words, not the phrase, because a match may break a line
// where the pattern has a space.
func LiteralNeedles(pattern string) []string {
	if pattern == "" {
		return nil
	}
	if !LooksLikeRegex(pattern) && !strings.Contains(pattern, ".") {
		return strings.Fields(strings.ToLower(pattern))
	}
	if strings.ContainsAny(pattern, "|[") {
		// Alternation and classes make any run optional or unknown.
		return nil
	}
	best, cur := "", []byte{}
	flush := func() {
		if len(cur) > len(best) {
			best = string(cur)
		}
		cur = cur[:0]
	}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '\\' && i+1 < len(pattern):
			i++
			if n := pattern[i]; (n >= 'a' && n <= 'z') || (n >= 'A' && n <= 'Z') || (n >= '0' && n <= '9') {
				flush() // \d, \s, \w…: a class, not a literal
			} else {
				cur = append(cur, n) // an escaped metacharacter is itself
			}
		case c == '*' || c == '?' || c == '{':
			// The preceding character is optional (a {n,m} count may be 0).
			if _, size := utf8.DecodeLastRune(cur); size > 0 {
				cur = cur[:len(cur)-size]
			}
			flush()
			if c == '{' {
				i = skipTo(pattern, i, '}')
			}
		case c == '(' && i+1 < len(pattern) && pattern[i+1] == '?':
			// (?i), (?:…), (?P<name>…): skip the group's head, or all of it.
			flush()
			i = skipTo(pattern, i, ')')
		case strings.IndexByte(".+}()^$", c) >= 0:
			flush()
		default:
			cur = append(cur, c)
		}
	}
	flush()
	if len(strings.TrimSpace(best)) < 3 {
		return nil
	}
	return strings.Fields(strings.ToLower(best))
}

// skipTo returns the index of the first end at or after i (the last index
// when there is none).
func skipTo(s string, i int, end byte) int {
	if j := strings.IndexByte(s[i:], end); j >= 0 {
		return i + j
	}
	return len(s) - 1
}
