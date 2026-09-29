package devicelab_ios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sampleProfile is `security cms -D` output for an Xcode-managed wildcard
// team profile, trimmed to the keys the signer reads.
const sampleProfile = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>AppIDName</key>
	<string>XC Wildcard</string>
	<key>DeveloperCertificates</key>
	<array>
		<data>YWJj</data>
	</array>
	<key>Entitlements</key>
	<dict>
		<key>application-identifier</key>
		<string>TEAM123456.*</string>
		<key>com.apple.developer.team-identifier</key>
		<string>TEAM123456</string>
		<key>get-task-allow</key>
		<true/>
	</dict>
	<key>ExpirationDate</key>
	<date>2027-11-10T12:45:12Z</date>
	<key>IsXcodeManaged</key>
	<true/>
	<key>Name</key>
	<string>iOS Team Provisioning Profile: *</string>
	<key>ProvisionedDevices</key>
	<array>
		<string>00008101-001C0C660A13001E</string>
		<string>00008030-0006216E22F3C02E</string>
	</array>
	<key>TeamIdentifier</key>
	<array>
		<string>TEAM123456</string>
	</array>
	<key>UUID</key>
	<string>c94fe992-dee0-42b3-b531-aa8741c3890f</string>
</dict>
</plist>`

func TestParseProfile(t *testing.T) {
	p, err := parseProfile([]byte(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "iOS Team Provisioning Profile: *" || p.UUID == "" {
		t.Fatalf("profile = %+v", p)
	}
	if p.appIdentifier() != "TEAM123456.*" {
		t.Fatalf("appIdentifier = %q", p.appIdentifier())
	}
	if len(p.TeamIdentifier) != 1 || p.TeamIdentifier[0] != "TEAM123456" {
		t.Fatalf("team = %v", p.TeamIdentifier)
	}
	if len(p.DeveloperCertificates) != 1 || string(p.DeveloperCertificates[0]) != "abc" {
		t.Fatalf("certificates = %v", p.DeveloperCertificates)
	}
	if want := time.Date(2027, 11, 10, 12, 45, 12, 0, time.UTC); !p.ExpirationDate.Equal(want) {
		t.Fatalf("expires = %v", p.ExpirationDate)
	}
	if _, err := parseProfile([]byte("not a plist")); err == nil {
		t.Fatal("garbage should not parse")
	}
	if _, err := parseProfile([]byte(`<plist version="1.0"><dict><key>Name</key><string>x</string></dict></plist>`)); err == nil {
		t.Fatal("a profile without a UUID should be rejected")
	}
}

func TestProfileCheck(t *testing.T) {
	p, err := parseProfile([]byte(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	const dev = "00008101-001C0C660A13001E"
	if err := p.check("TEAM123456", dev, "dev.devicelab.agent.uitests.xctrunner", now); err != nil {
		t.Fatalf("wildcard profile should cover the runner: %v", err)
	}
	if err := p.check("TEAM123456", strings.ToLower(dev), "dev.devicelab.agent", now); err != nil {
		t.Fatalf("device ids compare case-insensitively: %v", err)
	}
	if err := p.check("TEAM123456", "00008020-AAAA", "dev.devicelab.agent", now); err == nil || !strings.Contains(err.Error(), "does not include device") {
		t.Fatalf("unregistered device: %v", err)
	}
	if err := p.check("OTHERTEAM1", dev, "dev.devicelab.agent", now); err == nil {
		t.Fatal("another team's profile should not match")
	}
	if err := p.check("TEAM123456", dev, "dev.devicelab.agent", now.AddDate(2, 0, 0)); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired profile: %v", err)
	}
	p.ProvisionedDevices, p.ProvisionsAllDevices = nil, true
	if err := p.check("TEAM123456", "anything", "dev.devicelab.agent", now); err != nil {
		t.Fatalf("an all-devices profile covers any device: %v", err)
	}
}

func TestAppIDMatches(t *testing.T) {
	cases := []struct {
		appID, bundle string
		want          bool
	}{
		{"T1.*", "dev.devicelab.agent", true},
		{"T1.dev.devicelab.agent", "dev.devicelab.agent", true},
		{"T1.dev.devicelab.agent", "dev.devicelab.agent.uitests.xctrunner", false},
		{"T1.dev.devicelab.*", "dev.devicelab.agent", true},
		{"T1.com.other.*", "dev.devicelab.agent", false},
		{"T2.*", "dev.devicelab.agent", false},
		{"", "dev.devicelab.agent", false},
	}
	for _, c := range cases {
		if got := appIDMatches(c.appID, "T1", c.bundle); got != c.want {
			t.Errorf("appIDMatches(%q, %q) = %v, want %v", c.appID, c.bundle, got, c.want)
		}
	}
}

func TestParseCodesignInfo(t *testing.T) {
	out := `Executable=/tmp/dd/Build/Products/Debug-iphoneos/SigningStubUITests-Runner.app/SigningStubUITests-Runner
Identifier=dev.devicelab.agent.uitests.xctrunner
Format=app bundle with Mach-O thin (arm64)
CodeDirectory v=20500 size=1234 flags=0x0(none) hashes=27+7 location=embedded
Signature size=4786
Authority=Apple Development: Jane Doe (ABCDE12345)
Authority=Apple Worldwide Developer Relations Certification Authority
Authority=Apple Root CA
Signed Time=29 Sep 2026 at 17:00:00
Info.plist entries=24
TeamIdentifier=TEAM123456
Sealed Resources version=2 rules=10 files=3
Internal requirements count=1 size=208
`
	s := parseCodesignInfo(out)
	if s.Identifier != "dev.devicelab.agent.uitests.xctrunner" {
		t.Errorf("identifier = %q", s.Identifier)
	}
	if s.Authority != "Apple Development: Jane Doe (ABCDE12345)" {
		t.Errorf("authority = %q (want the leaf, the first Authority line)", s.Authority)
	}
	if s.TeamID != "TEAM123456" {
		t.Errorf("team = %q", s.TeamID)
	}
	unsigned := parseCodesignInfo("x: code object is not signed at all\nTeamIdentifier=not set\n")
	if unsigned.Authority != "" || unsigned.TeamID != "" {
		t.Errorf("unsigned = %+v", unsigned)
	}
}

func TestParseIdentities(t *testing.T) {
	out := `  1) 860903C07D6A612769381DAE78B0921B5CF9B54E "Apple Development: Jane Doe (ABCDE12345)"
  2) 01674f2664253ed22250ba47f59afea4f9ce609d "Apple Development: John Roe (FGHIJ67890)"
     2 valid identities found
`
	ids := parseIdentities(out)
	if len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
	if ids["860903C07D6A612769381DAE78B0921B5CF9B54E"] != "Apple Development: Jane Doe (ABCDE12345)" {
		t.Errorf("ids = %v", ids)
	}
	if _, ok := ids["01674F2664253ED22250BA47F59AFEA4F9CE609D"]; !ok {
		t.Errorf("hashes should be upper-cased: %v", ids)
	}
}

func TestCertSHA1(t *testing.T) {
	if got := certSHA1([]byte("abc")); got != "A9993E364706816ABA3E25717850C26C9CD0D89D" {
		t.Fatalf("certSHA1 = %s", got)
	}
}

func TestParseEntitlements(t *testing.T) {
	raw := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>application-identifier</key><string>TEAM123456.dev.devicelab.agent.uitests.xctrunner</string>
	<key>com.apple.developer.team-identifier</key><string>TEAM123456</string>
	<key>get-task-allow</key><true/>
</dict></plist>`)
	ent, err := parseEntitlements(raw, "TEAM123456", "dev.devicelab.agent.uitests.xctrunner")
	if err != nil {
		t.Fatal(err)
	}
	if ent["get-task-allow"] != true {
		t.Errorf("entitlements = %v", ent)
	}
	if _, err := parseEntitlements(raw, "TEAM123456", "dev.devicelab.agent"); err == nil {
		t.Error("entitlements for another bundle id should be rejected")
	}
	if _, err := parseEntitlements([]byte("junk"), "T", "x"); err == nil {
		t.Error("garbage should not parse")
	}
}

func TestAgentBundleIDs(t *testing.T) {
	def := agentBundleIDs("")
	if def.host != hostBundleID || def.runner != runnerBundleID || def.test != "dev.devicelab.agent.uitests" || def.custom() {
		t.Fatalf("default ids = %+v", def)
	}
	c := agentBundleIDs(" com.jane.agent ")
	if c.host != "com.jane.agent" || c.test != "com.jane.agent.uitests" || c.runner != "com.jane.agent.uitests.xctrunner" || !c.custom() {
		t.Fatalf("custom ids = %+v", c)
	}
}

func TestSigningCacheKey(t *testing.T) {
	ids := agentBundleIDs("")
	k := signingCacheKey("eafa2dc", "team123456", "00008101-001C0C660A13001E", ids)
	if k != "eafa2dc-TEAM123456-00008101-001C0C660A13001E" {
		t.Fatalf("key = %q", k)
	}
	if k == signingCacheKey("eafa2dd", "TEAM123456", "00008101-001C0C660A13001E", ids) {
		t.Error("a new agent version must re-sign")
	}
	if k == signingCacheKey("eafa2dc", "OTHER12345", "00008101-001C0C660A13001E", ids) {
		t.Error("another team must re-sign")
	}
	if k == signingCacheKey("eafa2dc", "TEAM123456", "00008030-0006216E22F3C02E", ids) {
		t.Error("another device must re-sign")
	}
	custom := signingCacheKey("eafa2dc", "TEAM123456", "00008101-001C0C660A13001E", agentBundleIDs("com.jane.agent"))
	if custom == k || !strings.HasPrefix(custom, k+"-") {
		t.Errorf("custom bundle id key = %q", custom)
	}
	if got := signingCacheKey("v1/../x y", "T", "U", ids); strings.ContainsAny(got, "/ ") {
		t.Errorf("key must be one safe path element: %q", got)
	}
}

func TestRequireTeamID(t *testing.T) {
	if err := RequireTeamID("TEAM123456"); err != nil {
		t.Fatal(err)
	}
	err := RequireTeamID("  ")
	if err == nil || !strings.Contains(err.Error(), "--team-id") || !strings.Contains(err.Error(), "not required for simulators") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartDeviceAgentNeedsTeamID(t *testing.T) {
	_, _, err := StartDeviceAgent(context.Background(), AgentOptions{UDID: "00008101-001C0C660A13001E", Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--team-id") {
		t.Fatalf("err = %v", err)
	}
}

func TestSignOrder(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Runner.app")
	for _, d := range []string{
		"Frameworks/XCTest.framework",
		"Frameworks/XCUIAutomation.framework",
		"PlugIns/Agent.xctest/Frameworks/Inner.framework",
		"PlugIns/Agent.xctest.dSYM/Contents",
		"_CodeSignature",
	} {
		if err := os.MkdirAll(filepath.Join(app, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"Frameworks/libXCTestSwiftSupport.dylib", "_CodeSignature/x.dylib", "Runner"} {
		if err := os.WriteFile(filepath.Join(app, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := signOrder(app)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		got[i] = strings.TrimPrefix(got[i], app+"/")
	}
	want := []string{
		"PlugIns/Agent.xctest/Frameworks/Inner.framework",
		"Frameworks/XCTest.framework",
		"Frameworks/XCUIAutomation.framework",
		"Frameworks/libXCTestSwiftSupport.dylib",
		"PlugIns/Agent.xctest",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("order:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoadSigned(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	write := func(s signedAgent) {
		raw, _ := json.Marshal(s)
		if err := os.WriteFile(filepath.Join(dir, signedMarker), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := loadSigned(dir, "v1", now); ok {
		t.Fatal("no marker: not signed")
	}
	write(signedAgent{Version: "v1", ProfileExpires: now.AddDate(0, 6, 0)})
	if _, ok := loadSigned(dir, "v1", now); !ok {
		t.Fatal("a valid signing should be reused")
	}
	if _, ok := loadSigned(dir, "v2", now); ok {
		t.Fatal("another agent version must re-sign")
	}
	write(signedAgent{Version: "v1", ProfileExpires: now.Add(2 * time.Hour)})
	if _, ok := loadSigned(dir, "v1", now); ok {
		t.Fatal("a profile about to expire must re-sign")
	}
}

func TestDiagnoseStubBuild(t *testing.T) {
	cases := map[string]string{
		`error: No Account for Team "TEAM123456". Add a new account in Accounts settings`:                                                     "Xcode > Settings > Accounts",
		`error: Failed Registering Bundle Identifier: The app identifier "dev.devicelab.agent" cannot be registered to your development team`: BundleIDEnv,
		`xcodebuild: error: Unable to find a destination matching the provided destination specifier`:                                         "connect and unlock",
		`error: Your team has no devices from which to generate a provisioning profile`:                                                       "not registered",
		`error: No signing certificate "iOS Development" found`:                                                                               "Manage Certificates",
		`error: No profiles for 'dev.devicelab.agent' were found`:                                                                             "provisioning profile",
	}
	for log, want := range cases {
		got := diagnoseStubBuild(log, "TEAM123456", "00008101-001C0C660A13001E")
		if !strings.Contains(got, want) {
			t.Errorf("diagnoseStubBuild(%q) = %q, want it to mention %q", log, got, want)
		}
	}
	if got := diagnoseStubBuild("** TEST BUILD FAILED **", "T", "U"); got != "" {
		t.Errorf("an unknown failure has no hint, got %q", got)
	}
}

func TestBundledDeviceAgentShape(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "drivers", "ios", "devicelab-ios-agent", "device")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no bundled device agent in this checkout")
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version == "" {
		t.Fatal("manifest has no version")
	}
	for _, p := range []string{hostAppName, runnerAppName, filepath.Join(runnerAppName, "PlugIns", testTargetName+".xctest"), "agent.xctestrun"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("device agent is missing %s", p)
		}
	}
	stub := filepath.Join("..", "..", "..", "drivers", "ios", "devicelab-ios-agent", "signing-stub", stubProject)
	if _, err := os.Stat(stub); err != nil {
		t.Errorf("signing stub project missing: %v", err)
	}
}
