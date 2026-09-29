package devicelab_ios_legacy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
)

// wedgeDriver returns a Driver whose client counts relaunch requests.
func wedgeDriver(t *testing.T) (*Driver, *int) {
	t.Helper()
	relaunches := 0
	client := &Client{port: 1000}
	client.SetReviver(func(context.Context, int) (int, error) {
		relaunches++
		return 1000 + relaunches, nil
	})
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })
	d := NewDriver(client, nil, "udid", nil)
	d.wedgeStderr = devnull
	return d, &relaunches
}

func timedOut() *core.CommandResult {
	err := fmt.Errorf("runner request aborted: %w", context.DeadlineExceeded)
	return core.ErrorResult(err, "tapOn: "+err.Error())
}

func TestWedgedRunnerIsRelaunchedAfterConsecutiveTimeouts(t *testing.T) {
	d, relaunches := wedgeDriver(t)
	for i := 0; i < wedgedRunnerSteps; i++ {
		d.noteRunnerTimeout(timedOut(), 11*time.Second)
	}
	if *relaunches != 1 {
		t.Fatalf("relaunches = %d after %d slow timeouts, want 1", *relaunches, wedgedRunnerSteps)
	}
	if d.client.Port() != 1001 {
		t.Errorf("client still on port %d, want the relaunched runner's", d.client.Port())
	}
	// The count starts again after a relaunch.
	d.noteRunnerTimeout(timedOut(), 11*time.Second)
	if *relaunches != 1 {
		t.Errorf("relaunched again after one more timeout")
	}
}

func TestRunnerIsNotRelaunchedWithoutAWedge(t *testing.T) {
	tests := []struct {
		name  string
		steps []func(d *Driver)
	}{
		{"timeouts that came back fast (the flow's own deadline)", []func(*Driver){
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), time.Second) },
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), time.Second) },
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), time.Second) },
		}},
		{"an answered step in between", []func(*Driver){
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), 11*time.Second) },
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), 11*time.Second) },
			func(d *Driver) {
				d.noteRunnerTimeout(core.ErrorResult(errors.New("element not found"), "not found"), 11*time.Second)
			},
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), 11*time.Second) },
		}},
		{"a passing step in between", []func(*Driver){
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), 11*time.Second) },
			func(d *Driver) { d.noteRunnerTimeout(core.SuccessResult("ok", nil), time.Second) },
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), 11*time.Second) },
			func(d *Driver) { d.noteRunnerTimeout(timedOut(), 11*time.Second) },
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, relaunches := wedgeDriver(t)
			for _, step := range tt.steps {
				step(d)
			}
			if *relaunches != 0 {
				t.Errorf("relaunched %d times", *relaunches)
			}
		})
	}
}

// Without a supervisor (tests, the readiness probe) there is nothing to
// relaunch with, and noting timeouts must not panic.
func TestWedgedRunnerWithoutReviver(t *testing.T) {
	d := NewDriver(&Client{}, nil, "udid", nil)
	for i := 0; i < wedgedRunnerSteps+1; i++ {
		d.noteRunnerTimeout(timedOut(), 11*time.Second)
	}
}
