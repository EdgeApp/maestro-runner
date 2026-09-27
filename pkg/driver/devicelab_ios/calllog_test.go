package devicelab_ios

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/logger"
)

// Every runner round trip is logged with the command, the host-side time,
// the runner's own time and the sizes; a failed call is logged too.
func TestRunnerCallsAreLogged(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "runner.log")
	if err := logger.Init(logPath); err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"data":{"message":"up"},"serverMs":12.5}`))
	}))
	defer srv.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient("127.0.0.1", port)
	if _, err := c.Call(context.Background(), Command{Command: CmdUptime}); err != nil {
		t.Fatal(err)
	}

	dead := NewClient("127.0.0.1", 1) // nothing listens on port 1
	_, _ = dead.Call(context.Background(), Command{Command: CmdSnapshot})

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, want := range []string{"runner uptime ", "server=12ms", "ok", "runner snapshot ", "server=- ", "err="} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}
