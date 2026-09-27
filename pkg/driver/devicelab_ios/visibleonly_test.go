package devicelab_ios

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// snapshotRecorder is a fake runner that records every snapshot request and
// answers with a fixed tree, optionally flagged truncated.
type snapshotRecorder struct {
	mu        sync.Mutex
	requests  []map[string]any
	truncated bool
}

func (s *snapshotRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cmd map[string]any
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Errorf("decode command: %v", err)
		}
		s.mu.Lock()
		s.requests = append(s.requests, cmd)
		truncated := s.truncated
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"nodes":     []SnapshotNode{{Type: "Button", Label: "Go", Rect: SnapshotRect{X: 10, Y: 10, Width: 50, Height: 20}}},
				"truncated": truncated,
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Lookups ask the runner for the on-screen elements only, as Maestro filters
// out-of-bounds elements before matching; the report hierarchy asks for the
// whole tree.
func TestLookupsAskForVisibleOnlyReportsDoNot(t *testing.T) {
	rec := &snapshotRecorder{truncated: true}
	srv := rec.server(t)
	d := NewDriver(&Client{baseURL: srv.URL, httpClient: srv.Client()}, nil, "test-udid", nil)

	if _, err := d.fetchSnapshot(); err != nil {
		t.Fatal(err)
	}
	if !d.lastSnapshotTruncated {
		t.Error("truncated snapshot not recorded")
	}
	if _, err := d.hierarchyJSON(); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(rec.requests))
	}
	if v, _ := rec.requests[0]["visibleOnly"].(bool); !v {
		t.Errorf("lookup snapshot request = %v, want visibleOnly true", rec.requests[0])
	}
	if _, ok := rec.requests[1]["visibleOnly"]; ok {
		t.Errorf("report snapshot request = %v, want the full tree", rec.requests[1])
	}
}
