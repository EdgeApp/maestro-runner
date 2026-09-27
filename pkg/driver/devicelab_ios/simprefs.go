package devicelab_ios

import (
	"os"
	"strings"

	"github.com/devicelab-dev/maestro-runner/pkg/logger"
)

// SimPrefsEnv turns the simulator preferences off when set to "0".
const SimPrefsEnv = "DEVICELAB_IOS_SIM_PREFS"

// simPref is one `defaults write` inside the simulator.
type simPref struct {
	domain, key, kind, value, why string
}

// simPrefs are the settings Appium applies to a simulator for automation.
// They are written into the simulator and stay there after the run.
var simPrefs = []simPref{
	// Crossfades instead of slide transitions: shorter animations to settle.
	{"com.apple.Accessibility", "ReduceMotionEnabled", "-int", "1", "Reduce Motion on"},
	// No "Save password?" sheet over login flows. Appium's key; not yet
	// checked against an iOS 26 simulator (an unknown key is inert).
	{"com.apple.WebUI", "AutoFillPasswords", "-int", "0", "password AutoFill off"},
}

// ApplySimulatorPrefs writes simPrefs into a booted simulator, so apps
// launched afterwards start with them. Set DEVICELAB_IOS_SIM_PREFS=0 to
// leave the simulator as it is — an app that behaves differently under
// Reduce Motion needs that. Failures are logged; none stops a run.
func ApplySimulatorPrefs(udid string) {
	if os.Getenv(SimPrefsEnv) == "0" || udid == "" {
		return
	}
	var applied []string
	for _, p := range simPrefs {
		out, err := simctlCommand("spawn", udid, "defaults", "write", p.domain, p.key, p.kind, p.value).CombinedOutput()
		if err != nil {
			logger.Warn("simulator pref %s.%s not set: %s", p.domain, p.key, strings.TrimSpace(string(out)))
			continue
		}
		applied = append(applied, p.why)
	}
	if len(applied) > 0 {
		logger.Info("Simulator prefs: %s (%s=0 to skip)", strings.Join(applied, ", "), SimPrefsEnv)
	}
}
