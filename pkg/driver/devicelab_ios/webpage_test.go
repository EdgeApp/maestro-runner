package devicelab_ios

import (
	"encoding/json"
	"testing"
	"time"
)

func request(id, kind, url string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"requestId": id, "type": kind, "request": map[string]any{"url": url}})
	return b
}

func finished(id string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"requestId": id})
	return b
}

func newTracker() *pageConn {
	return &pageConn{inflight: map[string]time.Time{}}
}

func TestNetworkBusyWhileRequestsRunAndBriefly(t *testing.T) {
	pc := newTracker()
	t0 := time.Now()
	if pc.networkBusy(t0) {
		t.Fatal("busy before any request")
	}
	pc.onEvent("Network.requestWillBeSent", request("1", "Document", "https://a.example/"), t0)
	if !pc.networkBusy(t0.Add(100 * time.Millisecond)) {
		t.Error("not busy with a document request in flight")
	}
	pc.onEvent("Network.loadingFinished", finished("1"), t0.Add(200*time.Millisecond))
	if !pc.networkBusy(t0.Add(300 * time.Millisecond)) {
		t.Error("not busy within the quiet period after the last request")
	}
	if pc.networkBusy(t0.Add(200*time.Millisecond + networkQuiet)) {
		t.Error("still busy after the quiet period")
	}
}

func TestNetworkIgnoresStreamsPingsAndData(t *testing.T) {
	pc := newTracker()
	t0 := time.Now()
	pc.onEvent("Network.requestWillBeSent", request("1", "WebSocket", "wss://a.example/"), t0)
	pc.onEvent("Network.requestWillBeSent", request("2", "Ping", "https://a.example/beacon"), t0)
	pc.onEvent("Network.requestWillBeSent", request("3", "Image", "data:image/gif;base64,AA"), t0)
	pc.onEvent("Network.requestWillBeSent", request("4", "Beacon", "https://a.example/b"), t0)
	if pc.networkBusy(t0.Add(10 * time.Millisecond)) {
		t.Error("busy on ignored traffic")
	}
}

func TestNetworkBusyStreakIsCapped(t *testing.T) {
	pc := newTracker()
	t0 := time.Now()
	pc.onEvent("Network.requestWillBeSent", request("1", "XHR", "https://a.example/poll"), t0)
	if !pc.networkBusy(t0.Add(time.Second)) {
		t.Error("not busy one second into a request")
	}
	if pc.networkBusy(t0.Add(networkCap + 10*time.Millisecond)) {
		t.Error("a page busy past the cap still counts")
	}
}

func TestNetworkDropsStaleRequestsAndFailures(t *testing.T) {
	pc := newTracker()
	t0 := time.Now()
	pc.onEvent("Network.requestWillBeSent", request("1", "Fetch", "https://a.example/x"), t0)
	if pc.networkBusy(t0.Add(staleRequest + time.Second)) {
		t.Error("a request with no finish event is still in flight after staleRequest")
	}
	pc.onEvent("Network.requestWillBeSent", request("2", "Fetch", "https://a.example/y"), t0)
	pc.onEvent("Network.loadingFailed", finished("2"), t0.Add(50*time.Millisecond))
	pc.onEvent("Network.loadingFinished", finished("unknown"), t0.Add(60*time.Millisecond))
	pc.onEvent("Network.requestWillBeSent", json.RawMessage(`not json`), t0)
	if pc.networkBusy(t0.Add(50*time.Millisecond + networkQuiet)) {
		t.Error("a failed request kept the page busy")
	}
}
