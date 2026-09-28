package devicelab_ios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/google/uuid"
)

// A client for WebKit's Remote Inspector protocol, as webinspectord speaks
// it: on a simulator over its RWI_LISTEN_SOCKET, on a device over the
// com.apple.webinspector service. Each message is a length-prefixed plist
// {__selector, __argument}. Page traffic for an inspector session comes back
// addressed to that session (WIRDestinationKey), and is routed to it here;
// go-ios's bridge put every page's events on one queue, so a second session
// took the first's replies and a new page's session timed out.

const (
	wirTypeWebPage = "WIRTypeWebPage"
	wirTypeWeb     = "WIRTypeWeb"
	// targetWait is how long a new session waits for its page's target;
	// without one, commands go to the page directly (older WebKit).
	targetWait = time.Second
)

// wirPage is one inspectable page of an app.
type wirPage struct {
	AppID string
	ID    int
	Type  string
	URL   string
}

// pageKey names a page across apps: page ids restart at 1 in every app
// process, so an id alone is not unique after a relaunch.
type pageKey struct {
	app string
	id  int
}

func (p wirPage) key() pageKey { return pageKey{p.AppID, p.ID} }

// isListed reports whether the page is in its app's current listing.
func (c *wirClient) isListed(k pageKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pages[k.app][k.id]
	return ok
}

// wirClient is one connection to webinspectord.
type wirClient struct {
	conn    io.ReadWriteCloser
	codec   ios.PlistCodecReadWriter
	connID  string
	writeMu sync.Mutex

	mu       sync.Mutex
	apps     map[string]string          // application id → bundle id
	pages    map[string]map[int]wirPage // application id → page id → page
	sessions map[string]*wirSession
	awaiting map[string]bool // apps whose first listing has not come yet
	reported bool            // the connected-application list has come
	listed   chan struct{}   // closed once every reported app has listed
	closed   chan struct{}
	err      error
}

func newWIRClient(conn io.ReadWriteCloser) *wirClient {
	return &wirClient{
		conn:     conn,
		codec:    ios.NewPlistCodecReadWriter(conn, conn),
		connID:   strings.ToUpper(uuid.New().String()),
		apps:     map[string]string{},
		pages:    map[string]map[int]wirPage{},
		sessions: map[string]*wirSession{},
		awaiting: map[string]bool{},
		listed:   make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

// start identifies this connection and waits, up to listingWait, until every
// connected app has sent its page listing (pushed on every change after that).
func (c *wirClient) start(ctx context.Context) error {
	go c.readLoop()
	if err := c.send("_rpc_reportIdentifier:", nil); err != nil {
		return err
	}
	if err := c.send("_rpc_getConnectedApplications:", nil); err != nil {
		return err
	}
	select {
	case <-c.listed:
	case <-c.closed:
		return c.failure()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(listingWait):
		// no inspectable app yet: fine, listings arrive when one appears
	}
	return nil
}

func (c *wirClient) send(selector string, arg map[string]any) error {
	if arg == nil {
		arg = map[string]any{}
	}
	arg["WIRConnectionIdentifierKey"] = c.connID
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.codec.Write(map[string]any{"__selector": selector, "__argument": arg})
}

func (c *wirClient) readLoop() {
	for {
		var msg map[string]any
		if err := c.codec.Read(&msg); err != nil {
			c.fail(err)
			return
		}
		selector, _ := msg["__selector"].(string)
		arg, _ := msg["__argument"].(map[string]any)
		c.handle(selector, arg)
	}
}

func (c *wirClient) handle(selector string, arg map[string]any) {
	switch selector {
	case "_rpc_reportConnectedApplicationList:":
		apps, _ := arg["WIRApplicationDictionaryKey"].(map[string]any)
		c.mu.Lock()
		for id := range apps {
			c.awaiting[id] = true
		}
		c.reported = true
		c.checkListedLocked()
		c.mu.Unlock()
		for _, raw := range apps {
			c.addApp(raw)
		}
	case "_rpc_applicationConnected:", "_rpc_applicationUpdated:":
		c.addApp(arg)
	case "_rpc_applicationDisconnected:":
		id, _ := arg["WIRApplicationIdentifierKey"].(string)
		c.mu.Lock()
		delete(c.apps, id)
		delete(c.pages, id)
		delete(c.awaiting, id)
		var gone []*wirSession
		for _, s := range c.sessions {
			if s.page.AppID == id {
				gone = append(gone, s)
			}
		}
		c.mu.Unlock()
		for _, s := range gone {
			s.fail(errPageClosed) // the app is gone: its pages will not answer
		}
	case "_rpc_applicationSentListing:":
		c.setListing(arg)
	case "_rpc_applicationSentData:":
		dest, _ := arg["WIRDestinationKey"].(string)
		data, _ := arg["WIRMessageDataKey"].([]byte)
		c.mu.Lock()
		s := c.sessions[dest]
		c.mu.Unlock()
		if s != nil {
			s.deliver(data)
		}
	}
}

func (c *wirClient) addApp(raw any) {
	app, _ := raw.(map[string]any)
	id, _ := app["WIRApplicationIdentifierKey"].(string)
	if id == "" {
		return
	}
	bundle, _ := app["WIRApplicationBundleIdentifierKey"].(string)
	c.mu.Lock()
	c.apps[id] = bundle
	c.mu.Unlock()
	// Never write from the read loop: while webinspectord is writing to us,
	// a write here would wait on it and it on us.
	go func() { _ = c.send("_rpc_forwardGetListing:", map[string]any{"WIRApplicationIdentifierKey": id}) }()
}

func (c *wirClient) setListing(arg map[string]any) {
	appID, _ := arg["WIRApplicationIdentifierKey"].(string)
	listing, _ := arg["WIRListingKey"].(map[string]any)
	pages := map[int]wirPage{}
	for _, raw := range listing {
		p, _ := raw.(map[string]any)
		id, ok := plistInt(p["WIRPageIdentifierKey"])
		if !ok {
			continue
		}
		typ, _ := p["WIRTypeKey"].(string)
		url, _ := p["WIRURLKey"].(string)
		pages[id] = wirPage{AppID: appID, ID: id, Type: typ, URL: url}
	}
	c.mu.Lock()
	c.pages[appID] = pages
	delete(c.awaiting, appID)
	c.checkListedLocked()
	c.mu.Unlock()
}

// checkListedLocked marks the client listed once every reported app has
// sent its first listing.
func (c *wirClient) checkListedLocked() {
	if !c.reported || len(c.awaiting) > 0 {
		return
	}
	select {
	case <-c.listed:
	default:
		close(c.listed)
	}
}

// webPages lists an app's web pages by bundle id.
func (c *wirClient) webPages(bundleID string) []wirPage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []wirPage
	for appID, pages := range c.pages {
		if c.apps[appID] != bundleID {
			continue
		}
		for _, p := range pages {
			if p.Type == wirTypeWebPage || p.Type == wirTypeWeb {
				out = append(out, p)
			}
		}
	}
	return out
}

func (c *wirClient) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	close(c.closed)
	for _, s := range c.sessions {
		s.fail(err)
	}
}

func (c *wirClient) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *wirClient) close() error {
	c.fail(errPageClosed)
	return c.conn.Close()
}

// wirSession is one inspector session on one page. Commands go through the
// page's target (Target.sendMessageToTarget) when WebKit announces one.
type wirSession struct {
	client  *wirClient
	page    wirPage
	id      string
	onEvent func(method string, params json.RawMessage)

	mu       sync.Mutex
	targetID string
	target   chan struct{} // closed when the target is known
	nextID   int
	pending  map[int]chan cdpReply
	wrapper  map[int]int // Target.sendMessageToTarget id → page command id
	closed   chan struct{}
	err      error
}

// openSession sets up an inspector session on page; events go to onEvent.
func (c *wirClient) openSession(ctx context.Context, page wirPage, onEvent func(string, json.RawMessage)) (*wirSession, error) {
	s := &wirSession{
		client:  c,
		page:    page,
		id:      strings.ToUpper(uuid.New().String()),
		onEvent: onEvent,
		target:  make(chan struct{}),
		nextID:  1,
		pending: map[int]chan cdpReply{},
		wrapper: map[int]int{},
		closed:  make(chan struct{}),
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.sessions[s.id] = s
	c.mu.Unlock()
	err := c.send("_rpc_forwardSocketSetup:", map[string]any{
		"WIRApplicationIdentifierKey":         page.AppID,
		"WIRPageIdentifierKey":                page.ID,
		"WIRSenderKey":                        s.id,
		"WIRAutomaticallyPause":               false,
		"WIRMessageDataTypeChunkSupportedKey": 0,
	})
	if err != nil {
		s.close()
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, targetWait)
	defer cancel()
	select {
	case <-s.target:
	case <-s.closed:
		return nil, s.err
	case <-wait.Done():
		if ctx.Err() != nil {
			s.close()
			return nil, ctx.Err()
		}
		// no target announced: talk to the page directly
	}
	return s, nil
}

// deliver handles one message WebKit sent to this session.
func (s *wirSession) deliver(data []byte) {
	var msg struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	switch msg.Method {
	case "Target.targetCreated":
		var p struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
			} `json:"targetInfo"`
		}
		if json.Unmarshal(msg.Params, &p) == nil && p.TargetInfo.TargetID != "" {
			s.mu.Lock()
			if s.targetID == "" {
				s.targetID = p.TargetInfo.TargetID
				close(s.target)
			}
			s.mu.Unlock()
		}
		return
	case "Target.dispatchMessageFromTarget":
		var p struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.deliver([]byte(p.Message))
		}
		return
	}
	if msg.Method != "" {
		if s.onEvent != nil {
			s.onEvent(msg.Method, msg.Params)
		}
		return
	}
	s.mu.Lock()
	if inner, ok := s.wrapper[msg.ID]; ok {
		// The target's acknowledgement: only an error answers the command
		// (the page's own reply comes through dispatchMessageFromTarget).
		delete(s.wrapper, msg.ID)
		ch := s.pending[inner]
		if len(msg.Error) > 0 && ch != nil {
			delete(s.pending, inner)
			s.mu.Unlock()
			ch <- cdpReply{Error: msg.Error}
			return
		}
		s.mu.Unlock()
		return
	}
	ch := s.pending[msg.ID]
	delete(s.pending, msg.ID)
	s.mu.Unlock()
	if ch != nil {
		ch <- cdpReply{Result: msg.Result, Error: msg.Error}
	}
}

// call sends one command to the page and waits for its reply. Through a
// target the outer acknowledgement is ignored and the page's own reply,
// carrying the inner id, answers the call.
func (s *wirSession) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	ch := make(chan cdpReply, 1)
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	id := s.nextID
	s.nextID += 2 // odd ids for page commands, even for the target wrapper
	s.pending[id] = ch
	target := s.targetID
	if target != "" {
		s.wrapper[id+1] = id
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		delete(s.wrapper, id+1)
		s.mu.Unlock()
	}()

	inner := map[string]any{"id": id, "method": method}
	if params != nil {
		inner["params"] = params
	}
	msg := inner
	if target != "" {
		raw, err := json.Marshal(inner)
		if err != nil {
			return nil, err
		}
		msg = map[string]any{"id": id + 1, "method": "Target.sendMessageToTarget",
			"params": map[string]any{"targetId": target, "message": string(raw)}}
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	err = s.client.send("_rpc_forwardSocketData:", map[string]any{
		"WIRApplicationIdentifierKey": s.page.AppID,
		"WIRPageIdentifierKey":        s.page.ID,
		"WIRSessionIdentifierKey":     s.id,
		"WIRSenderKey":                s.id,
		"WIRSocketDataKey":            data,
	})
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if len(r.Error) > 0 {
			return nil, fmt.Errorf("%s: %s", method, r.Error)
		}
		return r.Result, nil
	case <-s.closed:
		return nil, s.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *wirSession) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return
	}
	s.err = err
	close(s.closed)
}

// close ends the session on the device and forgets it.
func (s *wirSession) close() {
	s.fail(errPageClosed)
	c := s.client
	c.mu.Lock()
	delete(c.sessions, s.id)
	c.mu.Unlock()
	_ = c.send("_rpc_forwardDidClose:", map[string]any{
		"WIRApplicationIdentifierKey": s.page.AppID,
		"WIRPageIdentifierKey":        s.page.ID,
		"WIRSenderKey":                s.id,
	})
}

// plistInt reads a plist integer, which decodes as one of several types.
func plistInt(v any) (int, bool) {
	switch n := v.(type) {
	case uint64:
		return int(n), true
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}
