package devicelab_ios

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/danielpaulus/go-ios/ios"
)

// fakeWIRD plays webinspectord on the other end of a pipe: one app with web
// pages, sessions announced through targets, and each page answering
// Runtime.evaluate with its state ("silent" pages never answer).
type fakeWIRD struct {
	t     *testing.T
	codec ios.PlistCodecReadWriter
	conn  net.Conn

	mu       sync.Mutex
	appID    string
	bundle   string
	pages    map[int]string // page id → URL
	states   map[int]string // page id → readyState, "hidden" or "silent"
	targets  bool
	sessions map[string]int // session id → page id
	writeMu  sync.Mutex
}

func newFakeWIRD(t *testing.T, pages map[int]string, states map[int]string, targets bool) (*fakeWIRD, *wirClient) {
	t.Helper()
	server, client := net.Pipe()
	f := &fakeWIRD{
		t: t, codec: ios.NewPlistCodecReadWriter(server, server), conn: server,
		appID: "PID:42", bundle: "com.example.browser", pages: pages, states: states,
		targets: targets, sessions: map[string]int{},
	}
	go f.serve()
	t.Cleanup(func() { _ = server.Close() })
	return f, newWIRClient(client)
}

func (f *fakeWIRD) write(selector string, arg map[string]any) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_ = f.codec.Write(map[string]any{"__selector": selector, "__argument": arg})
}

func (f *fakeWIRD) listing() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := map[string]any{}
	for id, url := range f.pages {
		l[fmt.Sprint(id)] = map[string]any{"WIRPageIdentifierKey": id, "WIRTypeKey": wirTypeWebPage, "WIRURLKey": url}
	}
	return map[string]any{"WIRApplicationIdentifierKey": f.appID, "WIRListingKey": l}
}

// setPages changes the page list and pushes the new listing.
func (f *fakeWIRD) setPages(pages map[int]string) {
	f.mu.Lock()
	f.pages = pages
	f.mu.Unlock()
	f.write("_rpc_applicationSentListing:", f.listing())
}

// toSession sends one JSON message to a session, through its target when
// targets are on.
func (f *fakeWIRD) toSession(session string, msg map[string]any) {
	raw, _ := json.Marshal(msg)
	if f.targets {
		wrapped, _ := json.Marshal(map[string]any{"method": "Target.dispatchMessageFromTarget",
			"params": map[string]any{"targetId": "page-" + session, "message": string(raw)}})
		raw = wrapped
	}
	f.write("_rpc_applicationSentData:", map[string]any{"WIRApplicationIdentifierKey": f.appID,
		"WIRDestinationKey": session, "WIRMessageDataKey": raw})
}

func (f *fakeWIRD) serve() {
	for {
		var msg map[string]any
		if err := f.codec.Read(&msg); err != nil {
			return
		}
		sel, _ := msg["__selector"].(string)
		arg, _ := msg["__argument"].(map[string]any)
		switch sel {
		case "_rpc_getConnectedApplications:":
			f.write("_rpc_reportConnectedApplicationList:", map[string]any{"WIRApplicationDictionaryKey": map[string]any{
				f.appID: map[string]any{"WIRApplicationIdentifierKey": f.appID, "WIRApplicationBundleIdentifierKey": f.bundle},
				"PID:7": map[string]any{"WIRApplicationIdentifierKey": "PID:7", "WIRApplicationBundleIdentifierKey": "com.other"},
			}})
		case "_rpc_forwardGetListing:":
			if id, _ := arg["WIRApplicationIdentifierKey"].(string); id == f.appID {
				f.write("_rpc_applicationSentListing:", f.listing())
			} else {
				f.write("_rpc_applicationSentListing:", map[string]any{"WIRApplicationIdentifierKey": id,
					"WIRListingKey": map[string]any{"1": map[string]any{"WIRPageIdentifierKey": 1, "WIRTypeKey": wirTypeWebPage, "WIRURLKey": "https://other.example"}}})
			}
		case "_rpc_forwardSocketSetup:":
			session, _ := arg["WIRSenderKey"].(string)
			page, _ := plistInt(arg["WIRPageIdentifierKey"])
			f.mu.Lock()
			f.sessions[session] = page
			f.mu.Unlock()
			if f.targets {
				raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated",
					"params": map[string]any{"targetInfo": map[string]any{"targetId": "page-" + session, "type": "page"}}})
				f.write("_rpc_applicationSentData:", map[string]any{"WIRApplicationIdentifierKey": f.appID,
					"WIRDestinationKey": session, "WIRMessageDataKey": raw})
			}
		case "_rpc_forwardSocketData:":
			f.answer(arg)
		}
	}
}

func (f *fakeWIRD) answer(arg map[string]any) {
	session, _ := arg["WIRSenderKey"].(string)
	data, _ := arg["WIRSocketDataKey"].([]byte)
	var msg struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	f.mu.Lock()
	state := f.states[f.sessions[session]]
	f.mu.Unlock()
	if f.targets {
		if msg.Method != "Target.sendMessageToTarget" {
			f.t.Errorf("with targets, got %s outside a target", msg.Method)
			return
		}
		var p struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		_ = json.Unmarshal([]byte(p.Message), &msg)
		if state == "silent" && msg.Method == "Runtime.evaluate" {
			return // a silent page acknowledges nothing
		}
		ack, _ := json.Marshal(map[string]any{"id": msg.ID + 1, "result": map[string]any{}})
		f.write("_rpc_applicationSentData:", map[string]any{"WIRApplicationIdentifierKey": f.appID,
			"WIRDestinationKey": session, "WIRMessageDataKey": ack})
	}
	switch msg.Method {
	case "Runtime.evaluate":
		if state == "silent" {
			return
		}
		f.toSession(session, map[string]any{"id": msg.ID, "result": map[string]any{"result": map[string]any{"type": "string", "value": state}}})
	default:
		f.toSession(session, map[string]any{"id": msg.ID, "result": map[string]any{}})
	}
}

// sessionFor returns the session id opened on a page.
func (f *fakeWIRD) sessionFor(page int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s, p := range f.sessions {
		if p == page {
			return s
		}
	}
	return ""
}

func startInspector(t *testing.T, pages, states map[int]string, targets bool) (*fakeWIRD, *inspector) {
	t.Helper()
	f, client := newFakeWIRD(t, pages, states, targets)
	if err := client.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	in := newInspector(client)
	in.evalTimeout = 300 * time.Millisecond
	t.Cleanup(func() { _ = in.Close() })
	return f, in
}

func TestWIRListsOnlyTheAppsWebPages(t *testing.T) {
	_, in := startInspector(t, map[int]string{2: "https://a.example", 3: "https://b.example"}, nil, true)
	if got := len(in.client.webPages("com.example.browser")); got != 2 {
		t.Errorf("pages = %d, want 2", got)
	}
	if got := len(in.client.webPages("com.missing")); got != 0 {
		t.Errorf("pages of an unknown app = %d, want 0", got)
	}
}

func TestLoadingReadsTheVisiblePageThroughItsTarget(t *testing.T) {
	for _, targets := range []bool{true, false} {
		states := map[int]string{2: "hidden", 3: "loading"}
		_, in := startInspector(t, map[int]string{2: "https://tab1.example", 3: "https://tab2.example"}, states, targets)
		loading, err := in.Loading(context.Background(), "com.example.browser")
		if err != nil || !loading {
			t.Errorf("targets=%v: visible page loading: loading=%v err=%v, want true", targets, loading, err)
		}
		states[3] = "complete"
		if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
			t.Errorf("targets=%v: a complete visible page and a hidden one read as loading", targets)
		}
	}
}

func TestSessionsGetOnlyTheirOwnMessages(t *testing.T) {
	// Two pages, two sessions: each page's reply reaches its own session.
	// go-ios's bridge shared one queue, and a second session timed out.
	states := map[int]string{2: "complete", 3: "loading"}
	_, in := startInspector(t, map[int]string{2: "https://one.example", 3: "https://two.example"}, states, true)
	for round := 0; round < 3; round++ {
		for id, want := range map[int]string{2: "complete", 3: "loading"} {
			page := wirPage{AppID: "PID:42", ID: id, Type: wirTypeWebPage}
			got, _, err := in.pageState(context.Background(), page)
			if err != nil || got != want {
				t.Fatalf("round %d page %d: state=%q err=%v, want %q", round, id, got, err, want)
			}
		}
	}
	if n := len(in.pages); n != 2 {
		t.Errorf("open sessions = %d, want 2 reused", n)
	}
}

func TestNetworkEventsReachTheirPage(t *testing.T) {
	f, in := startInspector(t, map[int]string{2: "https://a.example"}, map[int]string{2: "complete"}, true)
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
		t.Fatal("an idle complete page read as loading")
	}
	f.toSession(f.sessionFor(2), map[string]any{"method": "Network.requestWillBeSent",
		"params": map[string]any{"requestId": "r1", "type": "XHR", "request": map[string]any{"url": "https://a.example/api"}}})
	time.Sleep(50 * time.Millisecond)
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); !loading {
		t.Error("a request in flight did not read as loading")
	}
	f.toSession(f.sessionFor(2), map[string]any{"method": "Network.loadingFinished", "params": map[string]any{"requestId": "r1"}})
	time.Sleep(networkQuiet + 50*time.Millisecond)
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
		t.Error("still loading after the request finished and the quiet period passed")
	}
}

func TestLoadingCountsAListingChangeOnce(t *testing.T) {
	f, in := startInspector(t, map[int]string{2: "https://one.example"}, map[int]string{2: "complete", 8: "complete"}, true)
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
		t.Error("the first listing counted as a change")
	}
	f.setPages(map[int]string{2: "https://one.example", 8: "https://two.example"})
	time.Sleep(50 * time.Millisecond)
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); !loading {
		t.Error("a new page did not count as loading")
	}
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
		t.Error("an unchanged listing still counted as loading")
	}
}

func TestSilentPageIsBusyWhenYoungThenLeftAlone(t *testing.T) {
	_, in := startInspector(t, map[int]string{4: "https://slow.example"}, map[int]string{4: "silent"}, true)
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); !loading {
		t.Error("a young page that does not answer did not read as busy")
	}
	start := time.Now()
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
		t.Error("a page in its retry backoff was asked again")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("a page in backoff cost %v", time.Since(start))
	}
	in.mu.Lock()
	in.retryAt[4] = time.Time{}
	in.firstSeen[4] = time.Now().Add(-time.Minute)
	in.mu.Unlock()
	if loading, _ := in.Loading(context.Background(), "com.example.browser"); loading {
		t.Error("an old silent page read as loading")
	}
}

func TestDisconnectFailsTheClient(t *testing.T) {
	f, in := startInspector(t, map[int]string{2: "https://a.example"}, map[int]string{2: "complete"}, true)
	_ = f.conn.Close()
	time.Sleep(50 * time.Millisecond)
	if _, err := in.Loading(context.Background(), "com.example.browser"); err == nil {
		t.Error("a closed connection was not reported")
	}
	if _, err := in.client.openSession(context.Background(), wirPage{AppID: "PID:42", ID: 2}, nil); err == nil {
		t.Error("a session opened on a closed connection")
	}
}

func TestAppDisconnectDropsItsPages(t *testing.T) {
	f, in := startInspector(t, map[int]string{2: "https://a.example"}, nil, true)
	f.write("_rpc_applicationDisconnected:", map[string]any{"WIRApplicationIdentifierKey": "PID:42"})
	time.Sleep(50 * time.Millisecond)
	if n := len(in.client.webPages("com.example.browser")); n != 0 {
		t.Errorf("pages after the app disconnected = %d, want 0", n)
	}
}

func TestTargetErrorAnswersTheCall(t *testing.T) {
	f, client := newFakeWIRD(t, map[int]string{2: "https://a.example"}, map[int]string{2: "complete"}, true)
	if err := client.start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := client.openSession(context.Background(), wirPage{AppID: "PID:42", ID: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The next wrapper id gets an error ack instead of a page reply.
	s.mu.Lock()
	next := s.nextID
	s.mu.Unlock()
	go func() {
		time.Sleep(20 * time.Millisecond)
		raw, _ := json.Marshal(map[string]any{"id": next + 1, "error": map[string]any{"message": "target gone"}})
		f.write("_rpc_applicationSentData:", map[string]any{"WIRApplicationIdentifierKey": "PID:42",
			"WIRDestinationKey": s.id, "WIRMessageDataKey": raw})
	}()
	f.mu.Lock()
	f.states[2] = "silent"
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.call(ctx, "Runtime.evaluate", map[string]any{"expression": "1"}); err == nil || ctx.Err() != nil {
		t.Errorf("a target error did not answer the call promptly: err=%v", err)
	}
	s.close()
	if _, err := s.call(context.Background(), "Runtime.evaluate", nil); err == nil {
		t.Error("a call on a closed session succeeded")
	}
}

func TestPlistInt(t *testing.T) {
	for _, v := range []any{uint64(3), int64(3), 3, float64(3)} {
		if n, ok := plistInt(v); !ok || n != 3 {
			t.Errorf("plistInt(%T) = %d, %v", v, n, ok)
		}
	}
	if _, ok := plistInt("3"); ok {
		t.Error("a string read as an integer")
	}
}

func TestVisiblePageDecidesWithoutWaitingForHiddenTabs(t *testing.T) {
	// A throttled hidden tab (silent) must not hold the check once the
	// visible page has answered.
	states := map[int]string{2: "silent", 3: "complete"}
	_, in := startInspector(t, map[int]string{2: "https://hidden.example", 3: "https://visible.example"}, states, true)
	in.evalTimeout = 2 * time.Second
	start := time.Now()
	loading, err := in.Loading(context.Background(), "com.example.browser")
	if err != nil || loading {
		t.Errorf("loading=%v err=%v, want false", loading, err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("check took %v, waiting on a hidden tab", took)
	}
	in.mu.Lock()
	_, backedOff := in.retryAt[2]
	_, kept := in.pages[2]
	in.mu.Unlock()
	if backedOff {
		t.Error("a hidden tab cut short was put in backoff")
	}
	if !kept {
		t.Error("a hidden tab cut short lost its session")
	}
}
