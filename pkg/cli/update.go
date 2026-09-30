package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// updateCheckURL is a var so tests can point the check at a local server.
var updateCheckURL = "https://open.devicelab.dev/api/maestro-runner/updates"

// updateNotice receives the update message from the background check.
var updateNotice = make(chan string, 1)

type updateResponse struct {
	LatestVersion string `json:"latest_version"`
}

// maybeStartUpdateCheck starts the background update check unless it was
// turned off with --no-update-check (MAESTRO_RUNNER_NO_UPDATE_CHECK). Hosts
// that must not call out, or that pin a version, turn it off.
func maybeStartUpdateCheck(disabled bool) {
	if disabled {
		return
	}
	startUpdateCheck()
}

// startUpdateCheck kicks off a background update check.
// Call printUpdateNotice() later to print the result.
func startUpdateCheck() {
	ch := updateNotice
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}

		req, err := http.NewRequest("GET", updateCheckURL, nil)
		if err != nil {
			ch <- ""
			return
		}

		req.Header.Set("User-Agent", "maestro-runner")

		resp, err := client.Do(req)
		if err != nil {
			ch <- ""
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			ch <- ""
			return
		}

		var result updateResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			ch <- ""
			return
		}

		if result.LatestVersion != "" && result.LatestVersion != Version {
			ch <- fmt.Sprintf("\n  Update available: %s → %s\n  Run: curl -fsSL https://open.devicelab.dev/install/maestro-runner | bash\n", Version, result.LatestVersion)
		} else {
			ch <- ""
		}
	}()
}

// printUpdateNotice prints the update message if one is available.
func printUpdateNotice() {
	select {
	case msg := <-updateNotice:
		if msg != "" {
			fmt.Print(msg)
		}
	default:
		// Check not finished yet, don't block
	}
}
