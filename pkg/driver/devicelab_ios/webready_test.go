package devicelab_ios

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/flow"
)

// fakePages reports loading for the first n checks.
type fakePages struct {
	loadingFor int32
	checks     atomic.Int32
	closed     bool
	bundles    []string
}

func (f *fakePages) Loading(_ context.Context, bundleID string) (bool, error) {
	f.bundles = append(f.bundles, bundleID)
	return f.checks.Add(1) <= f.loadingFor, nil
}

func (f *fakePages) Close() error { f.closed = true; return nil }

func TestSettleWaitsForVisiblePageToLoad(t *testing.T) {
	d, fa, _ := newTestDriver(t, screenOf(node(1, "Button", "Start", 10, 10, 50, 20)))
	fp := &fakePages{loadingFor: 2}
	d.openWeb = func() (webPages, error) { return fp, nil }
	d.SetAppID("com.example.browser")
	openWebNow(t, d)
	d.settle(defaultSettleTimeout)
	if got := fp.checks.Load(); got != 3 {
		t.Errorf("load checks = %d, want 3 (loading, loading, done)", got)
	}
	if fp.bundles[0] != "com.example.browser" {
		t.Errorf("checked %q, want the app under test", fp.bundles[0])
	}
	if n := len(fa.sent("settle")); n != 1 {
		t.Errorf("agent settles = %d, want 1 after the page loaded", n)
	}
	d.Close()
	if !fp.closed {
		t.Error("Close did not close the inspector")
	}
}

func TestWebCheckSkippedWithoutApp(t *testing.T) {
	d, _, _ := newTestDriver(t, screenOf())
	opened := 0
	d.openWeb = func() (webPages, error) { opened++; return &fakePages{}, nil }
	if d.waitForWebLoad() {
		t.Error("waited with no app under test")
	}
	if opened != 0 {
		t.Errorf("opened the inspector %d times with no app", opened)
	}
}

func TestUnavailableInspectorIsTriedOnce(t *testing.T) {
	d, _, _ := newTestDriver(t, screenOf(node(1, "Button", "Go", 10, 10, 50, 20)))
	opened := 0
	d.openWeb = func() (webPages, error) { opened++; return nil, errNoInspector }
	d.SetAppID("com.example.app")
	for i := 0; i < 3; i++ {
		if res := d.Execute(&flow.TapOnStep{Selector: flow.Selector{Text: "Go"}}); !res.Success {
			t.Fatal(res.Message)
		}
		d.settle(defaultSettleTimeout)
	}
	<-d.web.ready
	d.settle(defaultSettleTimeout)
	if opened != 1 {
		t.Errorf("inspector opened %d times, want 1 (off after the first failure)", opened)
	}
}

// openWebNow starts the background open and waits for it.
func openWebNow(t *testing.T, d *Driver) {
	t.Helper()
	_, _ = d.webPagesFor()
	select {
	case <-d.web.ready:
	case <-time.After(time.Second):
		t.Fatal("inspector did not open")
	}
}

func TestFirstSettleDoesNotWaitForTheInspector(t *testing.T) {
	d, _, _ := newTestDriver(t, screenOf())
	release := make(chan struct{})
	d.openWeb = func() (webPages, error) { <-release; return &fakePages{}, nil }
	d.SetAppID("com.example.app")
	start := time.Now()
	if d.waitForWebLoad() {
		t.Error("waited on a page before the inspector was open")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("first check blocked %v on the inspector opening", time.Since(start))
	}
	close(release)
	<-d.web.ready
	if _, err := d.webPagesFor(); err != nil {
		t.Errorf("inspector not usable once open: %v", err)
	}
	d.Close()
}

func TestOpenSimulatorInspectorNeedsSocket(t *testing.T) {
	d, _, sl := newTestDriver(t, screenOf())
	sl.answers["spawn SIM-1 launchctl getenv RWI_LISTEN_SOCKET"] = "\n"
	if _, err := d.openSimulatorInspector(); err == nil {
		t.Error("opened with no RWI_LISTEN_SOCKET")
	}
	sl.answers["spawn SIM-1 launchctl getenv RWI_LISTEN_SOCKET"] = "/nonexistent/webinspectord_sim.socket\n"
	if _, err := d.openSimulatorInspector(); err == nil {
		t.Error("opened a socket that does not exist")
	}
}

// TestInspectorOnSimulator runs against a booted simulator's webinspectord:
// DL_IOS_INSPECTOR_SOCKET=<RWI_LISTEN_SOCKET> DL_IOS_INSPECTOR_APP=<bundle id>.
// It checks every page of the app twice, so each page gets its own session
// and a second check reuses it.
func TestInspectorOnSimulator(t *testing.T) {
	socket, app := os.Getenv("DL_IOS_INSPECTOR_SOCKET"), os.Getenv("DL_IOS_INSPECTOR_APP")
	if socket == "" || app == "" {
		t.Skip("set DL_IOS_INSPECTOR_SOCKET and DL_IOS_INSPECTOR_APP to run against a simulator")
	}
	start := time.Now()
	in, err := openInspector(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	t.Logf("connected in %v", time.Since(start))
	for _, p := range in.client.webPages(app) {
		for round := 1; round <= 2; round++ {
			s := time.Now()
			state, pc, err := in.pageState(context.Background(), p)
			busy := err == nil && pc.networkBusy(time.Now())
			t.Logf("page %d %q round %d: state=%q netBusy=%v err=%v (%v)", p.ID, p.URL, round, state, busy, err, time.Since(s))
		}
	}
	s := time.Now()
	loading, err := in.Loading(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s loading=%v in %v", app, loading, time.Since(s))
	// DL_IOS_INSPECTOR_WATCH=<seconds> keeps checking, as settle would.
	if secs, _ := strconv.Atoi(os.Getenv("DL_IOS_INSPECTOR_WATCH")); secs > 0 {
		end := time.Now().Add(time.Duration(secs) * time.Second)
		for time.Now().Before(end) {
			s := time.Now()
			loading, err := in.Loading(context.Background(), app)
			var urls []string
			for _, p := range in.client.webPages(app) {
				urls = append(urls, fmt.Sprintf("%d:%s", p.ID, p.URL))
			}
			t.Logf("%s loading=%v err=%v in %v pages=%v", time.Now().Format("15:04:05.000"), loading, err, time.Since(s).Round(time.Millisecond), urls)
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func TestQuiescenceMode(t *testing.T) {
	for env, want := range map[string]string{"": "all", "all": "all", "main": "main", "off": ""} {
		t.Setenv("DL_IOS_QUIESCENCE", env)
		if got := quiescenceMode(); got != want {
			t.Errorf("DL_IOS_QUIESCENCE=%q: mode %q, want %q", env, got, want)
		}
	}
}
