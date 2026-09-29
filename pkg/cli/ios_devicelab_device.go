package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/core"
	dlios "github.com/devicelab-dev/maestro-runner/pkg/driver/devicelab_ios"
)

// createDevicelabIOSDeviceDriver constructs --driver devicelab on a physical
// iPhone: the prebuilt device agent, re-signed once for --team-id and this
// device (Xcode provisions it through a signing stub), started with
// xcodebuild and reached through a usbmux port forward. The agent runs under
// this process's xcodebuild, so it stops when the run ends.
func createDevicelabIOSDeviceDriver(cfg *RunConfig, udid string) (core.Driver, func(), error) {
	// Before the app install, so a missing team fails fast.
	if err := dlios.RequireTeamID(cfg.TeamID); err != nil {
		return nil, nil, err
	}

	if cfg.AppFile != "" && !cfg.NoAppInstall {
		printSetupStep(fmt.Sprintf("Installing app: %s", cfg.AppFile))
		if err := installIOSApp(udid, cfg.AppFile, false); err != nil {
			return nil, nil, fmt.Errorf("install app failed: %w", err)
		}
		printSetupSuccess("App installed")
	}

	ctx := context.Background()
	printSetupStep("Starting devicelab iOS agent on the device (the first run signs it for your team)...")
	agent, client, err := dlios.StartDeviceAgent(ctx, dlios.AgentOptions{UDID: udid, TeamID: cfg.TeamID, ReadyTimeout: 600 * time.Second})
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

	screenW, screenH := 0, 0
	if resp, err := client.Call(ctx, "snapshot", &dlios.Args{MaxNodes: 1}); err == nil && resp.Data != nil {
		screenW, screenH = int(resp.Data.ScreenW), int(resp.Data.ScreenH)
	}

	// An installed app cannot be read back from a device; the version comes
	// from the .app under test, as on the WDA path.
	appVersion, appBuild := "", ""
	if cfg.AppFile != "" {
		appVersion, appBuild = readBundleVersionAndBuild(cfg.AppFile)
	}

	info := &core.PlatformInfo{
		Platform:     "ios",
		OSVersion:    deviceInfo.OSVersion,
		DeviceName:   deviceInfo.Name,
		DeviceID:     udid,
		IsSimulator:  false,
		ScreenWidth:  screenW,
		ScreenHeight: screenH,
		AppID:        cfg.AppID,
		AppVersion:   appVersion,
		AppBuild:     appBuild,
	}

	drv := dlios.NewDriver(client, info, udid)
	drv.SetRealDevice(cfg.AppFile)
	if cfg.AppID != "" {
		drv.SetAppID(cfg.AppID)
	}
	if cfg.TypingFrequency > 0 {
		_ = drv.SetTypingFrequency(cfg.TypingFrequency)
	}
	// No Flutter VM Service fallback: as on the WDA path, it is
	// simulator-only.
	cleanup := func() {
		drv.Close()
		release()
	}
	return drv, cleanup, nil
}
