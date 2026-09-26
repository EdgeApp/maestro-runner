package wda

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// A key-named text that labels a real element (an alert's Delete) taps the
// element; it is not sent as a backspace (#179).
func TestTapOnKeyNamedButtonTapsElement(t *testing.T) {
	var mu sync.Mutex
	var keys, clicks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/wda/keys"):
			keys++
			jsonResponse(w, map[string]interface{}{"status": 0})
		case strings.HasSuffix(p, "/click"):
			clicks++
			jsonResponse(w, map[string]interface{}{"status": 0})
		case strings.HasSuffix(p, "/rect"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"x": 200, "y": 500, "width": 120, "height": 44}})
		case strings.HasSuffix(p, "/displayed"):
			jsonResponse(w, map[string]interface{}{"value": true})
		case strings.HasSuffix(p, "/element") && r.Method == "POST":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "Delete") {
				jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"ELEMENT": "alert-delete"}})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"error": "no such element"}})
		default:
			jsonResponse(w, map[string]interface{}{"status": 0})
		}
	}))
	defer server.Close()
	d := createTestDriver(server)

	res := d.tapOn(&flow.TapOnStep{Selector: flow.Selector{Text: "Delete"}})
	if !res.Success {
		t.Fatalf("tap failed: %s", res.Message)
	}
	if keys != 0 {
		t.Errorf("sent %d key presses; the Delete button should have been tapped", keys)
	}
	if clicks != 1 {
		t.Errorf("clicks = %d, want 1", clicks)
	}
}

// Of several elements with an id, the one on screen wins over an earlier one
// that XCUITest reports not displayed (the outgoing screen of a push).
func TestIDPrefersDisplayedElement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/elements"):
			jsonResponse(w, map[string]interface{}{"value": []map[string]interface{}{{"ELEMENT": "old"}, {"ELEMENT": "new"}}})
		case strings.Contains(p, "/element/old/rect"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"x": -97, "y": 184, "width": 145, "height": 44}})
		case strings.Contains(p, "/element/new/rect"):
			jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"x": 24, "y": 184, "width": 145, "height": 44}})
		case strings.Contains(p, "/element/old/displayed"):
			jsonResponse(w, map[string]interface{}{"value": false})
		case strings.Contains(p, "/element/new/displayed"):
			jsonResponse(w, map[string]interface{}{"value": true})
		default:
			jsonResponse(w, map[string]interface{}{"status": 0})
		}
	}))
	defer server.Close()
	d := createTestDriver(server)

	info, err := d.findElementByWDA(flow.Selector{ID: "open-subfolder"})
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "new" {
		t.Errorf("picked %q, want the displayed element %q", info.ID, "new")
	}
}

// A coordinate tap goes to the centre of the visible part of the bounds and
// never to a point off the screen.
func TestTapPointClipsToScreen(t *testing.T) {
	d := &Driver{info: &core.PlatformInfo{ScreenWidth: 402, ScreenHeight: 874}}
	x, y, ok := d.tapPoint(core.Bounds{X: -97, Y: 184, Width: 145, Height: 44})
	if !ok || x != 24 || y != 206 {
		t.Errorf("partly off-screen: got (%v,%v,%v), want (24,206,true)", x, y, ok)
	}
	if _, _, ok := d.tapPoint(core.Bounds{X: -300, Y: 184, Width: 145, Height: 44}); ok {
		t.Error("fully off-screen bounds should not be tappable")
	}
	x, y, ok = d.tapPoint(core.Bounds{X: 0, Y: 100, Width: 402, Height: 2000})
	if !ok || y != 487 || x != 201 {
		t.Errorf("taller than the screen: got (%v,%v,%v), want (201,487,true)", x, y, ok)
	}
}

// settleAfterTap waits while the tapped element moves and stops when it
// comes to rest or goes away; an element that never moves costs at most
// tapEffectWait.
func TestSettleAfterTap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xs     []int // successive x positions; -1 = element gone
		minCal int
	}{
		{"push slides out then rests", []int{10, 10, 0, -40, -80, -120, -120}, 7},
		{"gone mid-push", []int{10, 0, -1}, 3},
		{"never moves", []int{10}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				mu.Lock()
				i := calls
				calls++
				mu.Unlock()
				if i >= len(tc.xs) {
					i = len(tc.xs) - 1
				}
				if tc.xs[i] == -1 {
					w.WriteHeader(http.StatusNotFound)
					jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"error": "stale element reference"}})
					return
				}
				jsonResponse(w, map[string]interface{}{"value": map[string]interface{}{"x": tc.xs[i], "y": 200, "width": 100, "height": 44}})
			}))
			defer server.Close()
			d := createTestDriver(server)
			d.lastTapID = "tapped"
			d.settleAfterTap()
			if calls < tc.minCal {
				t.Errorf("rect reads = %d, want at least %d", calls, tc.minCal)
			}
			if calls > 40 {
				t.Errorf("rect reads = %d: did not stop", calls)
			}
		})
	}
}
