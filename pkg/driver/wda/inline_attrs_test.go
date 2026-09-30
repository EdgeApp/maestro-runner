package wda

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// inlineEntryJSON is a non-compact find entry as WDA returns it once
// shouldUseCompactResponses is false and elementResponseAttributes is set.
func inlineEntryJSON(id, typ, text string, displayed bool) map[string]interface{} {
	return map[string]interface{}{
		"ELEMENT":                             id,
		"element-6066-11e4-a52e-4f735466cecf": id,
		"type":                                typ,
		"text":                                text,
		"rect":                                map[string]interface{}{"x": 10.0, "y": 20.0, "width": 100.0, "height": 40.0},
		"displayed":                           displayed,
	}
}

// inlineServer answers finds with inline attributes and counts the
// per-element attribute GETs that inline attributes are meant to replace.
func inlineServer(t *testing.T, entry map[string]interface{}, attrGets *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/elements"):
			jsonResponse(w, map[string]interface{}{"value": []interface{}{entry}})
		case r.Method == "POST" && strings.HasSuffix(path, "/element"):
			jsonResponse(w, map[string]interface{}{"value": entry})
		case r.Method == "GET" && strings.Contains(path, "/element/"):
			atomic.AddInt32(attrGets, 1)
			switch {
			case strings.HasSuffix(path, "/text"):
				jsonResponse(w, map[string]interface{}{"value": "full text from GET"})
			case strings.HasSuffix(path, "/rect"):
				jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"x": 10.0, "y": 20.0, "width": 100.0, "height": 40.0}})
			case strings.HasSuffix(path, "/displayed"):
				jsonResponse(w, map[string]interface{}{"value": true})
			case strings.HasSuffix(path, "/name"):
				jsonResponse(w, map[string]interface{}{"value": "XCUIElementTypeStaticText"})
			}
		case strings.HasSuffix(path, "/window/size"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"width": 390.0, "height": 844.0}})
		default:
			jsonResponse(w, map[string]interface{}{"value": nil})
		}
	}))
}

func TestCreateSessionRequestsInlineAttrs(t *testing.T) {
	var got map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session":
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"sessionId": "s1"}})
		case strings.HasSuffix(r.URL.Path, "/appium/settings"):
			var body struct {
				Settings map[string]interface{} `json:"settings"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body.Settings["elementResponseAttributes"]; ok {
				got = body.Settings
			}
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{}})
		}
	}))
	defer server.Close()

	c := &Client{baseURL: server.URL, httpClient: http.DefaultClient}
	if err := c.CreateSession("com.example", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got == nil {
		t.Fatal("CreateSession did not request inline element attributes")
	}
	if got["shouldUseCompactResponses"] != false {
		t.Errorf("shouldUseCompactResponses = %v, want false", got["shouldUseCompactResponses"])
	}
	if got["elementResponseAttributes"] != inlineAttrFields {
		t.Errorf("elementResponseAttributes = %v, want %q", got["elementResponseAttributes"], inlineAttrFields)
	}
}

func TestCreateSessionInlineAttrsOptOut(t *testing.T) {
	t.Setenv("MAESTRO_WDA_INLINE_ATTRS", "0")
	var asked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session":
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"sessionId": "s1"}})
		case strings.HasSuffix(r.URL.Path, "/appium/settings"):
			var body struct {
				Settings map[string]interface{} `json:"settings"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body.Settings["elementResponseAttributes"]; ok {
				asked = true
			}
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{}})
		}
	}))
	defer server.Close()

	c := &Client{baseURL: server.URL, httpClient: http.DefaultClient}
	if err := c.CreateSession("com.example", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if asked {
		t.Error("MAESTRO_WDA_INLINE_ATTRS=0 should leave compact responses on")
	}
}

func TestParseInlineAttrs(t *testing.T) {
	a, ok := parseInlineAttrs(inlineEntryJSON("e1", "XCUIElementTypeButton", "Assets", false))
	if !ok {
		t.Fatal("expected inline attrs to parse")
	}
	want := ElementAttrs{Type: "XCUIElementTypeButton", Text: "Assets", X: 10, Y: 20, Width: 100, Height: 40}
	if a != want {
		t.Errorf("got %+v, want %+v", a, want)
	}

	// A compact entry (reference only) carries no attributes.
	if _, ok := parseInlineAttrs(map[string]interface{}{"ELEMENT": "e1"}); ok {
		t.Error("compact entry should not parse as inline attrs")
	}

	// text is null when an element has neither value nor label.
	entry := inlineEntryJSON("e1", "XCUIElementTypeOther", "", true)
	entry["text"] = nil
	a, ok = parseInlineAttrs(entry)
	if !ok || a.Text != "" || a.TextTruncated {
		t.Errorf("null text: got %+v ok=%v", a, ok)
	}

	// Text at the snapshot string cap may be cut short.
	a, _ = parseInlineAttrs(inlineEntryJSON("e1", "XCUIElementTypeTextView", strings.Repeat("x", 512), true))
	if !a.TextTruncated {
		t.Error("512-byte text should be flagged as possibly truncated")
	}
}

func TestElementRefIgnoresAttributeStrings(t *testing.T) {
	// Only the W3C key: must not pick "type" or "text" as the reference.
	m := inlineEntryJSON("", "XCUIElementTypeButton", "Assets", true)
	delete(m, "ELEMENT")
	m["element-6066-11e4-a52e-4f735466cecf"] = "w3c-id"
	for i := 0; i < 20; i++ { // map order is random; check repeatedly
		if got := elementRef(m); got != "w3c-id" {
			t.Fatalf("elementRef = %q, want w3c-id", got)
		}
	}
}

func TestGetElementInfoUsesInlineAttrs(t *testing.T) {
	var gets int32
	server := inlineServer(t, inlineEntryJSON("e1", "XCUIElementTypeStaticText", "Change PIN", true), &gets)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findElementByWDA(flow.Selector{Text: "Change PIN"})
	if err != nil {
		t.Fatalf("findElementByWDA: %v", err)
	}
	if gets != 0 {
		t.Errorf("expected no per-element GETs, got %d", gets)
	}
	want := core.Bounds{X: 10, Y: 20, Width: 100, Height: 40}
	if info.Text != "Change PIN" || info.Class != "XCUIElementTypeStaticText" || !info.Visible || info.Bounds != want {
		t.Errorf("unexpected info %+v", info)
	}
}

// Off-screen elements keep their existing handling: displayed=false with
// bounds outside the viewport is rejected, exactly as with the GETs.
func TestGetElementInfoInlineOffscreenRejected(t *testing.T) {
	var gets int32
	entry := inlineEntryJSON("e1", "XCUIElementTypeStaticText", "Auto Log Off", false)
	entry["rect"] = map[string]interface{}{"x": 10.0, "y": 2000.0, "width": 100.0, "height": 40.0}
	server := inlineServer(t, entry, &gets)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementByWDA(flow.Selector{Text: "Auto Log Off"}); err == nil {
		t.Fatal("expected off-screen displayed=false element to be rejected")
	}
	if gets != 0 {
		t.Errorf("expected no per-element GETs, got %d", gets)
	}
}

// displayed=false inside the viewport is accepted with the same MatchNote.
func TestGetElementInfoInlineDisplayedFalseInViewport(t *testing.T) {
	var gets int32
	server := inlineServer(t, inlineEntryJSON("e1", "XCUIElementTypeOther", "Wrapper", false), &gets)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findElementByWDA(flow.Selector{Text: "Wrapper"})
	if err != nil {
		t.Fatalf("findElementByWDA: %v", err)
	}
	if info.Visible || !strings.Contains(info.MatchNote, "displayed=false") {
		t.Errorf("expected displayed=false override note, got %+v", info)
	}
}

func TestGetElementInfoTruncatedTextRefetched(t *testing.T) {
	var gets int32
	server := inlineServer(t, inlineEntryJSON("e1", "XCUIElementTypeTextView", strings.Repeat("x", 512), true), &gets)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.client.FindElement("predicate string", "x"); err != nil {
		t.Fatal(err)
	}
	info, err := d.getElementInfo("e1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Text != "full text from GET" {
		t.Errorf("Text = %q, want the GET /text value", info.Text)
	}
	if gets != 1 {
		t.Errorf("expected exactly the text GET, got %d GETs", gets)
	}
}

func TestInlineAttrsFallbackToGets(t *testing.T) {
	var gets int32
	// Compact response: reference only.
	server := inlineServer(t, map[string]interface{}{"ELEMENT": "e1"}, &gets)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findElementByWDA(flow.Selector{Text: "anything"})
	if err != nil {
		t.Fatal(err)
	}
	if gets != 4 {
		t.Errorf("expected the four per-element GETs, got %d", gets)
	}
	if info.Text != "full text from GET" {
		t.Errorf("Text = %q", info.Text)
	}
}

func TestInlineAttrsInvalidatedByGesture(t *testing.T) {
	var gets int32
	server := inlineServer(t, inlineEntryJSON("e1", "XCUIElementTypeButton", "Next", true), &gets)
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: http.DefaultClient, sessionID: "s"}

	if _, err := c.FindElement("predicate string", "x"); err != nil {
		t.Fatal(err)
	}
	_ = c.ElementClick("e1") // any non-find POST may change the screen
	if _, ok := c.TakeInlineAttrs("e1"); ok {
		t.Error("inline attrs should not survive a gesture")
	}
}

func TestInlineAttrsConsumedAndAged(t *testing.T) {
	c := &Client{}
	c.storeInline("e1", inlineEntryJSON("e1", "XCUIElementTypeButton", "Next", true))
	if _, ok := c.TakeInlineAttrs("e1"); !ok {
		t.Fatal("expected fresh entry")
	}
	if _, ok := c.TakeInlineAttrs("e1"); ok {
		t.Error("entry should be consumed on read")
	}

	c.storeInline("e2", inlineEntryJSON("e2", "XCUIElementTypeButton", "Next", true))
	c.inlineMu.Lock()
	e := c.inline["e2"]
	e.at = time.Now().Add(-inlineAttrsMaxAge - time.Second)
	c.inline["e2"] = e
	c.inlineMu.Unlock()
	if _, ok := c.TakeInlineAttrs("e2"); ok {
		t.Error("stale entry should not be used")
	}
}
