package wda

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// absenceServer is a WDA whose finds for label/value text match nothing.
// probeHits is what the absence probe (the CONTAINS[cd] predicate) returns;
// nil answers with no value array. It counts /source and probe requests.
type absenceServer struct {
	source    string
	probeHits []interface{}
	sources   int32
	probes    int32
}

func (a *absenceServer) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		var body struct {
			Value string `json:"value"`
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		switch {
		case r.Method == "POST" && strings.HasSuffix(path, "/elements"):
			if strings.Contains(body.Value, "CONTAINS[cd]") {
				atomic.AddInt32(&a.probes, 1)
				if a.probeHits == nil {
					jsonResponse(w, map[string]interface{}{"status": 0})
					return
				}
				jsonResponse(w, map[string]interface{}{"value": a.probeHits})
				return
			}
			jsonResponse(w, map[string]interface{}{"value": []interface{}{}})
		case r.Method == "POST" && strings.HasSuffix(path, "/element"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"error": "no such element", "message": "no such element"}})
		case strings.HasSuffix(path, "/source"):
			atomic.AddInt32(&a.sources, 1)
			jsonResponse(w, map[string]interface{}{"value": a.source})
		case strings.HasSuffix(path, "/window/size"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"width": 390.0, "height": 844.0}})
		default:
			jsonResponse(w, map[string]interface{}{"value": nil})
		}
	}))
}

const absenceSource = `<?xml version="1.0"?>
<AppiumAUT>
  <XCUIElementTypeStaticText name="row" label="Assets" x="10" y="100" width="100" height="40" visible="true"/>
</AppiumAUT>`

func TestAbsenceProbe(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		sel  flow.Selector
		want string
	}{
		{"plain text", flow.Selector{Text: "Change PIN"}, "label CONTAINS[cd] 'Change PIN' OR value CONTAINS[cd] 'Change PIN' OR placeholderValue CONTAINS[cd] 'Change PIN'"},
		{"text with state filter", flow.Selector{Text: "Next", Enabled: &yes}, "label CONTAINS[cd] 'Next' OR value CONTAINS[cd] 'Next' OR placeholderValue CONTAINS[cd] 'Next'"},
		{"literal dot text", flow.Selector{Text: "mastodon.social"}, "label CONTAINS[cd] 'mastodon.social' OR value CONTAINS[cd] 'mastodon.social' OR placeholderValue CONTAINS[cd] 'mastodon.social'"},
		{"plain id", flow.Selector{ID: "wallet-list-row"}, "name CONTAINS[cd] 'wallet-list-row'"},
		{"regex text", flow.Selector{Text: "^Sign out$"}, ""},
		{"non-ASCII text", flow.Selector{Text: "Café"}, ""},
		{"quote in text", flow.Selector{Text: "Let's go"}, ""},
		{"percent in text", flow.Selector{Text: "50%"}, ""},
		{"backslash in text", flow.Selector{Text: `a\b`}, ""},
		{"id with regex metacharacter", flow.Selector{ID: "com.edge.row"}, ""},
		{"id and text", flow.Selector{ID: "row", Text: "Assets"}, ""},
		{"index", flow.Selector{Text: "Assets", Index: "1"}, ""},
		{"relative", flow.Selector{Text: "Assets", Below: &flow.Selector{Text: "Header"}}, ""},
		{"empty", flow.Selector{}, ""},
	}
	for _, c := range cases {
		got, ok := absenceProbe(c.sel)
		if ok != (c.want != "") || got != c.want {
			t.Errorf("%s: got (%q, %v), want %q", c.name, got, ok, c.want)
		}
	}
}

func TestFindElementOnceSkipsSourceWhenAbsent(t *testing.T) {
	for _, sel := range []flow.Selector{{Text: "Auto Log Off"}, {ID: "missing-row"}} {
		a := &absenceServer{source: absenceSource, probeHits: []interface{}{}}
		server := a.serve(t)
		d := createTestDriver(server)

		_, err := d.findElementOnce(sel)
		server.Close()
		if !errors.Is(err, errNotInTree) {
			t.Errorf("%s: err = %v, want errNotInTree", sel.Describe(), err)
		}
		if a.sources != 0 {
			t.Errorf("%s: fetched /source %d times, want 0", sel.Describe(), a.sources)
		}
		if a.probes != 1 {
			t.Errorf("%s: probes = %d, want 1", sel.Describe(), a.probes)
		}
	}
}

// The label/value predicate misses a text field matched only by its
// placeholder; the probe sees it, so page source still decides.
func TestPlaceholderOnlyMatchFallsBackToSource(t *testing.T) {
	a := &absenceServer{
		source: `<?xml version="1.0"?>
<AppiumAUT>
  <XCUIElementTypeTextField name="" label="" placeholderValue="Search Wallets" x="10" y="100" width="300" height="40" visible="true"/>
</AppiumAUT>`,
		probeHits: []interface{}{map[string]interface{}{"ELEMENT": "e1"}},
	}
	server := a.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findElementOnce(flow.Selector{Text: "Search Wallets"})
	if err != nil {
		t.Fatalf("expected the page-source match, got %v", err)
	}
	if info.Bounds.Width != 300 {
		t.Errorf("unexpected info %+v", info)
	}
	if a.sources == 0 {
		t.Error("never fetched /source, want page source to decide")
	}
}

// A probe response that is not an array proves nothing.
func TestUnreadableProbeFallsBackToSource(t *testing.T) {
	a := &absenceServer{source: absenceSource}
	server := a.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementOnce(flow.Selector{Text: "Assets"}); err != nil {
		t.Fatalf("expected the page-source match, got %v", err)
	}
	if a.sources == 0 {
		t.Error("never fetched /source, want page source to decide")
	}
}

func TestIneligibleSelectorsKeepSource(t *testing.T) {
	for _, sel := range []flow.Selector{{Text: "Ass.*"}, {Text: "Àssets"}, {ID: "r.w"}} {
		a := &absenceServer{source: absenceSource, probeHits: []interface{}{}}
		server := a.serve(t)
		d := createTestDriver(server)

		_, _ = d.findElementOnce(sel)
		server.Close()
		if a.probes != 0 {
			t.Errorf("%s: probed %d times, want 0", sel.Describe(), a.probes)
		}
		if a.sources == 0 {
			t.Errorf("%s: never fetched /source, want page source to decide", sel.Describe())
		}
	}
}

// An element WDA finds but rejects as off screen is not absent: page source
// still decides, exactly as before.
func TestOffscreenElementKeepsSourcePath(t *testing.T) {
	var sources, probes int32
	entry := inlineEntryJSON("e1", "XCUIElementTypeStaticText", "Auto Log Off", false)
	entry["rect"] = map[string]interface{}{"x": 10.0, "y": 2000.0, "width": 100.0, "height": 40.0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.URL.Path; {
		case strings.HasSuffix(path, "/elements"):
			atomic.AddInt32(&probes, 1)
			jsonResponse(w, map[string]interface{}{"value": []interface{}{}})
		case strings.HasSuffix(path, "/element"):
			jsonResponse(w, map[string]interface{}{"value": entry})
		case strings.HasSuffix(path, "/source"):
			atomic.AddInt32(&sources, 1)
			jsonResponse(w, map[string]interface{}{"value": absenceSource})
		case strings.HasSuffix(path, "/window/size"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"width": 390.0, "height": 844.0}})
		default:
			jsonResponse(w, map[string]interface{}{"value": nil})
		}
	}))
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementOnce(flow.Selector{Text: "Auto Log Off"}); errors.Is(err, errNotInTree) {
		t.Fatal("an element WDA found must not be reported absent")
	}
	if probes != 0 || sources != 1 {
		t.Errorf("probes=%d sources=%d, want 0 and 1", probes, sources)
	}
}

func TestOptionalFindPollsWithoutSource(t *testing.T) {
	a := &absenceServer{source: absenceSource, probeHits: []interface{}{}}
	server := a.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElement(flow.Selector{Text: "Auto Log Off"}, true, 300); !errors.Is(err, errNotInTree) {
		t.Fatalf("err = %v, want errNotInTree", err)
	}
	if a.sources != 0 {
		t.Errorf("fetched /source %d times, want 0", a.sources)
	}
	if a.probes < 2 {
		t.Errorf("probes = %d, want repeated polling", a.probes)
	}
}

func TestOptionalTapFindPollsWithoutSource(t *testing.T) {
	a := &absenceServer{source: absenceSource, probeHits: []interface{}{}}
	server := a.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementForTap(flow.Selector{Text: "Auto Log Off"}, true, 300); !errors.Is(err, errNotInTree) {
		t.Fatalf("err = %v, want errNotInTree", err)
	}
	if a.sources != 0 {
		t.Errorf("fetched /source %d times, want 0", a.sources)
	}
}

// A required step that fails still reports the closest on-screen texts,
// from one page-source pass after the deadline.
func TestRequiredFindFailureKeepsClosestTexts(t *testing.T) {
	a := &absenceServer{source: absenceSource, probeHits: []interface{}{}}
	server := a.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	_, err := d.findElement(flow.Selector{Text: "Assetz"}, false, 300)
	if err == nil || !strings.Contains(err.Error(), `closest on-screen texts: "Assets"`) {
		t.Fatalf("err = %v, want the closest-text hint", err)
	}
	if a.sources != 1 {
		t.Errorf("fetched /source %d times, want 1", a.sources)
	}
}
