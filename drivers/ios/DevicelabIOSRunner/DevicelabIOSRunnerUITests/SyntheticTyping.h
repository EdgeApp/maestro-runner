#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>

NS_ASSUME_NONNULL_BEGIN

// Local extension (not in upstream agent-device). Synthesizes typing events
// via the private XCSynthesizedEventRecord + XCPointerEventPath +
// XCUIDevice.eventSynthesizer pathway — the same mechanism WebDriverAgent
// uses for FBTypeText. Unlike XCUIElement.typeText / XCUIApplication.typeText,
// this does NOT require XCUIElement.hasKeyboardFocus to be true. That is
// what makes it work for RN SecureTextField on iOS Simulator, where the
// keyboard appears visually but hasKeyboardFocus stays false and the public
// typeText APIs silently drop characters.
//
// Returns YES on success. Writes a NSError into *outError on failure.
BOOL DLSendSyntheticTyping(NSString *text, NSUInteger typingSpeed, NSError * _Nullable * _Nullable outError);

// One finger, in screen points: touch down at `from`, rest there for `hold`,
// move to `to` over `move`, rest there for `rest`, lift. This is Maestro's
// swipe shape (hold 0, move 0.1s, rest = the swipe's duration): no stationary
// press at the start, which a list reads as a tap on the row underneath, and
// no element lookups or idle waits around the gesture, which XCUICoordinate
// adds. `interfaceOrientation` is a UIInterfaceOrientation raw value.
BOOL DLSendSyntheticTouch(CGPoint from,
                          CGPoint to,
                          NSTimeInterval hold,
                          NSTimeInterval move,
                          NSTimeInterval rest,
                          long long interfaceOrientation,
                          NSError * _Nullable * _Nullable outError);

// `count` taps at `point`, each held for `press`, `gap` apart.
BOOL DLSendSyntheticTaps(CGPoint point,
                         NSUInteger count,
                         NSTimeInterval press,
                         NSTimeInterval gap,
                         long long interfaceOrientation,
                         NSError * _Nullable * _Nullable outError);

NS_ASSUME_NONNULL_END
