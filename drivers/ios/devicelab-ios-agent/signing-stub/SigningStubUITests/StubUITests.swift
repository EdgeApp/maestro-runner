import XCTest

// The UI-test bundle of the signing stub. Building it makes Xcode produce a
// signed runner (…uitests.xctrunner) with the XCTest frameworks it embeds for
// a device; it is never run.
final class StubUITests: XCTestCase {
  func testNothing() {}
}
