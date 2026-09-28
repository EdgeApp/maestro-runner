package devicelab_ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/webinspector"

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
	// webEvalTimeout bounds one page check; a busy page answers slowly.
	webEvalTimeout = time.Second
	// youngPage is how long after a page is first listed a failed check
	// still means "busy loading".
	youngPage = 5 * time.Second
)

// webPages tells whether an app's visible web page is still loading.
type webPages interface {
	Loading(ctx context.Context, bundleID string) (bool, error)
	Close() error
}

// pageStateJS is "hidden" for a page not on screen, else its readyState.
const pageStateJS = `document.visibilityState === "visible" ? document.readyState : "hidden"`

// inspector reads page state through the simulator's webinspectord: go-ios's
// inspector client, bridged to CDP on a local port.
type inspector struct {
	client pageLister
	addr   string
	cancel context.CancelFunc

	// socks keeps one CDP connection per page: the bridge serves a page's
	// first inspector session only, and a second connection timed out.
	mu    sync.Mutex
	socks map[int]*pageConn

	// firstSeen, dead and lastSig track pages across checks: when each was
	// first listed, which stopped answering, and each app's last listing.
	firstSeen map[int]time.Time
	dead      map[int]bool
	lastSig   map[string]string

	// evalTimeout bounds one page check (webEvalTimeout; tests shorten it).
	evalTimeout time.Duration
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
	// Serve listens in the goroutine; the first check raced it and was
	// refused. Wait until the bridge accepts connections.
	if err := waitListening(server.Addr(), webCallTimeout); err != nil {
		stop()
		_ = client.Close()
		return nil, err
	}
	return &inspector{client: client, addr: server.Addr(), cancel: stop}, nil
}

// waitListening waits until addr accepts TCP connections.
func waitListening(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			return c.Close()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("inspector bridge not listening on %s: %w", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	listed, err := in.client.ListPages(ctx, 0)
	if err != nil {
		return false, err
	}
	var pages []webinspector.Page
	for _, p := range listed {
		if p.Application.BundleID != bundleID {
			continue
		}
		if p.Page.Type != webinspector.WIRTypeWebPage && p.Page.Type != webinspector.WIRTypeWeb {
			continue // JavaScript contexts and automation sessions
		}
		if !strings.HasPrefix(p.Page.URL, "http") {
			continue // extension and internal pages never "load" for the user
		}
		pages = append(pages, p.Page)
	}

	in.mu.Lock()
	now := time.Now()
	if in.firstSeen == nil {
		in.firstSeen, in.dead, in.lastSig = map[int]time.Time{}, map[int]bool{}, map[string]string{}
	}
	sig := pagesSignature(pages)
	prev, known := in.lastSig[bundleID]
	in.lastSig[bundleID] = sig
	for _, p := range pages {
		if _, ok := in.firstSeen[p.ID]; !ok {
			in.firstSeen[p.ID] = now
		}
	}
	in.mu.Unlock()

	// A page appearing or navigating shows up in the listing before (or
	// while) it loads; DDG's tab page was listed only after its load began.
	// Count the change once, so the next check looks at the new page.
	loading := known && sig != prev
	if loading {
		logger.Debug("[devicelab-ios] web pages changed: %s", sig)
	}
	for _, p := range pages {
		in.mu.Lock()
		dead, seen := in.dead[p.ID], in.firstSeen[p.ID]
		in.mu.Unlock()
		if dead {
			continue
		}
		start := time.Now()
		state, pc, err := in.pageState(ctx, p.ID)
		busy := err == nil && state != "hidden" && (state != "complete" || pc.networkBusy(time.Now()))
		logger.Debug("[devicelab-ios] web page %d %s state=%s busy=%v err=%v (%v)", p.ID, p.URL, state, busy, err, time.Since(start).Round(time.Millisecond))
		if err != nil {
			// A page's inspector session cannot be reopened once it fails,
			// so never ask it again. A young page that does not answer is
			// busy loading; an old one is a background tab or gone.
			in.mu.Lock()
			in.dead[p.ID] = true
			in.mu.Unlock()
			if time.Since(seen) < youngPage {
				loading = true
			}
			continue
		}
		if busy {
			loading = true
		}
	}
	return loading, nil
}

// pagesSignature identifies an app's set of pages and where they are.
func pagesSignature(pages []webinspector.Page) string {
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		parts = append(parts, fmt.Sprintf("%d=%s", p.ID, p.URL))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// pageState reads one page's visibility and readyState over its reused
// connection: "hidden", or the readyState of the visible page.
func (in *inspector) pageState(ctx context.Context, pageID int) (string, *pageConn, error) {
	timeout := in.evalTimeout
	if timeout <= 0 {
		timeout = webEvalTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pc, err := in.conn(cctx, pageID)
	if err != nil {
		return "", nil, err
	}
	raw, err := pc.evaluate(cctx, pageStateJS)
	if err != nil {
		in.drop(pageID)
		return "", nil, err
	}
	var state string
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", nil, fmt.Errorf("page state %s: %w", raw, err)
	}
	return state, pc, nil
}

// conn returns the page's open connection, dialing it the first time.
func (in *inspector) conn(ctx context.Context, pageID int) (*pageConn, error) {
	in.mu.Lock()
	pc, ok := in.socks[pageID]
	in.mu.Unlock()
	if ok {
		return pc, nil
	}
	pc, err := dialPage(ctx, fmt.Sprintf("ws://%s/devtools/page/%d", in.addr, pageID))
	if err != nil {
		return nil, err
	}
	in.mu.Lock()
	if in.socks == nil {
		in.socks = map[int]*pageConn{}
	}
	in.socks[pageID] = pc
	in.mu.Unlock()
	return pc, nil
}

func (in *inspector) drop(pageID int) {
	in.mu.Lock()
	pc := in.socks[pageID]
	delete(in.socks, pageID)
	in.mu.Unlock()
	if pc != nil {
		pc.close()
	}
}

func (in *inspector) Close() error {
	in.mu.Lock()
	for id, pc := range in.socks {
		pc.close()
		delete(in.socks, id)
	}
	in.mu.Unlock()
	if in.cancel != nil {
		in.cancel()
	}
	return in.client.Close()
}

// webState is the driver's inspector, opened in the background on first
// use: connecting costs about a second, which a native app should not pay
// on its first settle. Checks start once it is ready; off if it fails.
type webState struct {
	mu      sync.Mutex
	pages   webPages
	started bool
	ready   chan struct{}
}

var errNoInspector = errors.New("web inspector unavailable")

// webPagesFor returns the inspector once it is open. The first call starts
// opening it and returns errNoInspector until it is ready; a missing socket
// or an inspector that refuses turns the check off for the session.
func (d *Driver) webPagesFor() (webPages, error) {
	d.web.mu.Lock()
	if !d.web.started {
		d.web.started = true
		d.web.ready = make(chan struct{})
		open := d.openWeb
		go func() {
			var pages webPages
			var err error = errNoInspector
			if open != nil {
				pages, err = open()
			}
			d.web.mu.Lock()
			if err != nil {
				logger.Debug("[devicelab-ios] web inspector off: %v", err)
			} else {
				d.web.pages = pages
			}
			d.web.mu.Unlock()
			close(d.web.ready)
		}()
	}
	pages := d.web.pages
	d.web.mu.Unlock()
	if pages == nil {
		return nil, errNoInspector
	}
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
