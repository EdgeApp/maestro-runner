package appium

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// iosNativeMissSource is a login screen: a text field known only by its
// placeholder, and a field whose id the tests reach by substring.
const iosNativeMissSource = `<?xml version="1.0" encoding="UTF-8"?>
<AppiumAUT>
  <XCUIElementTypeApplication type="XCUIElementTypeApplication" name="TestApp" label="Test App" enabled="true" visible="true" x="0" y="0" width="390" height="844">
    <XCUIElementTypeTextField type="XCUIElementTypeTextField" name="username-input" label="" value="" placeholderValue="Enter username" enabled="true" visible="true" x="50" y="300" width="300" height="44" />
    <XCUIElementTypeButton type="XCUIElementTypeButton" name="login-button" label="Sign In" enabled="true" visible="true" x="100" y="400" width="100" height="44" />
  </XCUIElementTypeApplication>
</AppiumAUT>`

// nativeMissServer is an iOS Appium endpoint whose element queries answer
// from a caller-supplied function, and which counts page-source requests —
// the call #173 is about avoiding.
type nativeMissServer struct {
	mu      sync.Mutex
	sources int
	queries []string
	answer  func(using, value string) (id string, errType string)
}

func (s *nativeMissServer) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/source"):
		s.mu.Lock()
		s.sources++
		s.mu.Unlock()
		writeJSON(w, map[string]interface{}{"value": iosNativeMissSource})
	case strings.HasSuffix(r.URL.Path, "/element") && r.Method == http.MethodPost:
		var body struct{ Using, Value string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.queries = append(s.queries, body.Using+": "+body.Value)
		s.mu.Unlock()
		id, errType := s.answer(body.Using, body.Value)
		if errType != "" {
			w.WriteHeader(http.StatusNotFound)
			writeJSON(w, map[string]interface{}{"value": map[string]interface{}{"error": errType, "message": "from test server"}})
			return
		}
		writeJSON(w, map[string]interface{}{"value": map[string]interface{}{w3cElementKey: id}})
	default:
		writeJSON(w, map[string]interface{}{"value": nil})
	}
}

func newNativeMissDriver(t *testing.T, answer func(using, value string) (string, string)) (*Driver, *nativeMissServer) {
	t.Helper()
	s := &nativeMissServer{answer: answer}
	server := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(server.Close)
	d := createTestAppiumDriver(server)
	d.platform = "ios"
	d.client.platform = "ios"
	return d, s
}

// nothingMatches is the device's answer when the element is not on screen.
func nothingMatches(string, string) (string, string) { return "", "no such element" }

func TestIOSLiteralTextMissSkipsPageSource(t *testing.T) {
	d, s := newNativeMissDriver(t, nothingMatches)

	if _, err := d.findElementDirect(flow.Selector{Text: "Not on screen"}); err == nil {
		t.Fatal("found an element that is not on screen")
	}
	if s.sources != 0 {
		t.Errorf("fetched the page source %d times for an absent literal text, want 0", s.sources)
	}
}

func TestIOSLiteralIDMissSkipsPageSource(t *testing.T) {
	d, s := newNativeMissDriver(t, nothingMatches)

	if _, err := d.findElementDirect(flow.Selector{ID: "not-here"}); err == nil {
		t.Fatal("found an element that is not on screen")
	}
	if s.sources != 0 {
		t.Errorf("fetched the page source %d times for an absent literal id, want 0", s.sources)
	}
}

func TestIOSTapLiteralTextMissSkipsPageSource(t *testing.T) {
	d, s := newNativeMissDriver(t, nothingMatches)

	if _, err := d.findElementForTapIOS(flow.Selector{Text: "Not on screen"}); err == nil {
		t.Fatal("found an element that is not on screen")
	}
	if s.sources != 0 {
		t.Errorf("fetched the page source %d times for an absent tap target, want 0", s.sources)
	}
}

// The first text query does not read placeholderValue; the page-source
// matcher does. A field known only by its placeholder must still be found.
func TestIOSPlaceholderOnlyTextStillFound(t *testing.T) {
	d, _ := newNativeMissDriver(t, func(using, value string) (string, string) {
		if strings.Contains(value, "placeholderValue") {
			return "field-1", ""
		}
		return "", "no such element"
	})

	info, err := d.findElementDirect(flow.Selector{Text: "Enter username"})
	if err != nil {
		t.Fatalf("a field known only by its placeholder was not found: %v", err)
	}
	if info == nil {
		t.Fatal("no element info")
	}
}

// accessibility id is exact; the page-source matcher takes a literal id as a
// substring. "username" must still reach "username-input".
func TestIOSLiteralIDSubstringStillFound(t *testing.T) {
	d, s := newNativeMissDriver(t, func(using, value string) (string, string) {
		if using == "-ios predicate string" && value == `name CONTAINS "username"` {
			return "field-1", ""
		}
		return "", "no such element"
	})

	if _, err := d.findElementDirect(flow.Selector{ID: "username"}); err != nil {
		t.Fatalf("id matched by substring was not found: %v", err)
	}
	if s.sources != 1 {
		t.Errorf("page source fetched %d times, want 1 (the match is resolved there, as before)", s.sources)
	}
}

// Only "no such element" proves absence. An older WebDriverAgent that does
// not know placeholderValue rejects the query instead; that must fall back to
// the page source rather than report the element missing.
func TestIOSRejectedPredicateFallsBackToPageSource(t *testing.T) {
	d, s := newNativeMissDriver(t, func(using, value string) (string, string) {
		if strings.Contains(value, "placeholderValue") {
			return "", "invalid selector"
		}
		return "", "no such element"
	})

	if _, err := d.findElementDirect(flow.Selector{Text: "Enter username"}); err != nil {
		t.Fatalf("placeholder text not found through the page-source fallback: %v", err)
	}
	if s.sources != 1 {
		t.Errorf("page source fetched %d times, want 1", s.sources)
	}
}

// Regex selectors and ids with a dot are matched by regex in the page source,
// which CONTAINS cannot reproduce, so they keep the page-source path.
func TestIOSRegexSelectorsStillUsePageSource(t *testing.T) {
	for _, sel := range []flow.Selector{
		{Text: "^Sign In$"},
		{ID: "login.button"},
		{ID: "^login-.*"},
	} {
		d, s := newNativeMissDriver(t, nothingMatches)
		_, _ = d.findElementDirect(sel)
		if s.sources != 1 {
			t.Errorf("%s: page source fetched %d times, want 1", sel.Describe(), s.sources)
		}
	}
}

func TestIOSContainsPredicatesMatchPageSourceMatcher(t *testing.T) {
	if got, want := iosTextContainsPredicate(`Say "hi"`), `label CONTAINS[c] "Say \"hi\"" OR value CONTAINS[c] "Say \"hi\"" OR placeholderValue CONTAINS[c] "Say \"hi\""`; got != want {
		t.Errorf("text predicate:\n got %s\nwant %s", got, want)
	}
	if got, want := iosIDContainsPredicate("username"), `name CONTAINS "username"`; got != want {
		t.Errorf("id predicate: got %s, want %s", got, want)
	}
	for id, literal := range map[string]bool{"username-input": true, "login.button": false, "^login$": false} {
		if got := iosIDIsLiteral(id); got != literal {
			t.Errorf("iosIDIsLiteral(%q) = %v, want %v", id, got, literal)
		}
	}
}
