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
	// pageRetry is how long a page whose check failed is left alone.
	pageRetry = 5 * time.Second
	// sessionOpenTimeout bounds opening a page's session, which includes
	// waiting up to targetWait for its target.
	sessionOpenTimeout = 3 * time.Second
)

// webPages tells whether an app's visible web page is still loading.
type webPages interface {
	Loading(ctx context.Context, bundleID string) (bool, error)
	Close() error
}

// pageStateJS is "hidden" for a page not on screen, else its readyState.
const pageStateJS = `document.visibilityState === "visible" ? document.readyState : "hidden"`

// inspector reads page state through webinspectord with our own client.
type inspector struct {
	client *wirClient

	mu        sync.Mutex
	pages     map[pageKey]*pageConn // open sessions
	firstSeen map[pageKey]time.Time
	retryAt   map[pageKey]time.Time // a failed page is not asked before this
	lastSig   map[string]string     // bundle id → last listing

	// evalTimeout bounds one page check (webEvalTimeout; tests shorten it).
	evalTimeout time.Duration
}

// openInspector connects to a simulator's webinspectord socket.
func openInspector(ctx context.Context, socket string) (*inspector, error) {
	conn, err := net.DialTimeout("unix", socket, webCallTimeout)
	if err != nil {
		return nil, err
	}
	client := newWIRClient(conn)
	cctx, cancel := context.WithTimeout(ctx, 2*webCallTimeout)
	defer cancel()
	if err := client.start(cctx); err != nil {
		_ = client.close()
		return nil, err
	}
	return newInspector(client), nil
}

func newInspector(client *wirClient) *inspector {
	return &inspector{
		client:    client,
		pages:     map[pageKey]*pageConn{},
		firstSeen: map[pageKey]time.Time{},
		retryAt:   map[pageKey]time.Time{},
		lastSig:   map[string]string{},
	}
}

func (in *inspector) Loading(ctx context.Context, bundleID string) (bool, error) {
	if err := in.client.failure(); err != nil {
		return false, err
	}
	var pages []wirPage
	for _, p := range in.client.webPages(bundleID) {
		if strings.HasPrefix(p.URL, "http") {
			pages = append(pages, p) // extension and internal pages never "load" for the user
		}
	}

	now := time.Now()
	in.mu.Lock()
	sig := pagesSignature(pages)
	prev, known := in.lastSig[bundleID]
	in.lastSig[bundleID] = sig
	for _, p := range pages {
		if _, ok := in.firstSeen[p.key()]; !ok {
			in.firstSeen[p.key()] = now
		}
	}
	in.mu.Unlock()
	in.forgetGone()

	// A page appearing or navigating shows up in the listing before (or
	// while) it loads; DDG's tab page was listed only after its load began.
	// Count the change once, so the next check looks at the new page.
	loading := known && sig != prev
	if loading {
		logger.Debug("[devicelab-ios] web pages changed: %s", sig)
	}

	// Ask every page at once and stop at the visible one: only one page is
	// on screen and its state decides. A hidden tab's web process is
	// throttled and can take a second or more to answer; asked one by one,
	// it held the whole check.
	type answer struct {
		page  wirPage
		state string
		pc    *pageConn
		err   error
		took  time.Duration
	}
	pctx, stop := context.WithCancel(ctx)
	defer stop()
	answers := make(chan answer, len(pages))
	asked := 0
	for _, p := range pages {
		in.mu.Lock()
		retry := in.retryAt[p.key()]
		in.mu.Unlock()
		if now.Before(retry) {
			continue
		}
		asked++
		go func(p wirPage) {
			start := time.Now()
			state, pc, err := in.pageState(pctx, p)
			answers <- answer{p, state, pc, err, time.Since(start)}
		}(p)
	}
	visible := false
	silentYoung := false
	for i := 0; i < asked; i++ {
		a := <-answers
		if visible && a.err != nil {
			continue // cut short once the visible page answered
		}
		busy := a.err == nil && a.state != "hidden" && (a.state != "complete" || a.pc.networkBusy(time.Now()))
		logger.Debug("[devicelab-ios] web page %d %s state=%s busy=%v err=%v (%v)", a.page.ID, a.page.URL, a.state, busy, a.err, a.took.Round(time.Millisecond))
		if a.err != nil {
			// Leave a failing page alone for a while, like the Android
			// driver's connect backoff. A young page that does not answer is
			// busy loading; an old one is a background tab or gone.
			in.mu.Lock()
			in.retryAt[a.page.key()] = time.Now().Add(pageRetry)
			seen := in.firstSeen[a.page.key()]
			in.mu.Unlock()
			if time.Since(seen) < youngPage {
				silentYoung = true
			}
			continue
		}
		if a.state != "hidden" {
			visible = true
			loading = loading || busy
			stop() // the rest are hidden tabs
		}
	}
	if !visible && silentYoung {
		loading = true
	}
	return loading, nil
}

// forgetGone closes sessions, and drops the history, of pages no longer
// listed: an app that relaunched starts its page ids again at 1, and a new
// page must not inherit the old one's session, backoff or age.
func (in *inspector) forgetGone() {
	in.mu.Lock()
	var gone []*pageConn
	for key, pc := range in.pages {
		if !in.client.isListed(key) {
			gone = append(gone, pc)
			delete(in.pages, key)
		}
	}
	for key := range in.firstSeen {
		if !in.client.isListed(key) {
			delete(in.firstSeen, key)
			delete(in.retryAt, key)
		}
	}
	in.mu.Unlock()
	for _, pc := range gone {
		pc.close()
	}
}

// pagesSignature identifies an app's set of pages and where they are.
func pagesSignature(pages []wirPage) string {
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		parts = append(parts, fmt.Sprintf("%d=%s", p.ID, p.URL))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// pageState reads one page's visibility and readyState over its session:
// "hidden", or the readyState of the visible page.
func (in *inspector) pageState(ctx context.Context, page wirPage) (string, *pageConn, error) {
	timeout := in.evalTimeout
	if timeout <= 0 {
		timeout = webEvalTimeout
	}
	// Opening a session (setup, target, network) has its own budget: tied
	// to the check's, a page without targets could never finish opening.
	octx, ocancel := context.WithTimeout(ctx, sessionOpenTimeout)
	pc, err := in.conn(octx, page)
	ocancel()
	if err != nil {
		return "", nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	raw, err := pc.evaluate(cctx, pageStateJS)
	if err != nil {
		// A page that is only slow (a throttled hidden tab) keeps its
		// session; a protocol failure closes it.
		if cctx.Err() == nil {
			in.drop(page.key())
		}
		return "", nil, err
	}
	var state string
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", nil, fmt.Errorf("page state %s: %w", raw, err)
	}
	return state, pc, nil
}

// conn returns the page's open session, opening it the first time.
func (in *inspector) conn(ctx context.Context, page wirPage) (*pageConn, error) {
	in.mu.Lock()
	pc, ok := in.pages[page.key()]
	in.mu.Unlock()
	if ok {
		return pc, nil
	}
	pc, err := openPage(ctx, in.client, page)
	if err != nil {
		return nil, err
	}
	in.mu.Lock()
	in.pages[page.key()] = pc
	in.mu.Unlock()
	return pc, nil
}

func (in *inspector) drop(key pageKey) {
	in.mu.Lock()
	pc := in.pages[key]
	delete(in.pages, key)
	in.mu.Unlock()
	if pc != nil {
		pc.close()
	}
}

func (in *inspector) Close() error {
	in.mu.Lock()
	for id, pc := range in.pages {
		pc.close()
		delete(in.pages, id)
	}
	in.mu.Unlock()
	return in.client.close()
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
