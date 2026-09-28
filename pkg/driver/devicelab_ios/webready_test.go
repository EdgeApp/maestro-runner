package devicelab_ios

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielpaulus/go-ios/ios/webinspector"
	"github.com/gorilla/websocket"

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
	if opened != 1 {
		t.Errorf("inspector opened %d times, want 1 (off after the first failure)", opened)
	}
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

// cdpPage serves one page's CDP socket, answering Runtime.evaluate with value.
func cdpPage(t *testing.T, value string) *httptest.Server {
	t.Helper()
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var req map[string]any
		if err := ws.ReadJSON(&req); err != nil {
			return
		}
		_ = ws.WriteJSON(map[string]any{"method": "Runtime.consoleAPICalled"})
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"id":1,"result":{"result":{"type":"boolean","value":`+value+`}}}`))
	}))
}

func TestEvaluateBoolReadsTheAnswer(t *testing.T) {
	for _, c := range []struct {
		value string
		want  bool
	}{{"true", true}, {"false", false}} {
		srv := cdpPage(t, c.value)
		in := &inspector{addr: strings.TrimPrefix(srv.URL, "http://")}
		got, err := in.evaluateBool(context.Background(), 1, visibleLoadingJS)
		srv.Close()
		if err != nil || got != c.want {
			t.Errorf("value %s: got %v, %v; want %v", c.value, got, err, c.want)
		}
	}
}

func TestEvaluateBoolReportsErrors(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var req map[string]any
		_ = ws.ReadJSON(&req)
		_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"id":1,"error":{"message":"no page"}}`))
	}))
	defer srv.Close()
	in := &inspector{addr: strings.TrimPrefix(srv.URL, "http://")}
	if _, err := in.evaluateBool(context.Background(), 1, "1"); err == nil {
		t.Error("an error reply was not reported")
	}
	in.addr = "127.0.0.1:1"
	if _, err := in.evaluateBool(context.Background(), 1, "1"); err == nil {
		t.Error("a dead bridge was not reported")
	}
}

// TestInspectorOnSimulator runs against a booted simulator's webinspectord:
// DL_IOS_INSPECTOR_SOCKET=<RWI_LISTEN_SOCKET> DL_IOS_INSPECTOR_APP=<bundle id>.
func TestInspectorOnSimulator(t *testing.T) {
	socket, app := os.Getenv("DL_IOS_INSPECTOR_SOCKET"), os.Getenv("DL_IOS_INSPECTOR_APP")
	if socket == "" || app == "" {
		t.Skip("set DL_IOS_INSPECTOR_SOCKET and DL_IOS_INSPECTOR_APP to run against a simulator")
	}
	in, err := openInspector(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	pages, _ := in.client.ListPages(context.Background(), 0)
	for _, p := range pages {
		if p.Application.BundleID == app {
			t.Logf("page %d %s %q", p.Page.ID, p.Page.Type, p.Page.URL)
		}
	}
	start := time.Now()
	loading, err := in.Loading(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s loading=%v in %v", app, loading, time.Since(start))
}

// fakeLister lists fixed pages.
type fakeLister struct {
	pages  []webinspector.ApplicationPage
	err    error
	closed bool
}

func (f *fakeLister) ListPages(context.Context, time.Duration) ([]webinspector.ApplicationPage, error) {
	return f.pages, f.err
}

func (f *fakeLister) Close() error { f.closed = true; return nil }

func page(bundle string, id int, typ webinspector.WIRType, url string) webinspector.ApplicationPage {
	return webinspector.ApplicationPage{
		Application: webinspector.Application{BundleID: bundle},
		Page:        webinspector.Page{ID: id, Type: typ, URL: url},
	}
}

func TestLoadingChecksOnlyTheAppsWebPages(t *testing.T) {
	srv := cdpPage(t, "true")
	defer srv.Close()
	lister := &fakeLister{pages: []webinspector.ApplicationPage{
		page("com.other", 1, webinspector.WIRTypeWebPage, "https://other.example"),
		page("com.app", 2, webinspector.WIRTypeJavaScript, "https://sw.example"),
		page("com.app", 3, webinspector.WIRTypeWebPage, "webkit-extension://abc"),
	}}
	in := &inspector{client: lister, addr: strings.TrimPrefix(srv.URL, "http://")}
	if loading, err := in.Loading(context.Background(), "com.app"); err != nil || loading {
		t.Fatalf("with no user page: loading=%v err=%v, want false", loading, err)
	}
	lister.pages = append(lister.pages, page("com.app", 4, webinspector.WIRTypeWebPage, "https://app.example"))
	if loading, err := in.Loading(context.Background(), "com.app"); err != nil || !loading {
		t.Fatalf("with a loading page: loading=%v err=%v, want true", loading, err)
	}
	if err := in.Close(); err != nil || !lister.closed {
		t.Errorf("Close: err=%v closed=%v", err, lister.closed)
	}
}

func TestLoadingSkipsUnreachablePagesAndReportsListErrors(t *testing.T) {
	lister := &fakeLister{pages: []webinspector.ApplicationPage{
		page("com.app", 4, webinspector.WIRTypeWebPage, "https://app.example"),
	}}
	in := &inspector{client: lister, addr: "127.0.0.1:1"}
	if loading, err := in.Loading(context.Background(), "com.app"); err != nil || loading {
		t.Errorf("unreachable page: loading=%v err=%v, want false, nil", loading, err)
	}
	lister.err = errBoom
	if _, err := in.Loading(context.Background(), "com.app"); err == nil {
		t.Error("a listing error was not reported")
	}
}
