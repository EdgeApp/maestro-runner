import SwiftUI

// The host app of the signing stub. It is built only for its signature and
// provisioning profile, never run.
@main
struct StubApp: App {
  var body: some Scene {
    WindowGroup { Text("devicelab signing stub") }
  }
}
