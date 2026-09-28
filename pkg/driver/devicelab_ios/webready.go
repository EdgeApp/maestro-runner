package devicelab_ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/webinspector"
	"github.com/gorilla/websocket"

	"github.com/devicelab-dev/maestro-runner/pkg/logger"
)

// Web pages load on their own clock: the screen can look still while the
// visible page is still loading, and a tap then lands on a page that is not
// ready (a window.open from DDG's address-bar spoofing tests did nothing).
// Settle asks WebKit's inspector, when the app's web views are inspectable,
// whether the visible page has finished loading, the way the Android driver
// reads WebViews over CDP.

const (
	// webLoadTimeout caps how long settle waits for a visible page to load.
	webLoadTimeout = 5 * time.Second
	webPollEvery   = 100 * time.Millisecond
	webCallTimeout = 2 * time.Second
	listingWait    = 500 * time.Millisecond
)

// webPages tells whether an app's visible web page is still loading.
type webPages interface {
	Loading(ctx context.Context, bundleID string) (bool, error)
	Close() error
}

// visibleLoadingJS is true when this page is the visible one and has not
// finished loading.
const visibleLoadingJS = `document.visibilityState === "visible" && document.readyState !== "complete"`

// inspector reads page state through the simulator's webinspectord: go-ios's
// inspector client, bridged to CDP on a local port.
type inspector struct {
	client pageLister
	addr   string
	cancel context.CancelFunc

	// socks keeps one CDP connection per page: the bridge serves a page's
	// first inspector session only, and a second connection timed out.
	mu    sync.Mutex
	socks map[int]*pageSocket
}

// pageSocket is a page's CDP connection and its next request id.
type pageSocket struct {
	ws     *websocket.Conn
	nextID int
}

// pageLister is the part of go-ios's inspector client used here.
type pageLister interface {
	ListPages(ctx context.Context, wait time.Duration) ([]webinspector.ApplicationPage, error)
	Close() error
}

// openInspector connects to the simulator's inspector socket.
func openInspector(ctx context.Context, socket string) (*inspector, error) {
	conn, err := net.DialTimeout("unix", socket, webCallTimeout)
	if err != nil {
		return nil, err
	}
	client := webinspector.NewWithConnection(ios.DeviceEntry{}, ios.NewDeviceConnectionWithConn(conn))
	cctx, cancel := context.WithTimeout(ctx, 2*webCallTimeout)
	defer cancel()
	if err := client.Connect(cctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	// Page listings arrive a moment after connecting and are pushed on every
	// change after that; wait once so the first check sees them.
	if _, err := client.ListPages(cctx, listingWait); err != nil {
		_ = client.Close()
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	sctx, stop := context.WithCancel(context.Background())
	server := webinspector.NewCDPServer(client, "127.0.0.1", port)
	go func() {
		if err := server.Serve(sctx); err != nil && sctx.Err() == nil {
			logger.Debug("[devicelab-ios] web inspector bridge stopped: %v", err)
		}
	}()
	return &inspector{client: client, addr: server.Addr(), cancel: stop}, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (in *inspector) Loading(ctx context.Context, bundleID string) (bool, error) {
	pages, err := in.client.ListPages(ctx, 0)
	if err != nil {
		return false, err
	}
	for _, p := range pages {
		if p.Application.BundleID != bundleID {
			continue
		}
		if p.Page.Type != webinspector.WIRTypeWebPage && p.Page.Type != webinspector.WIRTypeWeb {
			continue // JavaScript contexts and automation sessions
		}
		if !strings.HasPrefix(p.Page.URL, "http") {
			continue // extension and internal pages never "load" for the user
		}
		start := time.Now()
		loading, err := in.evaluateBool(ctx, p.Page.ID, visibleLoadingJS)
		logger.Debug("[devicelab-ios] web page %d %s loading=%v err=%v (%v)", p.Page.ID, p.Page.URL, loading, err, time.Since(start).Round(time.Millisecond))
		if err != nil {
			continue // a page closing mid-check is not a loading page
		}
		if loading {
			return true, nil
		}
	}
	return false, nil
}

// evaluateBool runs a boolean expression on one page over the CDP bridge,
// reusing the page's connection.
func (in *inspector) evaluateBool(ctx context.Context, pageID int, expr string) (bool, error) {
	cctx, cancel := context.WithTimeout(ctx, webCallTimeout)
	defer cancel()
	in.mu.Lock()
	defer in.mu.Unlock()
	ps, err := in.socket(cctx, pageID)
	if err != nil {
		return false, err
	}
	v, err := ps.evaluate(cctx, expr)
	if err != nil {
		_ = ps.ws.Close()
		delete(in.socks, pageID)
	}
	return v, err
}

// socket returns the page's open connection, dialing it the first time.
func (in *inspector) socket(ctx context.Context, pageID int) (*pageSocket, error) {
	if ps, ok := in.socks[pageID]; ok {
		return ps, nil
	}
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, fmt.Sprintf("ws://%s/devtools/page/%d", in.addr, pageID), nil)
	if err != nil {
		return nil, err
	}
	if in.socks == nil {
		in.socks = map[int]*pageSocket{}
	}
	ps := &pageSocket{ws: ws, nextID: 1}
	in.socks[pageID] = ps
	return ps, nil
}

func (ps *pageSocket) evaluate(ctx context.Context, expr string) (bool, error) {
	id := ps.nextID
	ps.nextID++
	if deadline, ok := ctx.Deadline(); ok {
		_ = ps.ws.SetReadDeadline(deadline)
		_ = ps.ws.SetWriteDeadline(deadline)
	}
	req := map[string]any{"id": id, "method": "Runtime.evaluate",
		"params": map[string]any{"expression": expr, "returnByValue": true}}
	if err := ps.ws.WriteJSON(req); err != nil {
		return false, err
	}
	for {
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"result"`
			Error json.RawMessage `json:"error"`
		}
		if err := ps.ws.ReadJSON(&msg); err != nil {
			return false, err
		}
		if msg.ID != id {
			continue // events, and replies to requests that timed out
		}
		if len(msg.Error) > 0 {
			return false, fmt.Errorf("evaluate: %s", msg.Error)
		}
		var v bool
		if err := json.Unmarshal(msg.Result.Result.Value, &v); err != nil {
			return false, err
		}
		return v, nil
	}
}

func (in *inspector) Close() error {
	in.mu.Lock()
	for id, ps := range in.socks {
		_ = ps.ws.Close()
		delete(in.socks, id)
	}
	in.mu.Unlock()
	if in.cancel != nil {
		in.cancel()
	}
	return in.client.Close()
}

// webState is the driver's lazily opened inspector; off after it fails once.
type webState struct {
	mu    sync.Mutex
	pages webPages
	tried bool
}

var errNoInspector = errors.New("web inspector unavailable")

// webPagesFor opens the inspector on first use. A missing socket or an
// inspector that refuses turns the check off for the session.
func (d *Driver) webPagesFor() (webPages, error) {
	d.web.mu.Lock()
	defer d.web.mu.Unlock()
	if d.web.tried {
		if d.web.pages == nil {
			return nil, errNoInspector
		}
		return d.web.pages, nil
	}
	d.web.tried = true
	if d.openWeb == nil {
		return nil, errNoInspector
	}
	pages, err := d.openWeb()
	if err != nil {
		logger.Debug("[devicelab-ios] web inspector off: %v", err)
		return nil, errNoInspector
	}
	d.web.pages = pages
	return pages, nil
}

// openSimulatorInspector finds this simulator's webinspectord socket.
func (d *Driver) openSimulatorInspector() (webPages, error) {
	out, err := d.runSimctl("spawn", d.udid, "launchctl", "getenv", "RWI_LISTEN_SOCKET")
	if err != nil {
		return nil, err
	}
	socket := strings.TrimSpace(out)
	if socket == "" {
		return nil, errors.New("RWI_LISTEN_SOCKET not set")
	}
	return openInspector(d.context(), socket)
}

// waitForWebLoad waits, up to webLoadTimeout, while the app's visible web
// page is loading. It reports whether it waited, so settle can let the
// newly loaded page lay out.
func (d *Driver) waitForWebLoad() bool {
	if d.appID == "" {
		return false
	}
	pages, err := d.webPagesFor()
	if err != nil {
		return false
	}
	deadline := time.Now().Add(webLoadTimeout)
	waited := false
	for {
		loading, err := pages.Loading(d.context(), d.appID)
		if err != nil || !loading {
			if waited {
				logger.Debug("[devicelab-ios] web page finished loading")
			}
			return waited
		}
		if time.Now().After(deadline) || d.context().Err() != nil {
			logger.Debug("[devicelab-ios] web page still loading after %v; going on", webLoadTimeout)
			return waited
		}
		waited = true
		time.Sleep(webPollEvery)
	}
}
