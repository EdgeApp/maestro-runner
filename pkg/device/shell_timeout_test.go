package device

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ShellTimeout kills an adb call that runs past its deadline and says so;
// one that finishes in time returns its output.
func TestShellTimeout(t *testing.T) {
	t.Cleanup(func() { execCommand = exec.Command })
	d := &AndroidDevice{serial: "x", adbPath: "adb"}

	execCommand = func(name string, args ...string) *exec.Cmd { return exec.Command("sh", "-c", "echo hi") }
	if out, err := d.ShellTimeout("echo hi", time.Second); err != nil || strings.TrimSpace(out) != "hi" {
		t.Fatalf("fast command: out=%q err=%v", out, err)
	}

	execCommand = func(name string, args ...string) *exec.Cmd { return exec.Command("sleep", "5") }
	start := time.Now()
	_, err := d.ShellTimeout("hang", 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("hung command: err=%v, want a timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("timeout took %v, want about 200ms", time.Since(start))
	}
}
