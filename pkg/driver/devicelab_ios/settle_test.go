package devicelab_ios

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// settleRunner is a fake runner: settle answers `settled` (or, when
// oldRunner is set, the error a runner without the command gives); drags are
// counted; anything else returns nodesFn's tree.
type settleRunner struct {
	mu        sync.Mutex
	settles   []map[string]any
	drags     int
	settled   bool
	oldRunner bool
	nodesFn   func(drags int) []SnapshotNode
}

func (r *settleRunner) driver(t *testing.T, info *core.PlatformInfo) *Driver {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var cmd map[string]any
		_ = json.NewDecoder(req.Body).Decode(&cmd)
		r.mu.Lock()
		defer r.mu.Unlock()
		switch cmd["command"] {
		case "settle":
			r.settles = append(r.settles, cmd)
			if r.oldRunner {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"message": "unknown command"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"idle": r.settled, "waitedMs": 120}})
		case "drag":
			r.drags++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"message": "dragged"}})
		default:
			var nodes []SnapshotNode
			if r.nodesFn != nil {
				nodes = r.nodesFn(r.drags)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"nodes": nodes}})
		}
	}))
	t.Cleanup(srv.Close)
	return NewDriver(&Client{baseURL: srv.URL, httpClient: srv.Client()}, info, "test-udid", nil)
}

func TestSettleScreen(t *testing.T) {
	r := &settleRunner{settled: true}
	d := r.driver(t, nil)
	if !d.settleScreen(1500) {
		t.Error("settled screen reported unsettled")
	}
	if got, _ := r.settles[0]["timeoutMs"].(float64); got != 1500 {
		t.Errorf("settle cap = %v, want 1500", r.settles[0]["timeoutMs"])
	}
	if d.settleScreen(0) {
		t.Error("a zero cap must not report a settle")
	}
	if len(r.settles) != 1 {
		t.Errorf("a zero cap still called the runner (%d calls)", len(r.settles))
	}

	moving := &settleRunner{settled: false}
	if moving.driver(t, nil).settleScreen(500) {
		t.Error("moving screen reported settled")
	}
}

// A runner without the settle command gets the old fixed pause, not an error.
func TestSettleScreenOldRunnerPauses(t *testing.T) {
	r := &settleRunner{oldRunner: true}
	d := r.driver(t, nil)
	start := time.Now()
	if d.settleScreen(1000) {
		t.Error("old runner reported a settle")
	}
	if waited := time.Since(start); waited < 250*time.Millisecond {
		t.Errorf("fallback pause = %v, want about 300ms", waited)
	}
}

func TestSettleCapMs(t *testing.T) {
	if got := settleCapMs(time.Now().Add(time.Minute)); got != settleDefaultCapMs {
		t.Errorf("far deadline cap = %v, want %v", got, settleDefaultCapMs)
	}
	if got := settleCapMs(time.Now().Add(-time.Second)); got != 0 {
		t.Errorf("past deadline cap = %v, want 0", got)
	}
	if got := settleCapMs(time.Now().Add(time.Second)); got <= 0 || got > 1000 {
		t.Errorf("near deadline cap = %v, want (0, 1000]", got)
	}
}

// scrollUntilVisible settles after every swipe instead of a fixed sleep.
func TestScrollUntilVisibleSettlesAfterEachSwipe(t *testing.T) {
	info := &core.PlatformInfo{ScreenWidth: 400, ScreenHeight: 800}
	r := &settleRunner{settled: true, nodesFn: func(drags int) []SnapshotNode {
		y := 1200.0
		if drags >= 2 {
			y = 300
		}
		return []SnapshotNode{
			{Type: "Other", Label: "row", Rect: SnapshotRect{X: 0, Y: float64(drags) * 10, Width: 400, Height: 40}},
			{Type: "Cell", Identifier: "target", Rect: SnapshotRect{X: 0, Y: y, Width: 100, Height: 40}},
		}
	}}
	d := r.driver(t, info)
	res := d.handleScrollUntilVisible(&flow.ScrollUntilVisibleStep{Element: flow.Selector{ID: "target"}})
	if !res.Success {
		t.Fatalf("scrollUntilVisible failed: %s", res.Message)
	}
	if r.drags != 2 || len(r.settles) != 2 {
		t.Errorf("drags = %d, settles = %d; want 2 and 2", r.drags, len(r.settles))
	}
}

// A screen that stops moving ends the loop early with a clear reason.
func TestScrollUntilVisibleStopsWhenNothingMoves(t *testing.T) {
	info := &core.PlatformInfo{ScreenWidth: 400, ScreenHeight: 800}
	r := &settleRunner{settled: true, nodesFn: func(int) []SnapshotNode {
		return []SnapshotNode{{Type: "Other", Label: "end of list", Rect: SnapshotRect{Width: 400, Height: 40}}}
	}}
	d := r.driver(t, info)
	res := d.handleScrollUntilVisible(&flow.ScrollUntilVisibleStep{
		Element:  flow.Selector{ID: "missing"},
		BaseStep: flow.BaseStep{TimeoutMs: 20000},
	})
	if res.Success {
		t.Fatal("found an element that is not there")
	}
	if r.drags > 4 {
		t.Errorf("kept scrolling a still screen: %d drags", r.drags)
	}
}

// An index selector picks the Nth match and still needs it on screen.
func TestScrollTargetInHonoursIndex(t *testing.T) {
	d := &Driver{info: &core.PlatformInfo{ScreenWidth: 400, ScreenHeight: 800}}
	nodes := []SnapshotNode{
		{Type: "Cell", Label: "item", Rect: SnapshotRect{X: 0, Y: 100, Width: 100, Height: 40}},
		{Type: "Cell", Label: "item", Rect: SnapshotRect{X: 0, Y: 200, Width: 100, Height: 40}},
	}
	got := d.scrollTargetIn(nodes, flow.Selector{Text: "item", Index: "1"}, 0)
	if got == nil || got.Rect.Y != 200 {
		t.Errorf("index 1 picked %+v, want the second item", got)
	}
	// Past the matches, the index falls back to the first match, as it does
	// for findElement (selectByIndex).
	if got := d.scrollTargetIn(nodes, flow.Selector{Text: "item", Index: "5"}, 0); got == nil || got.Rect.Y != 100 {
		t.Errorf("index past the matches picked %+v, want the first item", got)
	}
	offscreen := []SnapshotNode{{Type: "Cell", Label: "item", Rect: SnapshotRect{X: 0, Y: 2000, Width: 100, Height: 40}}}
	if d.scrollTargetIn(offscreen, flow.Selector{Text: "item", Index: "0"}, 0) != nil {
		t.Error("an indexed match off screen was accepted")
	}
	if d.scrollTargetIn(nodes, flow.Selector{Text: "nothing"}, 0) != nil {
		t.Error("a selector with no match found something")
	}
}

func TestWaitForAnimationUsesSettle(t *testing.T) {
	r := &settleRunner{settled: true}
	d := r.driver(t, nil)
	res := d.handleWaitForAnimation(&flow.WaitForAnimationToEndStep{})
	if !res.Success || res.Message != "animation ended" {
		t.Fatalf("waitForAnimation = %+v", res)
	}
	if len(r.settles) != 1 {
		t.Errorf("settles = %d, want 1", len(r.settles))
	}

	moving := &settleRunner{settled: false}
	md := moving.driver(t, nil)
	res = md.handleWaitForAnimation(&flow.WaitForAnimationToEndStep{BaseStep: flow.BaseStep{TimeoutMs: 300}})
	if !res.Success {
		t.Fatalf("a timed-out wait must still pass (best effort), got %+v", res)
	}
}
