package devicelab

import (
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
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
