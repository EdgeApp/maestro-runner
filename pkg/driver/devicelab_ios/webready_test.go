package devicelab_ios

import (
	"context"
	"fmt"
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

// cdpPage serves page CDP sockets. Runtime.evaluate answers with the page
// state (a readyState, or "hidden"); other commands get an empty result.
func cdpPage(t *testing.T, state string) *httptest.Server {
	srv, _ := cdpPageCounting(t, state)
	return srv
}

func cdpPageCounting(t *testing.T, state string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var conns atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conns.Add(1)
		defer ws.Close()
		for {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := ws.ReadJSON(&req); err != nil {
				return
			}
			_ = ws.WriteJSON(map[string]any{"method": "Runtime.consoleAPICalled"})
			reply := fmt.Sprintf(`{"id":%d,"result":{}}`, req.ID)
			if req.Method == "Runtime.evaluate" {
				reply = fmt.Sprintf(`{"id":%d,"result":{"result":{"type":"string","value":%q}}}`, req.ID, state)
			}
			_ = ws.WriteMessage(websocket.TextMessage, []byte(reply))
		}
	}))
	return srv, &conns
}

func TestPageStateReusesThePageConnection(t *testing.T) {
	srv, conns := cdpPageCounting(t, "complete")
	defer srv.Close()
	in := &inspector{client: &fakeLister{}, addr: strings.TrimPrefix(srv.URL, "http://")}
	for i := 0; i < 3; i++ {
		state, _, err := in.pageState(context.Background(), 7)
		if err != nil || state != "complete" {
			t.Fatalf("check %d: state=%q err=%v", i, state, err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Errorf("connections = %d, want 1 reused across checks", n)
	}
	_ = in.Close()
	if len(in.socks) != 0 {
		t.Errorf("Close left %d page connections open", len(in.socks))
	}
}

func TestPageStateReportsErrors(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if ws.ReadJSON(&req) != nil {
				return
			}
			reply := fmt.Sprintf(`{"id":%d,"result":{}}`, req.ID)
			if req.Method == "Runtime.evaluate" {
				reply = fmt.Sprintf(`{"id":%d,"error":{"message":"no page"}}`, req.ID)
			}
			_ = ws.WriteMessage(websocket.TextMessage, []byte(reply))
		}
	}))
	defer srv.Close()
	in := &inspector{client: &fakeLister{}, addr: strings.TrimPrefix(srv.URL, "http://")}
	if _, _, err := in.pageState(context.Background(), 1); err == nil {
		t.Error("an error reply was not reported")
	}
	if len(in.socks) != 0 {
		t.Error("a failed page connection was kept")
	}
	in.addr = "127.0.0.1:1"
	if _, _, err := in.pageState(context.Background(), 1); err == nil {
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
	if err == nil {
		time.Sleep(time.Second) // let network events arrive, then check again
		loading, err = in.Loading(context.Background(), app)
	}
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
	srv := cdpPage(t, "loading")
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

func TestLoadingTreatsYoungSilentPagesAsBusyThenSkipsThem(t *testing.T) {
	lister := &fakeLister{pages: []webinspector.ApplicationPage{
		page("com.app", 4, webinspector.WIRTypeWebPage, "https://app.example"),
	}}
	in := &inspector{client: lister, addr: "127.0.0.1:1", evalTimeout: 50 * time.Millisecond}
	if loading, err := in.Loading(context.Background(), "com.app"); err != nil || !loading {
		t.Errorf("young page not answering: loading=%v err=%v, want true", loading, err)
	}
	if !in.dead[4] {
		t.Error("the silent page was not marked dead")
	}
	if loading, err := in.Loading(context.Background(), "com.app"); err != nil || loading {
		t.Errorf("dead page asked again: loading=%v err=%v, want false", loading, err)
	}
	lister.err = errBoom
	if _, err := in.Loading(context.Background(), "com.app"); err == nil {
		t.Error("a listing error was not reported")
	}
}

func TestLoadingIgnoresOldSilentPages(t *testing.T) {
	lister := &fakeLister{pages: []webinspector.ApplicationPage{
		page("com.app", 4, webinspector.WIRTypeWebPage, "https://app.example"),
	}}
	in := &inspector{client: lister, addr: "127.0.0.1:1", evalTimeout: 50 * time.Millisecond,
		firstSeen: map[int]time.Time{4: time.Now().Add(-time.Minute)}, dead: map[int]bool{},
		lastSig: map[string]string{"com.app": "4=https://app.example"}}
	if loading, err := in.Loading(context.Background(), "com.app"); err != nil || loading {
		t.Errorf("old background page not answering: loading=%v err=%v, want false", loading, err)
	}
}

func TestLoadingCountsANewPageOnce(t *testing.T) {
	srv := cdpPage(t, "complete")
	defer srv.Close()
	lister := &fakeLister{pages: []webinspector.ApplicationPage{
		page("com.app", 2, webinspector.WIRTypeWebPage, "https://one.example"),
	}}
	in := &inspector{client: lister, addr: strings.TrimPrefix(srv.URL, "http://")}
	if loading, _ := in.Loading(context.Background(), "com.app"); loading {
		t.Error("the first listing counted as a change")
	}
	lister.pages = append(lister.pages, page("com.app", 8, webinspector.WIRTypeWebPage, "https://two.example"))
	if loading, _ := in.Loading(context.Background(), "com.app"); !loading {
		t.Error("a new page did not count as loading")
	}
	if loading, _ := in.Loading(context.Background(), "com.app"); loading {
		t.Error("an unchanged listing still counted as loading")
	}
	lister.pages[1] = page("com.app", 8, webinspector.WIRTypeWebPage, "https://two.example/next")
	if loading, _ := in.Loading(context.Background(), "com.app"); !loading {
		t.Error("a navigation (new URL) did not count as loading")
	}
}

func TestWaitListening(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := waitListening(strings.TrimPrefix(srv.URL, "http://"), time.Second); err != nil {
		t.Errorf("a listening server: %v", err)
	}
	if err := waitListening("127.0.0.1:1", 50*time.Millisecond); err == nil {
		t.Error("a closed port was reported listening")
	}
}
