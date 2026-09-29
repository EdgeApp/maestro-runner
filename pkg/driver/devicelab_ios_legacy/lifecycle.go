package devicelab_ios_legacy

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	"github.com/devicelab-dev/maestro-runner/pkg/flow"
	"github.com/devicelab-dev/maestro-runner/pkg/logger"
)

// simctlCommand builds an `xcrun simctl` command. Tests replace it to record
// calls instead of running them.
var simctlCommand = func(args ...string) *exec.Cmd {
	return exec.Command("xcrun", append([]string{"simctl"}, args...)...)
}

func (d *Driver) simctl(args ...string) *exec.Cmd { return simctlCommand(args...) }

// launchEnv is the environment for `simctl launch`: the host's own, which
// xcrun needs (PATH, DEVELOPER_DIR), plus the app's variables under the
// SIMCTL_CHILD_ prefix simctl forwards to the app. Nil keeps the default.
func launchEnv(appEnv map[string]string) []string {
	if len(appEnv) == 0 {
		return nil
	}
	keys := make([]string, 0, len(appEnv))
	for k := range appEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := os.Environ()
	for _, k := range keys {
		env = append(env, fmt.Sprintf("SIMCTL_CHILD_%s=%s", k, appEnv[k]))
	}
	return env
}

// applyLaunchPermissions gives the app its launch permissions on a
// simulator, as Maestro and the WDA driver do: everything reset, then the
// flow's permissions — all:allow when it names none — applied on top.
// Failures are logged, not fatal: a permission simctl can't set must not
// stop the app launching.
func (d *Driver) applyLaunchPermissions(bundleID string, perms map[string]string) {
	if d.info == nil || !d.info.IsSimulator || d.udid == "" {
		return
	}
	if len(perms) == 0 {
		perms = map[string]string{"all": "allow"}
	}
	if out, err := d.simctl("privacy", d.udid, "reset", "all", bundleID).CombinedOutput(); err != nil {
		logger.Warn("launchApp: permission reset failed: %s", strings.TrimSpace(string(out)))
	}
	set := map[string]string{}
	for name, value := range perms {
		if !strings.EqualFold(strings.TrimSpace(value), "unset") {
			set[name] = value // already reset above
		}
	}
	if _, failures := d.applyPermissions(bundleID, set); len(failures) > 0 {
		logger.Warn("launchApp: permissions not applied: %s", strings.Join(failures, "; "))
	}
}

// applyPermissions sets each named permission with `simctl privacy`, one
// call per service, in a stable order. Names iOS gives the host no control
// over (notifications, faceid) and values a service doesn't take are logged
// and skipped.
func (d *Driver) applyPermissions(bundleID string, perms map[string]string) (applied int, failures []string) {
	names := make([]string, 0, len(perms))
	for name := range perms {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := perms[name]
		services := core.IOSPrivacyServices(name)
		if len(services) == 0 {
			logger.Warn("permissions: iOS has no host-side control over %q — skipping", name)
			continue
		}
		for _, service := range services {
			action, resolved, ok := core.IOSPrivacyAction(service, value)
			if !ok {
				logger.Warn("permissions: ignoring unsupported value %q for permission %q", value, name)
				continue
			}
			if out, err := d.simctl("privacy", d.udid, action, resolved, bundleID).CombinedOutput(); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %s", resolved, strings.TrimSpace(string(out))))
				continue
			}
			applied++
		}
	}
	return applied, failures
}

// resetKeychain empties the simulator's keychain. Real devices can't.
func (d *Driver) resetKeychain() error {
	if d.info == nil || !d.info.IsSimulator || d.udid == "" {
		return fmt.Errorf("clearKeychain needs a simulator")
	}
	if out, err := d.simctl("keychain", d.udid, "reset").CombinedOutput(); err != nil {
		return fmt.Errorf("simctl keychain reset: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *Driver) handleClearKeychain() *core.CommandResult {
	if err := d.resetKeychain(); err != nil {
		return core.ErrorResult(err, err.Error())
	}
	return core.SuccessResult("keychain cleared", nil)
}

// terminateApp stops an app; one that isn't running counts as stopped.
func (d *Driver) terminateApp(bundleID string) error {
	out, err := d.simctl("terminate", d.udid, bundleID).CombinedOutput()
	if err == nil {
		return nil
	}
	body := strings.ToLower(strings.TrimSpace(string(out)))
	if strings.Contains(body, "found nothing") || strings.Contains(body, "no such process") {
		return nil
	}
	return fmt.Errorf("simctl terminate failed: %s", strings.TrimSpace(string(out)))
}

// handleKillApp stops the app. iOS has no separate "kill as the system
// would" on a simulator, so it is stopApp under another name, as in WDA.
func (d *Driver) handleKillApp(s *flow.KillAppStep) *core.CommandResult {
	bid := strings.TrimSpace(s.AppID)
	if bid == "" {
		bid = d.appID
	}
	if bid == "" {
		return core.ErrorResult(fmt.Errorf("killApp requires an active app"), "no active app")
	}
	if err := d.terminateApp(bid); err != nil {
		return core.ErrorResult(err, err.Error())
	}
	return core.SuccessResult(fmt.Sprintf("killed %s", bid), nil)
}
