package devicelab

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A peer that accepts the connection and never answers must not hold a read
// past the dial context's deadline (the WebSocket handshake hung 11 minutes);
// once cleared, the connection has no deadline.
func TestUnixDialerDeadline(t *testing.T) {
	dir, err := os.MkdirTemp("", "ud")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(3 * time.Second) // accept, never answer
		}
	}()

	d := &unixDialer{socketPath: sock}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	conn, err := d.DialContext(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	start := time.Now()
	_, err = conn.Read(make([]byte, 1))
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("read returned %v after %v; want a deadline error within ~200ms", err, time.Since(start))
	}

	d.clearDeadline()
}
