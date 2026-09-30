package wda

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// gateServer answers the interactive union with gate (nil answers with no
// value array) and each single find with the first hit whose key the query
// contains. It records every single-find query and attribute GET.
type gateServer struct {
	gate     []interface{}
	hits     map[string]map[string]interface{}
	mu       sync.Mutex
	finds    []string
	attrGets int
}

func (g *gateServer) serve(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Using string `json:"using"`
			Value string `json:"value"`
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		switch path := r.URL.Path; {
		case r.Method == "POST" && strings.HasSuffix(path, "/elements"):
			if !strings.Contains(body.Value, "XCUIElementTypeSearchField' AND") {
				jsonResponse(w, map[string]interface{}{"value": []interface{}{}})
				return
			}
			if g.gate == nil {
				jsonResponse(w, map[string]interface{}{"status": 0})
				return
			}
			jsonResponse(w, map[string]interface{}{"value": g.gate})
		case r.Method == "POST" && strings.HasSuffix(path, "/element"):
			g.finds = append(g.finds, body.Using+": "+body.Value)
			for key, entry := range g.hits {
				if strings.Contains(body.Value, key) {
					jsonResponse(w, map[string]interface{}{"value": entry})
					return
				}
			}
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"error": "no such element", "message": "no such element"}})
		case r.Method == "GET" && strings.Contains(path, "/element/"):
			g.attrGets++
			jsonResponse(w, map[string]interface{}{"value": nil})
		case strings.HasSuffix(path, "/window/size"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"width": 390.0, "height": 844.0}})
		default:
			jsonResponse(w, map[string]interface{}{"value": nil})
		}
	}))
}

func TestInteractiveUnion(t *testing.T) {
	want := "((type == 'XCUIElementTypeTextField' OR type == 'XCUIElementTypeSecureTextField') AND (label CONTAINS[c] 'Log in' OR value CONTAINS[c] 'Log in' OR placeholderValue CONTAINS[c] 'Log in')) OR (type == 'XCUIElementTypeSearchField' AND (label CONTAINS[c] 'Log in' OR value CONTAINS[c] 'Log in')) OR (type == 'XCUIElementTypeButton' AND label ==[c] 'Log in')"
	if got := interactiveUnion("Log in", ""); got != want {
		t.Errorf("union = %q\nwant    %q", got, want)
	}
	yes := true
	filter := buildStateFilter(flow.Selector{Enabled: &yes})
	if got := interactiveUnion("Log in", filter); got != "("+want+") AND enabled == true" {
		t.Errorf("filtered union = %q", got)
	}
}

func TestInteractiveGateNoMatchSkipsQueries(t *testing.T) {
	g := &gateServer{gate: []interface{}{}}
	server := g.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	if _, err := d.findInteractiveElementByWDA(flow.Selector{Text: "Assets"}, ""); err == nil {
		t.Fatal("expected no interactive element")
	}
	if len(g.finds) != 0 {
		t.Errorf("ran %d single finds, want 0: %q", len(g.finds), g.finds)
	}
}

func TestInteractiveGateOneMatchIsTheAnswer(t *testing.T) {
	g := &gateServer{gate: []interface{}{inlineEntryJSON("e-btn", "XCUIElementTypeButton", "Log in", true)}}
	server := g.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findInteractiveElementByWDA(flow.Selector{Text: "Log in"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "e-btn" || info.Class != "XCUIElementTypeButton" || info.Text != "Log in" {
		t.Errorf("info = %+v", info)
	}
	if len(g.finds) != 0 || g.attrGets != 0 {
		t.Errorf("finds=%q attrGets=%d, want none", g.finds, g.attrGets)
	}
}

// Several matches leave the choice to the prioritized queries: a text field
// beats a button even when WDA lists the button first.
func TestInteractiveGateManyMatchesKeepsPriority(t *testing.T) {
	tf := inlineEntryJSON("e-tf", "XCUIElementTypeTextField", "Log in", true)
	btn := inlineEntryJSON("e-btn", "XCUIElementTypeButton", "Log in", true)
	g := &gateServer{
		gate: []interface{}{btn, tf},
		hits: map[string]map[string]interface{}{
			"**/XCUIElementTypeTextField[": tf,
			"**/XCUIElementTypeButton[":    btn,
		},
	}
	server := g.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findInteractiveElementByWDA(flow.Selector{Text: "Log in"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "e-tf" {
		t.Errorf("picked %s, want the text field", info.ID)
	}
	if len(g.finds) != 4 {
		t.Errorf("ran %d single finds, want 4", len(g.finds))
	}
}

func TestUnreadableGateRunsQueries(t *testing.T) {
	btn := inlineEntryJSON("e-btn", "XCUIElementTypeButton", "Log in", true)
	g := &gateServer{hits: map[string]map[string]interface{}{"**/XCUIElementTypeButton[": btn}}
	server := g.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findInteractiveElementByWDA(flow.Selector{Text: "Log in"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "e-btn" || len(g.finds) != 4 {
		t.Errorf("info=%+v finds=%d, want e-btn after 4 finds", info, len(g.finds))
	}
}

// A tap on plain text skips the four interactive queries and resolves
// through the exact-match predicate, as it did when they all missed.
func TestTapOnPlainTextSkipsInteractiveQueries(t *testing.T) {
	text := inlineEntryJSON("e-text", "XCUIElementTypeStaticText", "Assets", true)
	g := &gateServer{
		gate: []interface{}{},
		hits: map[string]map[string]interface{}{"(label == 'Assets' OR value == 'Assets')": text},
	}
	server := g.serve(t)
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findElementForTap(flow.Selector{Text: "Assets"}, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "e-text" {
		t.Errorf("info = %+v", info)
	}
	if len(g.finds) != 1 || strings.Contains(g.finds[0], "class chain") {
		t.Errorf("single finds = %q, want only the exact predicate", g.finds)
	}
}
