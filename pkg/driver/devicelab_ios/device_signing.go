package devicelab_ios

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/devicelab-dev/maestro-runner/pkg/config"
	"github.com/devicelab-dev/maestro-runner/pkg/logger"
	"howett.net/plist"
)

// A real iPhone runs the same agent as a simulator, built unsigned for arm64
// (drivers/ios/devicelab-ios-agent/device/). Only the user's own development
// certificate and provisioning profiles can make it run on their device, and
// those live in their Xcode account, so the agent is re-signed on their Mac:
//
//  1. build the signing stub (drivers/ios/devicelab-ios-agent/signing-stub/,
//     no agent code, the agent's bundle ids) with -allowProvisioningUpdates,
//     so Xcode creates or downloads the certificate and the profiles that
//     cover the device;
//  2. take from the stub's products the identity it was signed with, the
//     runner's and the host app's embedded.mobileprovision and entitlements,
//     and the XCTest frameworks Xcode embedded in the runner;
//  3. copy the prebuilt agent, put those in, and sign it inside-out.
//
// The result is cached per agent version, team and device, and redone when
// its profile is about to expire.

// BundleIDEnv replaces the agent's host bundle id on a real device (the
// runner's and the test bundle's follow it). A personal team cannot use the
// team's wildcard App ID, so Xcode registers the ids explicitly, and an id
// another team registered first is not available.
const BundleIDEnv = "DEVICELAB_IOS_AGENT_BUNDLE_ID"

// DeviceDirEnv points at a device agent build other than the bundled one.
const DeviceDirEnv = "DEVICELAB_IOS_AGENT_DEVICE_DIR"

const (
	stubProject      = "SigningStub.xcodeproj"
	stubScheme       = "SigningStubUITests"
	stubHostApp      = "SigningStub.app"
	stubRunnerApp    = "SigningStubUITests-Runner.app"
	stubBuildTimeout = 10 * time.Minute
	signTimeout      = 2 * time.Minute
	// profileMargin re-signs a cached agent whose profile expires within it,
	// so a run never starts on one that lapses halfway.
	profileMargin = 24 * time.Hour
	signedMarker  = "signed.json"
)

// BundledDeviceAgentDir is where maestro-runner ships the unsigned device
// agent.
func BundledDeviceAgentDir() string {
	return filepath.Join(config.GetDriversDir("ios"), "devicelab-ios-agent", "device")
}

// bundledSigningStubDir is where maestro-runner ships the signing stub.
func bundledSigningStubDir() string {
	return filepath.Join(config.GetDriversDir("ios"), "devicelab-ios-agent", "signing-stub")
}

// RequireTeamID is the error for a real device without --team-id (nil when
// one is set).
func RequireTeamID(team string) error {
	if strings.TrimSpace(team) != "" {
		return nil
	}
	return fmt.Errorf("iOS on a real device requires --team-id to code-sign the devicelab agent\n" +
		"Usage: maestro-runner --platform ios --driver devicelab --team-id <APPLE_TEAM_ID> test <flow-files>\n" +
		"Note: --team-id is not required for simulators.\n" +
		"      `maestro-runner --team-id <ID> doctor` checks it against the accounts Xcode has")
}

// bundleIDs are the agent's three bundle identifiers.
type bundleIDs struct {
	host, test, runner string
}

// agentBundleIDs derives the test bundle's and the runner's ids from the
// host app's, as Xcode does; empty means the agent's own.
func agentBundleIDs(host string) bundleIDs {
	host = strings.TrimSpace(host)
	if host == "" {
		host = hostBundleID
	}
	test := host + ".uitests"
	return bundleIDs{host: host, test: test, runner: test + ".xctrunner"}
}

// custom reports whether the ids differ from the prebuilt agent's.
func (b bundleIDs) custom() bool { return b.host != hostBundleID }

var unsafePathChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// signingCacheKey names the signed agent's cache directory: agent version,
// team and device, plus a hash of the bundle id when it is not the agent's.
func signingCacheKey(version, team, udid string, ids bundleIDs) string {
	key := strings.Join([]string{
		unsafePathChars.ReplaceAllString(version, "_"),
		unsafePathChars.ReplaceAllString(strings.ToUpper(team), "_"),
		unsafePathChars.ReplaceAllString(udid, "_"),
	}, "-")
	if ids.custom() {
		sum := sha256.Sum256([]byte(ids.host))
		key += "-" + hex.EncodeToString(sum[:4])
	}
	return key
}

// signedAgentDir is where the signed agent for one key lives.
func signedAgentDir(key string) string {
	return filepath.Join(config.GetCacheDir(), "devicelab-ios-agent", "device-signed", key)
}

// ---------- parsing ----------

// provisioningProfile is the part of a decoded embedded.mobileprovision the
// signer checks.
type provisioningProfile struct {
	Name                  string                 `plist:"Name"`
	UUID                  string                 `plist:"UUID"`
	TeamIdentifier        []string               `plist:"TeamIdentifier"`
	ExpirationDate        time.Time              `plist:"ExpirationDate"`
	ProvisionedDevices    []string               `plist:"ProvisionedDevices"`
	ProvisionsAllDevices  bool                   `plist:"ProvisionsAllDevices"`
	Entitlements          map[string]interface{} `plist:"Entitlements"`
	DeveloperCertificates [][]byte               `plist:"DeveloperCertificates"`
}

// parseProfile reads the plist `security cms -D` prints for a profile.
func parseProfile(raw []byte) (provisioningProfile, error) {
	var p provisioningProfile
	if _, err := plist.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("bad provisioning profile: %w", err)
	}
	if p.UUID == "" {
		return p, fmt.Errorf("bad provisioning profile: no UUID")
	}
	return p, nil
}

// appIdentifier is the profile's application-identifier (TEAM.id or TEAM.*).
func (p provisioningProfile) appIdentifier() string {
	s, _ := p.Entitlements["application-identifier"].(string)
	return s
}

// check reports why the profile cannot run bundleID of team on udid at now.
func (p provisioningProfile) check(team, udid, bundleID string, now time.Time) error {
	if !p.ExpirationDate.IsZero() && !now.Before(p.ExpirationDate) {
		return fmt.Errorf("provisioning profile %q expired on %s", p.Name, p.ExpirationDate.Format("2006-01-02"))
	}
	if !p.ProvisionsAllDevices && !containsFold(p.ProvisionedDevices, udid) {
		return fmt.Errorf("provisioning profile %q does not include device %s: register the device in team %s "+
			"(Xcode > Window > Devices and Simulators, or developer.apple.com > Devices)", p.Name, udid, team)
	}
	appID := p.appIdentifier()
	if !appIDMatches(appID, team, bundleID) {
		return fmt.Errorf("provisioning profile %q is for %q, not %s.%s", p.Name, appID, team, bundleID)
	}
	return nil
}

// appIDMatches reports whether an application-identifier (TEAM.id, TEAM.*,
// TEAM.prefix.*) covers bundleID of team.
func appIDMatches(appID, team, bundleID string) bool {
	prefix := team + "."
	if !strings.HasPrefix(appID, prefix) {
		return false
	}
	pattern := strings.TrimPrefix(appID, prefix)
	if pattern == "*" || pattern == bundleID {
		return true
	}
	return strings.HasSuffix(pattern, ".*") && strings.HasPrefix(bundleID, strings.TrimSuffix(pattern, "*"))
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// codeSignature is what `codesign -dvv` prints about a signed bundle.
type codeSignature struct {
	Identifier string // the bundle's signing identifier
	Authority  string // the leaf certificate, "Apple Development: Name (ID)"
	TeamID     string
}

// parseCodesignInfo reads `codesign -dvv` output (printed on stderr).
func parseCodesignInfo(out string) codeSignature {
	var s codeSignature
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Identifier=") && s.Identifier == "":
			s.Identifier = strings.TrimPrefix(line, "Identifier=")
		case strings.HasPrefix(line, "Authority=") && s.Authority == "":
			s.Authority = strings.TrimPrefix(line, "Authority=")
		case strings.HasPrefix(line, "TeamIdentifier=") && s.TeamID == "":
			s.TeamID = strings.TrimPrefix(line, "TeamIdentifier=")
		}
	}
	if s.TeamID == "not set" {
		s.TeamID = ""
	}
	return s
}

var identityLine = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-Fa-f]{40})\s+"(.*)"`)

// parseIdentities reads `security find-identity -v -p codesigning` into
// SHA-1 (upper case) → certificate name.
func parseIdentities(out string) map[string]string {
	ids := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if m := identityLine.FindStringSubmatch(line); m != nil {
			ids[strings.ToUpper(m[1])] = m[2]
		}
	}
	return ids
}

// certSHA1 is a DER certificate's SHA-1, the form codesign --sign accepts.
func certSHA1(der []byte) string {
	sum := sha1.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// parseEntitlements reads an entitlements plist and checks it is for
// bundleID of team.
func parseEntitlements(raw []byte, team, bundleID string) (map[string]interface{}, error) {
	var ent map[string]interface{}
	if _, err := plist.Unmarshal(raw, &ent); err != nil {
		return nil, fmt.Errorf("bad entitlements: %w", err)
	}
	appID, _ := ent["application-identifier"].(string)
	if appID != team+"."+bundleID {
		return nil, fmt.Errorf("entitlements are for %q, not %s.%s", appID, team, bundleID)
	}
	return ent, nil
}

// signOrder lists the code nested in a bundle, deepest first, so each
// signature seals the ones inside it: frameworks and dylibs, then the
// plug-ins that hold them. The bundle itself is not listed.
func signOrder(bundle string) ([]string, error) {
	var nested []string
	err := filepath.WalkDir(bundle, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == bundle {
			return nil
		}
		name := e.Name()
		if e.IsDir() && (name == "_CodeSignature" || strings.HasSuffix(name, ".dSYM")) {
			return filepath.SkipDir
		}
		switch filepath.Ext(name) {
		case ".framework", ".xctest", ".appex", ".app":
			if e.IsDir() {
				nested = append(nested, path)
			}
		case ".dylib":
			if !e.IsDir() {
				nested = append(nested, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	depth := func(p string) int { return strings.Count(p, string(filepath.Separator)) }
	sort.SliceStable(nested, func(i, j int) bool {
		if di, dj := depth(nested[i]), depth(nested[j]); di != dj {
			return di > dj
		}
		return nested[i] < nested[j]
	})
	return nested, nil
}

// diagnoseStubBuild turns a failed stub build's log into what to do about it.
func diagnoseStubBuild(log, team, udid string) string {
	l := strings.ToLower(log)
	switch {
	case strings.Contains(l, "no account for team") || strings.Contains(l, "no accounts"):
		return fmt.Sprintf("Xcode has no signed-in Apple account for team %s: add it in Xcode > Settings > Accounts", team)
	case strings.Contains(l, "cannot be registered to your development team") || strings.Contains(l, "is not available") && strings.Contains(l, "identifier"):
		return fmt.Sprintf("the agent's bundle id is taken for team %s (a personal team cannot use a wildcard App ID): "+
			"set %s to an id of your own, e.g. %s=com.yourname.devicelab.agent", team, BundleIDEnv, BundleIDEnv)
	case strings.Contains(l, "unable to find a destination matching") || strings.Contains(l, "is not available because") && strings.Contains(l, "device"):
		return fmt.Sprintf("Xcode cannot use device %s: connect and unlock it, trust this Mac, enable Developer Mode "+
			"(Settings > Privacy & Security), and wait until Xcode > Window > Devices and Simulators shows it ready", udid)
	case strings.Contains(l, "has not been registered") || strings.Contains(l, "no devices") || strings.Contains(l, "device registration"):
		return fmt.Sprintf("device %s is not registered in team %s: open Xcode > Window > Devices and Simulators "+
			"with the device connected, or add it at developer.apple.com > Devices", udid, team)
	case strings.Contains(l, "no signing certificate") || strings.Contains(l, "no certificate for team") || strings.Contains(l, "doesn't include signing certificate"):
		return fmt.Sprintf("no Apple Development certificate for team %s on this Mac: Xcode > Settings > Accounts > Manage Certificates > +", team)
	case strings.Contains(l, "no profiles for") || strings.Contains(l, "requires a provisioning profile"):
		return fmt.Sprintf("Xcode could not create a provisioning profile for team %s: check the team in Xcode > Settings > Accounts "+
			"and that the device is registered", team)
	}
	return ""
}

// ---------- signing ----------

// signedAgent is the marker a completed signing writes.
type signedAgent struct {
	Version        string    `json:"version"`
	Team           string    `json:"team"`
	UDID           string    `json:"udid"`
	HostBundleID   string    `json:"hostBundleId"`
	Identity       string    `json:"identity"`
	IdentityName   string    `json:"identityName"`
	ProfileExpires time.Time `json:"profileExpires"`
}

// loadSigned returns a cached signing that is still usable at now.
func loadSigned(dir, version string, now time.Time) (signedAgent, bool) {
	var s signedAgent
	raw, err := os.ReadFile(filepath.Join(dir, signedMarker))
	if err != nil || json.Unmarshal(raw, &s) != nil {
		return s, false
	}
	if s.Version != version || !now.Add(profileMargin).Before(s.ProfileExpires) {
		return s, false
	}
	return s, true
}

// deviceSigner signs the prebuilt device agent for one team and device.
type deviceSigner struct {
	udid, team, version string
	ids                 bundleIDs
	agentDir            string // the unsigned prebuilt agent
	stubDir             string // the signing stub project
	logf                func(format string, args ...any)
}

// signedDir returns a directory holding the agent signed for the device,
// signing it first unless a usable signing is cached.
func (s *deviceSigner) signedDir(ctx context.Context) (string, error) {
	dir := signedAgentDir(signingCacheKey(s.version, s.team, s.udid, s.ids))
	if cached, ok := loadSigned(dir, s.version, time.Now()); ok {
		logger.Info("[devicelab-ios] using the agent signed by %s (%s)", cached.IdentityName, dir)
		return dir, nil
	}
	s.logf("Signing the devicelab agent for team %s (first run on this device; about a minute)...", s.team)
	products, err := s.buildStub(ctx)
	if err != nil {
		return "", err
	}
	marker, err := s.sign(ctx, products, dir)
	if err != nil {
		return "", err
	}
	logger.Info("[devicelab-ios] signed the agent with %s, profile valid until %s", marker.IdentityName, marker.ProfileExpires.Format("2006-01-02"))
	return dir, nil
}

// buildStub builds the signing stub for the device and returns its build
// products directory.
func (s *deviceSigner) buildStub(ctx context.Context) (string, error) {
	root := filepath.Join(config.GetCacheDir(), "devicelab-ios-agent", "signing-stub", unsafePathChars.ReplaceAllString(s.team, "_"))
	src := filepath.Join(root, "src")
	dd := filepath.Join(root, "DerivedData")
	// A copy, so xcodebuild never writes into the installed drivers.
	_ = os.RemoveAll(src)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if out, err := exec.Command("ditto", s.stubDir, src).CombinedOutput(); err != nil {
		return "", fmt.Errorf("copy signing stub from %s: %v: %s", s.stubDir, err, strings.TrimSpace(string(out)))
	}
	products := filepath.Join(dd, "Build", "Products", "Debug-iphoneos")
	_ = os.RemoveAll(products)
	logPath := filepath.Join(root, "build.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = logFile.Close() }()
	buildCtx, cancel := context.WithTimeout(ctx, stubBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(buildCtx, "xcodebuild", "build-for-testing",
		"-project", filepath.Join(src, stubProject),
		"-scheme", stubScheme,
		"-destination", "platform=iOS,id="+s.udid,
		"-derivedDataPath", dd,
		"-allowProvisioningUpdates",
		"-allowProvisioningDeviceRegistration",
		"DEVELOPMENT_TEAM="+s.team,
		"CODE_SIGN_STYLE=Automatic",
		"DL_AGENT_BUNDLE_ID="+s.ids.host,
		"COMPILER_INDEX_STORE_ENABLE=NO")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Run(); err != nil {
		raw, _ := os.ReadFile(logPath)
		hint := diagnoseStubBuild(string(raw), s.team, s.udid)
		if hint == "" {
			hint = "Xcode could not sign for this device"
		}
		return "", fmt.Errorf("%s\n\n%s\n\nFull log: %s", hint, lastLines(string(raw), 15), logPath)
	}
	for _, app := range []string{stubHostApp, stubRunnerApp} {
		if !pathExists(filepath.Join(products, app, "embedded.mobileprovision")) {
			return "", fmt.Errorf("the signing stub built but %s has no provisioning profile (full log: %s)", app, logPath)
		}
	}
	return products, nil
}

// sign assembles the signed agent into dir from the prebuilt agent and the
// stub's build products.
func (s *deviceSigner) sign(ctx context.Context, products, dir string) (signedAgent, error) {
	var marker signedAgent
	stubRunner := filepath.Join(products, stubRunnerApp)
	stubHost := filepath.Join(products, stubHostApp)

	identity, name, err := signingIdentity(ctx, stubRunner, s.team)
	if err != nil {
		return marker, err
	}
	runnerProfile, err := readProfile(ctx, filepath.Join(stubRunner, "embedded.mobileprovision"))
	if err != nil {
		return marker, err
	}
	now := time.Now()
	if err := runnerProfile.check(s.team, s.udid, s.ids.runner, now); err != nil {
		return marker, err
	}
	hostProfile, err := readProfile(ctx, filepath.Join(stubHost, "embedded.mobileprovision"))
	if err != nil {
		return marker, err
	}
	if err := hostProfile.check(s.team, s.udid, s.ids.host, now); err != nil {
		return marker, err
	}

	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return marker, err
	}
	if out, err := exec.Command("ditto", s.agentDir, tmp).CombinedOutput(); err != nil {
		return marker, fmt.Errorf("copy device agent from %s: %v: %s", s.agentDir, err, strings.TrimSpace(string(out)))
	}
	runner := filepath.Join(tmp, runnerAppName)
	host := filepath.Join(tmp, hostAppName)
	if s.ids.custom() {
		for path, id := range map[string]string{
			filepath.Join(host, "Info.plist"):                                        s.ids.host,
			filepath.Join(runner, "Info.plist"):                                      s.ids.runner,
			filepath.Join(runner, "PlugIns", testTargetName+".xctest", "Info.plist"): s.ids.test,
		} {
			if err := run(ctx, signTimeout, "plutil", "-replace", "CFBundleIdentifier", "-string", id, path); err != nil {
				return marker, err
			}
		}
	}
	if err := copyMissingFrameworks(filepath.Join(stubRunner, "Frameworks"), filepath.Join(runner, "Frameworks")); err != nil {
		return marker, err
	}
	for app, stub := range map[string]string{runner: stubRunner, host: stubHost} {
		if out, err := exec.Command("cp", filepath.Join(stub, "embedded.mobileprovision"), filepath.Join(app, "embedded.mobileprovision")).CombinedOutput(); err != nil {
			return marker, fmt.Errorf("embed profile in %s: %v: %s", filepath.Base(app), err, strings.TrimSpace(string(out)))
		}
	}
	for _, target := range []struct{ app, stub, id string }{
		{runner, stubRunner, s.ids.runner},
		{host, stubHost, s.ids.host},
	} {
		ent, err := stubEntitlements(ctx, target.stub, s.team, target.id)
		if err != nil {
			return marker, err
		}
		entPath := filepath.Join(filepath.Dir(tmp), filepath.Base(target.app)+".entitlements")
		if err := os.WriteFile(entPath, ent, 0o644); err != nil {
			return marker, err
		}
		if err := signBundle(ctx, target.app, identity, entPath); err != nil {
			return marker, err
		}
		_ = os.Remove(entPath)
	}

	marker = signedAgent{
		Version: s.version, Team: s.team, UDID: s.udid, HostBundleID: s.ids.host,
		Identity: identity, IdentityName: name, ProfileExpires: earliest(runnerProfile.ExpirationDate, hostProfile.ExpirationDate),
	}
	raw, _ := json.MarshalIndent(marker, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, signedMarker), raw, 0o644); err != nil {
		return marker, err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return marker, err
	}
	return marker, nil
}

// signingIdentity returns the SHA-1 and name of the certificate Xcode signed
// the stub with. The hash, not the name, is what gets passed to codesign: a
// keychain often holds an expired certificate of the same name.
func signingIdentity(ctx context.Context, signedApp, team string) (string, string, error) {
	out, _ := output(ctx, signTimeout, "codesign", "-dvv", signedApp)
	sig := parseCodesignInfo(out)
	if sig.Authority == "" {
		return "", "", fmt.Errorf("the signing stub is not signed (codesign: %s)", lastLines(out, 3))
	}
	if sig.TeamID != "" && !strings.EqualFold(sig.TeamID, team) {
		return "", "", fmt.Errorf("the signing stub was signed for team %s, not %s: check --team-id", sig.TeamID, team)
	}
	tmp, err := os.MkdirTemp("", "dl-cert-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	prefix := filepath.Join(tmp, "cert")
	if _, err := output(ctx, signTimeout, "codesign", "-d", "--extract-certificates="+prefix, signedApp); err != nil {
		return "", "", fmt.Errorf("read the stub's signing certificate: %w", err)
	}
	der, err := os.ReadFile(prefix + "0")
	if err != nil {
		return "", "", fmt.Errorf("read the stub's signing certificate: %w", err)
	}
	hash := certSHA1(der)
	ids, _ := output(ctx, signTimeout, "security", "find-identity", "-v", "-p", "codesigning")
	if _, ok := parseIdentities(ids)[hash]; !ok {
		return "", "", fmt.Errorf("%q (%s) signed the stub but is not a valid signing identity in your keychain", sig.Authority, hash)
	}
	return hash, sig.Authority, nil
}

// readProfile decodes an embedded.mobileprovision.
func readProfile(ctx context.Context, path string) (provisioningProfile, error) {
	out, err := exec.CommandContext(ctx, "security", "cms", "-D", "-i", path).Output()
	if err != nil {
		return provisioningProfile{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return parseProfile(out)
}

// stubEntitlements returns the entitlements Xcode signed a stub bundle with,
// checked to be for id of team.
func stubEntitlements(ctx context.Context, app, team, id string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "codesign", "-d", "--entitlements", "-", "--xml", app).Output()
	if err != nil || len(out) == 0 {
		// codesign before macOS 12 has no --xml; ":-" is its XML form.
		out, err = exec.CommandContext(ctx, "codesign", "-d", "--entitlements", ":-", app).Output()
	}
	if err != nil {
		return nil, fmt.Errorf("read entitlements of %s: %w", filepath.Base(app), err)
	}
	if _, err := parseEntitlements(out, team, id); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(app), err)
	}
	return out, nil
}

// copyMissingFrameworks copies each entry of src (the XCTest frameworks
// Xcode embedded in the stub runner) that dst does not have.
func copyMissingFrameworks(src, dst string) error {
	entries, err := os.ReadDir(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		to := filepath.Join(dst, e.Name())
		if pathExists(to) {
			continue
		}
		if out, err := exec.Command("ditto", filepath.Join(src, e.Name()), to).CombinedOutput(); err != nil {
			return fmt.Errorf("copy %s: %v: %s", e.Name(), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// signBundle signs the code nested in bundle, then bundle itself with
// entitlements, and verifies the result.
func signBundle(ctx context.Context, bundle, identity, entitlements string) error {
	nested, err := signOrder(bundle)
	if err != nil {
		return err
	}
	for _, p := range nested {
		if err := run(ctx, signTimeout, "codesign", "--force", "--sign", identity, "--timestamp=none", p); err != nil {
			return err
		}
	}
	if err := run(ctx, signTimeout, "codesign", "--force", "--sign", identity, "--entitlements", entitlements, "--timestamp=none", bundle); err != nil {
		return err
	}
	return run(ctx, signTimeout, "codesign", "--verify", "--deep", "--strict", bundle)
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

// run runs a command bounded by timeout; the error carries its output.
func run(ctx context.Context, timeout time.Duration, name string, args ...string) error {
	out, err := output(ctx, timeout, name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, lastLines(out, 5))
	}
	return nil
}

// output runs a command bounded by timeout and returns stdout and stderr.
func output(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := exec.CommandContext(c, name, args...).CombinedOutput()
	return string(out), err
}

// lastLines is the trimmed tail of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
