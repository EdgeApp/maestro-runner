package devicelab

import (
	"errors"
	"testing"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
	"github.com/devicelab-dev/maestro-runner/pkg/uiautomator2"
)

// settleCountingClient counts WaitForSettle calls.
type settleCountingClient struct {
	*richClient
	settles int
}

func (c *settleCountingClient) WaitForSettle(timeoutMs, quietMs int) (bool, error) {
	c.settles++
	return true, nil
}

// FindAndClickChecked finds nothing, so a tap fails fast; the settle tests
// only count settles.
func (c *settleCountingClient) FindAndClickChecked(strategy, selector string, screenW, screenH int, hitTest bool) (*uiautomator2.Element, bool, string, error) {
	return nil, false, "", errors.New("not found")
}

// A key press right after a tap waits for the UI to settle first; one that
// does not follow a tap goes out at once.
func TestKeyPressSettlesOnlyAfterTap(t *testing.T) {
	client := &settleCountingClient{richClient: &richClient{trackingClient: newTrackingClient()}}
	d := New(client, &core.PlatformInfo{}, &mockShell{})
	back := &flow.PressKeyStep{BaseStep: flow.BaseStep{StepType: flow.StepPressKey}, Key: "back"}

	d.Execute(back)
	if client.settles != 0 {
		t.Fatalf("key press with no tap before it settled %d times", client.settles)
	}

	d.lastStepWasTap = true
	d.Execute(back)
	if client.settles != 1 {
		t.Fatalf("key press after a tap settled %d times, want 1", client.settles)
	}

	// The key press itself is not a tap, so the next one does not settle.
	d.Execute(&flow.BackStep{BaseStep: flow.BaseStep{StepType: flow.StepBack}})
	if client.settles != 1 {
		t.Fatalf("back after a key press settled again: %d", client.settles)
	}
}

// openLink waits for the app to settle, so the next step reads the page the
// link opened rather than the one it replaced.
func TestOpenLinkSettles(t *testing.T) {
	client := &settleCountingClient{richClient: &richClient{trackingClient: newTrackingClient()}}
	d := New(client, &core.PlatformInfo{}, &mockShell{})

	if res := d.openLink(&flow.OpenLinkStep{Link: "duck://https://duckduckgo.com?q=x"}); !res.Success {
		t.Fatalf("openLink failed: %v", res.Error)
	}
	if client.settles != 1 {
		t.Errorf("openLink settled %d times, want 1", client.settles)
	}
}

// copyTextFrom right after a tap settles first, so it reads what the tap led to.
func TestCopyTextFromSettlesAfterTap(t *testing.T) {
	client := &settleCountingClient{richClient: &richClient{trackingClient: newTrackingClient()}}
	d := New(client, &core.PlatformInfo{}, &mockShell{})
	d.lastStepWasTap = true
	d.Execute(&flow.CopyTextFromStep{BaseStep: flow.BaseStep{TimeoutMs: 1}, Selector: flow.Selector{ID: "omnibarTextInput"}})
	if client.settles != 1 {
		t.Errorf("copyTextFrom after a tap settled %d times, want 1", client.settles)
	}
}

// Enter settles after it is pressed; other keys do not.
func TestEnterSettlesAfterPress(t *testing.T) {
	client := &settleCountingClient{richClient: &richClient{trackingClient: newTrackingClient()}}
	d := New(client, &core.PlatformInfo{}, &mockShell{})

	d.pressKey(&flow.PressKeyStep{Key: "back"})
	if client.settles != 0 {
		t.Fatalf("back settled %d times", client.settles)
	}
	d.pressKey(&flow.PressKeyStep{Key: "Enter"})
	if client.settles != 1 {
		t.Errorf("enter settled %d times, want 1", client.settles)
	}
}

// A tap straight after a tap settles first; a first tap does not.
func TestTapSettlesOnlyAfterTap(t *testing.T) {
	client := &settleCountingClient{richClient: &richClient{trackingClient: newTrackingClient()}}
	d := New(client, &core.PlatformInfo{}, &mockShell{})
	tap := &flow.TapOnStep{BaseStep: flow.BaseStep{StepType: flow.StepTapOn, TimeoutMs: 1}, Selector: flow.Selector{Text: "Menu"}}

	d.Execute(tap)
	if client.settles != 0 {
		t.Fatalf("first tap settled %d times", client.settles)
	}
	d.lastStepWasTap = true
	d.Execute(tap)
	if client.settles != 1 {
		t.Errorf("tap after a tap settled %d times, want 1", client.settles)
	}
}

// An assert right after a tap settles first, so it reads the screen the tap
// led to and not the one being left; typing after a tap does not wait.
func TestAssertSettlesAfterTap(t *testing.T) {
	client := &settleCountingClient{richClient: &richClient{trackingClient: newTrackingClient()}}
	d := New(client, &core.PlatformInfo{}, &mockShell{})
	assert := &flow.AssertVisibleStep{BaseStep: flow.BaseStep{StepType: flow.StepAssertVisible, TimeoutMs: 1}, Selector: flow.Selector{Text: "Albums"}}

	d.lastStepWasTap = true
	d.Execute(assert)
	if client.settles != 1 {
		t.Fatalf("assert after a tap settled %d times, want 1", client.settles)
	}
	d.Execute(assert)
	if client.settles != 1 {
		t.Fatalf("assert after an assert settled again: %d", client.settles)
	}
	d.lastStepWasTap = true
	d.Execute(&flow.HideKeyboardStep{BaseStep: flow.BaseStep{StepType: flow.StepHideKeyboard}})
	if client.settles != 1 {
		t.Errorf("hideKeyboard after a tap settled: %d", client.settles)
	}
}

// The probes before a find's full wait count against its timeout.
func TestRemainingTimeoutMs(t *testing.T) {
	d := New(newTrackingClient(), &core.PlatformInfo{}, &mockShell{})
	start := time.Now().Add(-2 * time.Second)
	if got := d.remainingTimeoutMs(start, true, 7000); got < 4900 || got > 5000 {
		t.Errorf("7s timeout 2s in: %dms left, want ~5000", got)
	}
	if got := d.remainingTimeoutMs(start, true, 1000); got != 1 {
		t.Errorf("1s timeout 2s in: %dms left, want 1", got)
	}
	if got := d.remainingTimeoutMs(start, true, 0); got < OptionalFindTimeout-2100 || got > OptionalFindTimeout-2000 {
		t.Errorf("default optional timeout 2s in: %dms left", got)
	}
}
