package devicelab_ios_legacy

import (
	"fmt"
	"strings"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// handleSetPermissions applies permissions mid-flow on a simulator.
//
// The step was parsed and then never dispatched here, so a flow using it
// aborted before reaching the device (#148). Simulators take permissions
// through `simctl privacy`, the same mechanism the WDA driver uses; a real
// device has no equivalent, and saying so beats a silent no-op.
func (d *Driver) handleSetPermissions(step *flow.SetPermissionsStep) *core.CommandResult {
	if d.info == nil || !d.info.IsSimulator {
		return &core.CommandResult{
			Success: false,
			Error:   fmt.Errorf("unsupported on a real device"),
			Message: "setPermissions needs a simulator — on a real iOS device, permission dialogs must be handled in the flow",
		}
	}
	if d.udid == "" {
		return &core.CommandResult{Success: false, Error: fmt.Errorf("no udid"), Message: "setPermissions: no simulator udid"}
	}

	appID := step.AppID
	if appID == "" {
		appID = d.appID
	}
	if appID == "" {
		return &core.CommandResult{Success: false, Error: fmt.Errorf("no appId"), Message: "setPermissions needs an appId"}
	}
	if len(step.Permissions) == 0 {
		return &core.CommandResult{Success: false, Error: fmt.Errorf("no permissions"), Message: "setPermissions needs at least one permission"}
	}

	applied, failures := d.applyPermissions(appID, step.Permissions)
	if len(failures) > 0 {
		return &core.CommandResult{
			Success: false,
			Error:   fmt.Errorf("some permissions failed"),
			Message: fmt.Sprintf("Permissions: %d applied, failures: %s", applied, strings.Join(failures, "; ")),
		}
	}
	return &core.CommandResult{Success: true, Message: fmt.Sprintf("Permissions updated: %d", applied)}
}
