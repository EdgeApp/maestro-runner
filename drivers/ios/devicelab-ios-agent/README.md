# devicelab-ios-agent (prebuilt)

The XCUITest agent behind `--driver devicelab` on iOS simulators, built from
the private `devicelab-ios-agent` repository with `scripts/build.sh`
(universal arm64 + x86_64, Swift Testing interop libraries bundled).

`simulator/manifest.json` records the agent commit (`version`), the wire
protocol and a sha256 per file. The host re-installs the agent on a
simulator when `version` changes.

To update: build in the agent repository, then replace `simulator/` with its
`dist/simulator/`.

Overrides:

- `DEVICELAB_IOS_AGENT_DIR` — use an agent build from another directory
- `DEVICELAB_IOS_AGENT_LAUNCH` — `auto` (default), `simctl` or `xcodebuild`
- `DEVICELAB_IOS_AGENT_KEEP=0` — stop the agent when the run ends
  (by default an agent started with simctl stays up for the next run)
