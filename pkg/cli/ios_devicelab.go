package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	dlios "github.com/devicelab-dev/maestro-runner/pkg/driver/devicelab_ios"
	"github.com/devicelab-dev/maestro-runner/pkg/flutter"
)

// createDevicelabIOSDriver constructs the iOS driver for --driver devicelab:
// the prebuilt devicelab-ios-agent (drivers/ios/devicelab-ios-agent/) on a
// booted simulator. The agent stays up between runs; a later run re-attaches
// to it instead of starting it again.
func createDevicelabIOSDriver(cfg *RunConfig) (core.Driver, func(), error) {
	udid := getFirstDevice(cfg)
	if udid == "" {
		printSetupStep("Finding iOS simulator...")
		var err error
		udid, err = findBootedSimulator()
		if err != nil || udid == "" {
			return nil, nil, fmt.Errorf("--driver devicelab on iOS needs a booted simulator (real devices: use --driver wda)")
		}
		printSetupSuccess(fmt.Sprintf("Found simulator: %s", udid))
	}
	if !isIOSSimulator(udid) {
		return nil, nil, fmt.Errorf("--driver devicelab on iOS supports simulators only; %s looks like a physical device (use --driver wda)", udid)
	}

	if cfg.AppFile != "" && !cfg.NoAppInstall {
		printSetupStep(fmt.Sprintf("Installing app: %s", cfg.AppFile))
		if err := installIOSApp(udid, cfg.AppFile, true); err != nil {
			return nil, nil, fmt.Errorf("install app failed: %w", err)
		}
		printSetupSuccess("App installed")
	}

	// Before the agent and the app start, so both launch with them.
	dlios.ApplySimulatorPrefs(udid)

	ctx := context.Background()
	printSetupStep("Starting devicelab iOS agent...")
	agent, client, err := dlios.StartAgent(ctx, dlios.AgentOptions{UDID: udid, ReadyTimeout: 600 * time.Second})
	if err != nil {
		return nil, nil, fmt.Errorf("devicelab iOS agent: %w", err)
	}
	printSetupSuccess(fmt.Sprintf("Agent ready on port %d (%s)", agent.Port(), agent.Mode()))
	release := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		agent.Release(stopCtx, client)
	}

	deviceInfo, err := getIOSDeviceInfo(udid)
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("get device info: %w", err)
	}

	// Screen size in points from the agent's own view of the display.
	screenW, screenH := 0, 0
	if resp, err := client.Call(ctx, "snapshot", &dlios.Args{MaxNodes: 1}); err == nil && resp.Data != nil {
		screenW, screenH = int(resp.Data.ScreenW), int(resp.Data.ScreenH)
	}

	appVersion, appBuild := "", ""
	if cfg.AppID != "" {
		appVersion, appBuild = getIOSAppVersionAndBuild(udid, cfg.AppID)
	}
	if appVersion == "" && appBuild == "" && cfg.AppFile != "" {
		appVersion, appBuild = readBundleVersionAndBuild(cfg.AppFile)
	}

	info := &core.PlatformInfo{
		Platform:     "ios",
		OSVersion:    deviceInfo.OSVersion,
		DeviceName:   deviceInfo.Name,
		DeviceID:     udid,
		IsSimulator:  true,
		ScreenWidth:  screenW,
		ScreenHeight: screenH,
		AppID:        cfg.AppID,
		AppVersion:   appVersion,
		AppBuild:     appBuild,
	}

	drv := dlios.NewDriver(client, info, udid)
	if cfg.AppID != "" {
		drv.SetAppID(cfg.AppID)
	}
	if cfg.TypingFrequency > 0 {
		_ = drv.SetTypingFrequency(cfg.TypingFrequency)
	}
	cleanup := func() {
		drv.Close()
		release()
	}

	// The Flutter VM Service fallback, as on the WDA and legacy paths.
	var driver core.Driver = drv
	if !cfg.NoFlutterFallback {
		fw := flutter.WrapIOS(drv, nil, udid, cfg.AppID)
		driver = fw
		inner := cleanup
		cleanup = func() {
			if fd, ok := fw.(*flutter.FlutterDriver); ok {
				fd.Close()
			}
			inner()
		}
	}
	return driver, cleanup, nil
}
