package devicelab

import (
	"errors"
	"strings"
	"testing"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
)

// scriptedShell answers each shell command through fn.
type scriptedShell struct {
	commands []string
	fn       func(cmd string) (string, error)
}

func (s *scriptedShell) Shell(cmd string) (string, error) {
	s.commands = append(s.commands, cmd)
	return s.fn(cmd)
}

// The launch wait is bounded on the device, and a wait that times out after
// the intent went out still counts as a launch; a real error does not.
func TestLaunchAppViaShell_BoundedWait(t *testing.T) {
	launchOut := "Starting: Intent { act=android.intent.action.MAIN }\n"
	for _, tc := range []struct {
		name    string
		out     string
		err     error
		success bool
	}{
		{"completes", launchOut + "Status: ok\n", nil, true},
		{"wait times out", launchOut, errors.New("exit status 124"), true},
		{"real error", "Error: Activity class does not exist.\n", errors.New("exit status 1"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh := &scriptedShell{fn: func(cmd string) (string, error) {
				switch {
				case strings.HasPrefix(cmd, "getprop"):
					return "34\n", nil
				case strings.Contains(cmd, "resolve-activity"):
					return "priority=0\ncom.app/com.app.Main\n", nil
				case strings.Contains(cmd, "am start-activity"):
					return tc.out, tc.err
				}
				return "", nil
			}}
			d := New(newTrackingClient(), &core.PlatformInfo{}, sh)
			res := d.launchAppViaShell("com.app", map[string]interface{}{"k": "v"})
			if res.Success != tc.success {
				t.Errorf("success = %v, want %v (%v)", res.Success, tc.success, res.Error)
			}
			var launch string
			for _, c := range sh.commands {
				if strings.Contains(c, "am start-activity") {
					launch = c
				}
			}
			if !strings.HasPrefix(launch, "timeout 30 am start-activity -W") {
				t.Errorf("launch not bounded: %q", launch)
			}
		})
	}
}
