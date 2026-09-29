package cli

import (
	"strings"
	"testing"
)

// A real iPhone without --team-id fails before anything touches the device.
func TestCreateDevicelabIOSDeviceDriverNeedsTeamID(t *testing.T) {
	drv, cleanup, err := createDevicelabIOSDeviceDriver(&RunConfig{AppFile: "/nonexistent/My.app"}, "00008101-001C0C660A13001E")
	if err == nil || drv != nil || cleanup != nil {
		t.Fatalf("want an error and no driver, got drv=%v err=%v", drv, err)
	}
	if !strings.Contains(err.Error(), "--team-id") || !strings.Contains(err.Error(), "--driver devicelab") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "install") {
		t.Fatalf("the app must not be installed first: %v", err)
	}
}
