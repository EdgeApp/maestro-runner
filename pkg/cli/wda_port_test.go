package cli

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v2"

	wdadriver "github.com/devicelab-dev/maestro-runner/pkg/driver/wda"
)

func TestIOSWDAPortDefaultsToUDID(t *testing.T) {
	udid := "32B53853-65F3-492A-90A1-8A3105AAB00D"
	port, err := iosWDAPort(&RunConfig{}, udid)
	if err != nil {
		t.Fatal(err)
	}
	if port != wdadriver.PortFromUDID(udid) {
		t.Errorf("port = %d, want the UDID-derived %d", port, wdadriver.PortFromUDID(udid))
	}
}

func TestIOSWDAPortOverride(t *testing.T) {
	port, err := iosWDAPort(&RunConfig{WDAPort: 9282}, "32B53853-65F3-492A-90A1-8A3105AAB00D")
	if err != nil {
		t.Fatal(err)
	}
	if port != 9282 {
		t.Errorf("port = %d, want 9282", port)
	}
}

func TestIOSWDAPortOutOfRange(t *testing.T) {
	for _, p := range []int{-1, 65536} {
		if _, err := iosWDAPort(&RunConfig{WDAPort: p}, "udid"); err == nil {
			t.Errorf("--wda-port %d should be rejected", p)
		}
	}
}

func TestWDAPortRejectedForParallel(t *testing.T) {
	cfg := &RunConfig{Platform: "ios", Devices: []string{"A", "B"}, WDAPort: 9282}
	_, _, err := determineExecutionMode(cfg, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--wda-port") {
		t.Errorf("expected a --wda-port parallel error, got %v", err)
	}
}

func TestWDAPortFlagReadsEnv(t *testing.T) {
	for _, f := range GlobalFlags {
		if f, ok := f.(*cli.IntFlag); ok && f.Name == "wda-port" {
			if len(f.EnvVars) == 0 || f.EnvVars[0] != "MAESTRO_WDA_PORT" {
				t.Errorf("EnvVars = %v, want MAESTRO_WDA_PORT", f.EnvVars)
			}
			return
		}
	}
	t.Fatal("--wda-port flag not defined")
}
