package cli

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/urfave/cli/v2"
)

// updateCheckServer counts update-check requests and restores the real URL
// and notice channel when the test ends.
func updateCheckServer(t *testing.T) *int32 {
	t.Helper()
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`{"latest_version":"` + Version + `"}`))
	}))
	origURL, origNotice := updateCheckURL, updateNotice
	updateCheckURL, updateNotice = server.URL, make(chan string, 1)
	t.Cleanup(func() {
		server.Close()
		updateCheckURL, updateNotice = origURL, origNotice
	})
	return &hits
}

func TestUpdateCheckRuns(t *testing.T) {
	hits := updateCheckServer(t)
	maybeStartUpdateCheck(false)
	select {
	case <-updateNotice:
	case <-time.After(5 * time.Second):
		t.Fatal("update check did not finish")
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Errorf("update check requests = %d, want 1", *hits)
	}
}

func TestUpdateCheckDisabled(t *testing.T) {
	hits := updateCheckServer(t)
	maybeStartUpdateCheck(true)
	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(hits) != 0 {
		t.Errorf("update check requests = %d, want 0 when disabled", *hits)
	}
	printUpdateNotice() // must not block when no check ran
}

func TestNoUpdateCheckFlagReadsEnv(t *testing.T) {
	var flag *cli.BoolFlag
	for _, f := range GlobalFlags {
		if bf, ok := f.(*cli.BoolFlag); ok && bf.Name == "no-update-check" {
			flag = bf
		}
	}
	if flag == nil {
		t.Fatal("--no-update-check flag not defined")
	}
	if len(flag.EnvVars) == 0 || flag.EnvVars[0] != "MAESTRO_RUNNER_NO_UPDATE_CHECK" {
		t.Errorf("EnvVars = %v, want MAESTRO_RUNNER_NO_UPDATE_CHECK", flag.EnvVars)
	}
}
