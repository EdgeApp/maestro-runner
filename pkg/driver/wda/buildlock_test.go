//go:build !windows

package wda

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLockFileSerialises(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.lock")
	unlock, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		if u, err := lockFile(path); err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("second lock taken while the first was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock not taken after release")
	}
}

// Runs that share a cache directory build it once: the others wait for the
// lock, then find the finished xctestrun.
func TestBuildIfMissingBuildsOnce(t *testing.T) {
	buildDir := filepath.Join(t.TempDir(), "sim-ios26.0-iphone")
	products := filepath.Join(buildDir, "DerivedData", "Build", "Products")
	if err := os.MkdirAll(products, 0o755); err != nil {
		t.Fatal(err)
	}

	var builds int32
	build := func() error {
		atomic.AddInt32(&builds, 1)
		time.Sleep(200 * time.Millisecond) // a build long enough to overlap
		return os.WriteFile(filepath.Join(products, "WebDriverAgentRunner.xctestrun"), []byte("<plist/>"), 0o644)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &Runner{buildDir: buildDir}
			errs <- r.buildIfMissing(build)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if builds != 1 {
		t.Errorf("builds = %d, want 1", builds)
	}
}
