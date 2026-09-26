package devicelab

import (
	"errors"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
	"github.com/devicelab-dev/maestro-runner/pkg/uiautomator2"
)

// batchClient answers the batched find-and-click: misses first, then taps
// the form at hitIndex.
type batchClient struct {
	*scriptedClient
	misses    int
	hitIndex  int
	calls     int
	perForm   int
	lastPairs []string
}

func (b *batchClient) FindFirstAndClickChecked(pairs []string, w, h int, hitTest bool) (*uiautomator2.Element, bool, string, int, error) {
	b.calls++
	b.lastPairs = pairs
	if b.calls <= b.misses {
		return nil, false, "", -1, errors.New("Element not found in any of the strategies")
	}
	return uiautomator2.NewCachedElement("e", "Save", uiautomator2.ElementRect{X: 0, Y: 1000, Width: 200, Height: 100}), true, "", b.hitIndex, nil
}

func (b *batchClient) FindAndClickChecked(string, string, int, int, bool) (*uiautomator2.Element, bool, string, error) {
	b.perForm++
	return nil, false, "", errors.New("not found")
}

// A tap sends every form in one call per round and never falls back to one
// call per form while the agent has the batched call.
func TestTapOn_OneCallPerRound(t *testing.T) {
	client := &batchClient{scriptedClient: &scriptedClient{trackingClient: newTrackingClient()}, misses: 2, hitIndex: 3}
	d := New(client, &core.PlatformInfo{ScreenWidth: 1080, ScreenHeight: 2400}, &mockShell{})

	res := d.tapOn(&flow.TapOnStep{Selector: flow.Selector{Text: "Save"}})
	if !res.Success {
		t.Fatalf("tap failed: %v", res.Error)
	}
	if client.calls != 3 || client.perForm != 0 {
		t.Errorf("batched calls = %d, per-form calls = %d; want 3 and 0", client.calls, client.perForm)
	}
	if len(client.lastPairs) < 8 || len(client.lastPairs)%2 != 0 {
		t.Errorf("pairs = %v, want every form as strategy, selector", client.lastPairs)
	}
}
