package flutter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// An installed app is Flutter exactly when it embeds Flutter.framework; when
// the bundle cannot be read the answer stays "maybe" (true), so discovery runs.
func TestIsFlutterBundle(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "Plain.app")
	flutterApp := filepath.Join(dir, "Flutter.app")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(flutterApp, "Frameworks", "Flutter.framework"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := appContainer
	defer func() { appContainer = old }()

	for _, tc := range []struct {
		name string
		path string
		err  error
		want bool
	}{
		{"plain app", plain, nil, false},
		{"flutter app", flutterApp, nil, true},
		{"not installed", "", errors.New("no such app"), true},
		{"unreadable path", filepath.Join(dir, "gone.app"), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appContainer = func(string, string) (string, error) { return tc.path, tc.err }
			if got := isFlutterBundle("SIM-1", "com.example"); got != tc.want {
				t.Errorf("isFlutterBundle = %v, want %v", got, tc.want)
			}
		})
	}
	if !isFlutterBundle("", "com.example") {
		t.Error("no UDID should keep discovery (true)")
	}
}
