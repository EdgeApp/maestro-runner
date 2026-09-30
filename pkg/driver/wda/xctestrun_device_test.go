package wda

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBuiltXctestrun(t *testing.T) (*Runner, string) {
	t.Helper()
	r := &Runner{buildDir: t.TempDir()}
	products := filepath.Join(r.derivedDataPath(), "Build", "Products")
	if err := os.MkdirAll(products, 0o755); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(products, "WebDriverAgentRunner_iphonesimulator26.0-arm64.xctestrun")
	if err := os.WriteFile(built, []byte("<plist>built</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return r, built
}

// Two devices get separate copies beside the build, so each can carry its
// own port without touching the other's.
func TestXctestrunForDeviceSeparatesDevices(t *testing.T) {
	_, built := writeBuiltXctestrun(t)
	a, err := xctestrunForDevice(built, "AAAA")
	if err != nil {
		t.Fatal(err)
	}
	b, err := xctestrunForDevice(built, "BBBB")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == built || b == built {
		t.Fatalf("copies must be distinct from each other and the build: %s %s", a, b)
	}
	if filepath.Dir(a) != filepath.Dir(built) {
		t.Errorf("copy must sit beside the build (__TESTROOT__ is its directory): %s", a)
	}
	if err := os.WriteFile(a, []byte("<plist>port A</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{built, b} {
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), "port A") {
			t.Errorf("editing device A's copy changed %s", p)
		}
	}
	if _, err := xctestrunForDevice(filepath.Join(t.TempDir(), "missing.xctestrun"), "CCCC"); err == nil {
		t.Error("expected an error for a missing build")
	}
}

// Per-device copies share the build's directory and extension; the cache
// lookup must keep returning the build itself.
func TestFindXctestrunSkipsDeviceCopies(t *testing.T) {
	r, built := writeBuiltXctestrun(t)
	if _, err := xctestrunForDevice(built, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Fatal(err)
	}
	got, err := r.findXctestrun()
	if err != nil {
		t.Fatal(err)
	}
	if got != built {
		t.Errorf("findXctestrun = %s, want the build %s", got, built)
	}

	// With only a device copy left, the cache counts as not built.
	if err := os.Remove(built); err != nil {
		t.Fatal(err)
	}
	if _, err := r.findXctestrun(); err == nil {
		t.Error("a device copy alone must not count as a cached build")
	}
}
