import XCTest
#if canImport(UIKit)
import UIKit
#endif

// Screen settle: wait until the screen stops changing, the way Maestro's
// iOS driver decides a screen is static (two consecutive identical
// screenshots, 3s cap). The comparison runs on the device over small
// grayscale thumbnails, so one settle is one round trip and each sample
// costs a screen capture plus a ~5k-pixel compare — not two full PNGs
// shipped to the host.
extension RunnerTests {
  static let settleDefaultTimeoutMs: Double = 3000
  static let settleMaxTimeoutMs: Double = 10000
  // Thumbnail size. Large enough that a list moving by a row or a sheet
  // sliding in changes it; small enough that a blinking caret or a
  // sub-pixel shimmer rarely does.
  static let settleThumbWidth = 48
  static let settleThumbHeight = 104
  // A thumbnail pixel "differs" when it moves by more than this many gray
  // levels, and the screen counts as still when at most this share of
  // pixels differ.
  static let settlePixelTolerance = 6
  static let settleMaxDifferingShare = 0.002

  func executeSettle(command: Command) -> Response {
    let capMs = min(max(command.timeoutMs ?? Self.settleDefaultTimeoutMs, 0), Self.settleMaxTimeoutMs)
    let started = CACurrentMediaTime()
    var previous = settleThumbnail()
    var settled = false
    while (CACurrentMediaTime() - started) * 1000 < capMs {
      let current = settleThumbnail()
      if let prev = previous, let cur = current, thumbnailsMatch(prev, cur) {
        settled = true
        break
      }
      previous = current
    }
    let waitedMs = ((CACurrentMediaTime() - started) * 1000).rounded()
    return Response(
      ok: true,
      data: DataPayload(
        message: settled ? "settled" : "screen still changing at the cap",
        idle: settled,
        waitedMs: waitedMs
      )
    )
  }

  /// A small grayscale thumbnail of the whole screen, or nil when the
  /// capture could not be drawn.
  func settleThumbnail() -> [UInt8]? {
    guard let image = runnerCGImage(from: XCUIScreen.main.screenshot().image) else {
      return nil
    }
    let width = Self.settleThumbWidth
    let height = Self.settleThumbHeight
    var pixels = [UInt8](repeating: 0, count: width * height)
    let drawn = pixels.withUnsafeMutableBytes { buffer -> Bool in
      guard let context = CGContext(
        data: buffer.baseAddress,
        width: width,
        height: height,
        bitsPerComponent: 8,
        bytesPerRow: width,
        space: CGColorSpaceCreateDeviceGray(),
        bitmapInfo: CGImageAlphaInfo.none.rawValue
      ) else {
        return false
      }
      context.interpolationQuality = .low
      context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
      return true
    }
    return drawn ? pixels : nil
  }

  /// Whether two thumbnails show the same still screen.
  func thumbnailsMatch(_ a: [UInt8], _ b: [UInt8]) -> Bool {
    guard a.count == b.count, !a.isEmpty else { return false }
    let allowed = Int(Double(a.count) * Self.settleMaxDifferingShare)
    var differing = 0
    for i in 0..<a.count where abs(Int(a[i]) - Int(b[i])) > Self.settlePixelTolerance {
      differing += 1
      if differing > allowed { return false }
    }
    return true
  }
}
