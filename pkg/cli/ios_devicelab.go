package cli

import "github.com/devicelab-dev/maestro-runner/pkg/core"

// createDevicelabIOSDriver constructs the iOS driver for --driver devicelab.
// Until the new agent's host driver lands it is the legacy runner.
func createDevicelabIOSDriver(cfg *RunConfig) (core.Driver, func(), error) {
	return createDevicelabLegacyIOSDriver(cfg)
}
