package devicelab_ios

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// Network activity, as the Android driver tracks it for WebViews: a page is
// busy while requests are in flight and for a quiet period after the last one
// ends. The document request starts before the new page commits, so this also
// covers the moment readyState still describes the old page.
const (
	networkQuiet = 500 * time.Millisecond
	// networkCap stops a page that never goes quiet (analytics beacons, long
	// polling) from holding every settle: after this long busy, it no longer
	// counts until its traffic stops.
	networkCap = 2 * time.Second
	// staleRequest drops a request whose finish event never came.
	staleRequest = 10 * time.Second
)

// pageConn is one page's inspector session plus its network tracking.
type pageConn struct {
	sess *wirSession

	mu        sync.Mutex
	inflight  map[string]time.Time
	lastEvent time.Time // last request start or end
	busySince time.Time // start of the current busy streak
}

type cdpReply struct {
	Result json.RawMessage
	Error  json.RawMessage
}

var errPageClosed = errors.New("page connection closed")

// openPage opens an inspector session on page and turns on network tracking.
func openPage(ctx context.Context, client *wirClient, page wirPage) (*pageConn, error) {
	pc := &pageConn{inflight: map[string]time.Time{}}
	sess, err := client.openSession(ctx, page, func(method string, params json.RawMessage) {
		pc.onEvent(method, params, time.Now())
	})
	if err != nil {
		return nil, err
	}
	pc.sess = sess
	// Network tracking is a second signal; a page that refuses it still
	// answers readyState.
	_, _ = sess.call(ctx, "Network.enable", nil)
	return pc, nil
}

// onEvent tracks request lifecycles.
func (pc *pageConn) onEvent(method string, params json.RawMessage, now time.Time) {
	var p struct {
		RequestID string `json:"requestId"`
		Type      string `json:"type"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	switch method {
	case "Network.requestWillBeSent":
		if json.Unmarshal(params, &p) != nil || ignoredRequest(p.Type, p.Request.URL) {
			return
		}
		pc.mu.Lock()
		if pc.idleLocked(now) {
			pc.busySince = now
		}
		pc.inflight[p.RequestID] = now
		pc.lastEvent = now
		pc.mu.Unlock()
	case "Network.loadingFinished", "Network.loadingFailed":
		if json.Unmarshal(params, &p) != nil {
			return
		}
		pc.mu.Lock()
		if _, ok := pc.inflight[p.RequestID]; ok {
			delete(pc.inflight, p.RequestID)
			pc.lastEvent = now
		}
		pc.mu.Unlock()
	}
}

// ignoredRequest leaves out traffic that never settles or never matters:
// open sockets and streams, inline data, and analytics pings.
func ignoredRequest(kind, url string) bool {
	switch strings.ToLower(kind) {
	case "websocket", "eventsource", "ping", "beacon":
		return true
	}
	return strings.HasPrefix(url, "data:")
}

// idleLocked reports whether no request is in flight and the last one ended
// at least networkQuiet ago. Requests older than staleRequest are dropped.
func (pc *pageConn) idleLocked(now time.Time) bool {
	for id, started := range pc.inflight {
		if now.Sub(started) > staleRequest {
			delete(pc.inflight, id)
		}
	}
	return len(pc.inflight) == 0 && (pc.lastEvent.IsZero() || now.Sub(pc.lastEvent) >= networkQuiet)
}

// networkBusy reports whether the page is loading resources, ignoring a busy
// streak longer than networkCap.
func (pc *pageConn) networkBusy(now time.Time) bool {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.idleLocked(now) {
		return false
	}
	return pc.busySince.IsZero() || now.Sub(pc.busySince) < networkCap
}

// evaluate runs an expression and returns its JSON value.
func (pc *pageConn) evaluate(ctx context.Context, expr string) (json.RawMessage, error) {
	raw, err := pc.sess.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true})
	if err != nil {
		return nil, err
	}
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Result.Value, nil
}

func (pc *pageConn) close() {
	if pc.sess != nil {
		pc.sess.close()
	}
}
