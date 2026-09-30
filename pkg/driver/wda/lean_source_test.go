package wda

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// leanSourceFull is a screen as the full /source returns it; the wrapper is
// visible="false" but on screen, the case the rescue note covers.
const leanSourceFull = `<?xml version="1.0"?>
<AppiumAUT>
  <XCUIElementTypeOther name="wallet-list" label="" x="0" y="0" width="390" height="844" enabled="true" visible="true" accessible="false">
    <XCUIElementTypeOther name="row-wrapper" label="Assets" x="10" y="100" width="100" height="40" enabled="true" visible="false" accessible="false">
      <XCUIElementTypeStaticText name="row" label="Assets" x="10" y="100" width="100" height="40" enabled="true" visible="true" accessible="true"/>
    </XCUIElementTypeOther>
    <XCUIElementTypeTextField name="search" label="" placeholderValue="Search Wallets" x="10" y="700" width="300" height="40" enabled="true" selected="false" focused="false" visible="true" accessible="true"/>
    <XCUIElementTypeStaticText name="offscreen" label="Auto Log Off" x="10" y="2000" width="100" height="40" enabled="true" visible="false" accessible="true"/>
  </XCUIElementTypeOther>
</AppiumAUT>`

var visibleAccessibleAttr = regexp.MustCompile(` (visible|accessible)="[^"]*"`)

// leanOf is what WDA returns for excluded_attributes=visible,accessible.
func leanOf(full string) string {
	return visibleAccessibleAttr.ReplaceAllString(full, "")
}

// leanServer serves full or lean XML depending on the query, and records
// each /source query string.
type leanServer struct {
	full    string
	mu      sync.Mutex
	queries []string
}

func (s *leanServer) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.URL.Path; {
		case strings.HasSuffix(path, "/source"):
			s.mu.Lock()
			s.queries = append(s.queries, r.URL.RawQuery)
			s.mu.Unlock()
			src := s.full
			if strings.Contains(r.URL.Query().Get("excluded_attributes"), "visible") {
				src = leanOf(s.full)
			}
			jsonResponse(w, map[string]interface{}{"value": src})
		case strings.HasSuffix(path, "/window/size"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"width": 390.0, "height": 844.0}})
		default:
			jsonResponse(w, map[string]interface{}{"value": nil})
		}
	}))
}

func TestMatcherFetchesLeanSource(t *testing.T) {
	s := &leanServer{full: leanSourceFull}
	server := s.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementByPageSourceOnce(flow.Selector{Text: "Assets"}); err != nil {
		t.Fatalf("findElementByPageSourceOnce: %v", err)
	}
	if _, err := d.findElementRelativeOnce(flow.Selector{Text: "Assets", Above: &flow.Selector{ID: "search"}}); err != nil {
		t.Fatalf("findElementRelativeOnce: %v", err)
	}
	if _, err := d.countVisibleMatchesOnce(flow.Selector{Text: "Assets"}); err != nil {
		t.Fatalf("countVisibleMatchesOnce: %v", err)
	}
	if _, err := d.Hierarchy(); err != nil {
		t.Fatalf("Hierarchy: %v", err)
	}

	lean := "excluded_attributes=visible,accessible"
	want := []string{
		lean, "", // a page-source hit is read again from the full tree
		lean, "", // so is a relative hit
		lean, // a count reports no visibility
		"",   // the hierarchy dump keeps every attribute
	}
	if strings.Join(s.queries, "|") != strings.Join(want, "|") {
		t.Errorf("/source queries = %q, want %q", s.queries, want)
	}
}

// A miss in the lean tree is a miss in the full one, so it costs one fetch.
func TestLeanSourceMissFetchesOnce(t *testing.T) {
	s := &leanServer{full: leanSourceFull}
	server := s.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementByPageSourceOnce(flow.Selector{Text: "Buy"}); err == nil {
		t.Fatal("expected no match")
	}
	if _, err := d.findElementRelativeOnce(flow.Selector{Text: "Buy", Above: &flow.Selector{ID: "search"}}); err == nil {
		t.Fatal("expected no relative match")
	}
	want := "excluded_attributes=visible,accessible|excluded_attributes=visible,accessible"
	if got := strings.Join(s.queries, "|"); got != want {
		t.Errorf("/source queries = %q, want %q", got, want)
	}
}

func TestLeanSourceOptOut(t *testing.T) {
	t.Setenv("MAESTRO_WDA_LEAN_SOURCE", "0")
	s := &leanServer{full: leanSourceFull}
	server := s.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findElementByPageSourceOnce(flow.Selector{Text: "Assets"}); err != nil {
		t.Fatal(err)
	}
	if len(s.queries) != 1 || s.queries[0] != "" {
		t.Errorf("/source queries = %q, want one full fetch", s.queries)
	}
}

// Every attribute the matcher reads survives the exclusion, so each element
// parses the same apart from Displayed, which defaults to true.
func TestLeanSourceParsesSameElements(t *testing.T) {
	full, err := ParsePageSource(leanSourceFull)
	if err != nil {
		t.Fatal(err)
	}
	lean, err := ParsePageSource(leanOf(leanSourceFull))
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != len(lean) {
		t.Fatalf("full has %d elements, lean %d", len(full), len(lean))
	}
	for i := range full {
		f, l := full[i], lean[i]
		if !l.Displayed {
			t.Errorf("%s: lean Displayed = false, want the true default", l.Name)
		}
		if fields(f) != fields(l) || len(f.Children) != len(l.Children) {
			t.Errorf("element %d differs:\nfull %s\nlean %s", i, fields(f), fields(l))
		}
	}
}

// fields is every ParsedElement field except Displayed and the tree links.
func fields(e *ParsedElement) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%v|%v|%v|%v|%d", e.Type, e.Name, e.Label, e.Value,
		e.PlaceholderValue, e.Bounds, e.Enabled, e.Selected, e.Focused, e.Depth)
}

// The same selectors find the same elements, and off-screen elements stay
// off screen: out-of-bounds filtering reads bounds, not visible.
func TestLeanSourceFindsSameElements(t *testing.T) {
	sels := []flow.Selector{
		{Text: "Assets"},
		{Text: "search wallets"},
		{ID: "search"},
		{Text: "Auto Log Off"},
		{Text: "Assets", Above: &flow.Selector{ID: "search"}},
	}
	for _, lean := range []bool{false, true} {
		if !lean {
			t.Setenv("MAESTRO_WDA_LEAN_SOURCE", "0")
		} else {
			t.Setenv("MAESTRO_WDA_LEAN_SOURCE", "")
		}
		s := &leanServer{full: leanSourceFull}
		server := s.serve(t)
		d := createTestDriver(server)
		for _, sel := range sels {
			find := d.findElementByPageSourceOnce
			if sel.HasRelativeSelector() {
				find = d.findElementRelativeOnce
			}
			info, err := find(sel)
			if sel.Text == "Auto Log Off" {
				if err == nil {
					t.Errorf("lean=%v: off-screen %s matched", lean, sel.Describe())
				}
				continue
			}
			if err != nil {
				t.Errorf("lean=%v: %s: %v", lean, sel.Describe(), err)
				continue
			}
			if info.Bounds.Width == 0 {
				t.Errorf("lean=%v: %s: no bounds in %+v", lean, sel.Describe(), info)
			}
		}
		server.Close()
	}
}

// An on-screen element XCUITest marks visible="false" reports the same
// Visible and rescue note with and without the lean tree.
func TestLeanSourceKeepsRescueNote(t *testing.T) {
	for _, relative := range []bool{false, true} {
		sel := flow.Selector{ID: "row-wrapper"}
		if relative {
			sel.Above = &flow.Selector{ID: "search"}
		}
		for _, lean := range []bool{false, true} {
			if !lean {
				t.Setenv("MAESTRO_WDA_LEAN_SOURCE", "0")
			} else {
				t.Setenv("MAESTRO_WDA_LEAN_SOURCE", "")
			}
			s := &leanServer{full: leanSourceFull}
			server := s.serve(t)
			d := createTestDriver(server)
			find := d.findElementByPageSourceOnce
			if relative {
				find = d.findElementRelativeOnce
			}
			info, err := find(sel)
			server.Close()
			if err != nil {
				t.Fatalf("relative=%v lean=%v: %v", relative, lean, err)
			}
			if info.Visible || info.MatchNote == "" {
				t.Errorf("relative=%v lean=%v: Visible=%v MatchNote=%q, want false with a note", relative, lean, info.Visible, info.MatchNote)
			}
		}
	}
}
