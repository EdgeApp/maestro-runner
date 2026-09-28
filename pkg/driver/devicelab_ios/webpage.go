package devicelab_ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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

// pageConn is one page's CDP connection. A reader goroutine answers calls and
// tracks network requests.
type pageConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex

	mu        sync.Mutex
	nextID    int
	pending   map[int]chan cdpReply
	inflight  map[string]time.Time
	lastEvent time.Time // last request start or end
	busySince time.Time // start of the current busy streak
	closed    chan struct{}
	err       error
}

type cdpReply struct {
	Result json.RawMessage
	Error  json.RawMessage
}

var errPageClosed = errors.New("page connection closed")

// dialPage connects to a page's CDP socket and turns on network tracking.
func dialPage(ctx context.Context, url string) (*pageConn, error) {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	pc := &pageConn{
		ws:       ws,
		nextID:   1,
		pending:  map[int]chan cdpReply{},
		inflight: map[string]time.Time{},
		closed:   make(chan struct{}),
	}
	go pc.readLoop()
	// Network tracking is a second signal; a page that refuses it still
	// answers readyState.
	_, _ = pc.call(ctx, "Network.enable", nil)
	return pc, nil
}

func (pc *pageConn) readLoop() {
	for {
		var msg struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := pc.ws.ReadJSON(&msg); err != nil {
			pc.fail(err)
			return
		}
		if msg.ID > 0 {
			pc.mu.Lock()
			ch := pc.pending[msg.ID]
			delete(pc.pending, msg.ID)
			pc.mu.Unlock()
			if ch != nil {
				ch <- cdpReply{Result: msg.Result, Error: msg.Error}
			}
			continue
		}
		pc.onEvent(msg.Method, msg.Params, time.Now())
	}
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

// call sends one CDP command and waits for its reply.
func (pc *pageConn) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	ch := make(chan cdpReply, 1)
	pc.mu.Lock()
	if pc.err != nil {
		err := pc.err
		pc.mu.Unlock()
		return nil, err
	}
	id := pc.nextID
	pc.nextID++
	pc.pending[id] = ch
	pc.mu.Unlock()
	defer func() {
		pc.mu.Lock()
		delete(pc.pending, id)
		pc.mu.Unlock()
	}()

	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	pc.writeMu.Lock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = pc.ws.SetWriteDeadline(deadline)
	}
	err := pc.ws.WriteJSON(req)
	pc.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if len(r.Error) > 0 {
			return nil, fmt.Errorf("%s: %s", method, r.Error)
		}
		return r.Result, nil
	case <-pc.closed:
		return nil, errPageClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// evaluate runs an expression and returns its JSON value.
func (pc *pageConn) evaluate(ctx context.Context, expr string) (json.RawMessage, error) {
	raw, err := pc.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true})
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

func (pc *pageConn) fail(err error) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.err != nil {
		return
	}
	pc.err = err
	close(pc.closed)
}

func (pc *pageConn) close() {
	pc.fail(errPageClosed)
	_ = pc.ws.Close()
}
