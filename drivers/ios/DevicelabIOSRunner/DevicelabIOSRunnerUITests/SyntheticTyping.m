#import "SyntheticTyping.h"
#import <UIKit/UIKit.h>
#import "PrivateHeaders/XCTest/XCSynthesizedEventRecord.h"
#import "PrivateHeaders/XCTest/XCPointerEventPath.h"
#import "PrivateHeaders/XCTest/XCUIDevice.h"

static NSError *DLSynthesisError(NSInteger code, NSString *message) {
  return [NSError errorWithDomain:@"DevicelabIOSRunner"
                             code:code
                         userInfo:@{NSLocalizedDescriptionKey: message}];
}

// Sends a built event record through XCUIDevice's event synthesizer and waits
// for it to be delivered. No element lookup and no wait for the app to go
// idle happen here: the event goes straight to the screen, the way Maestro's
// runner and WebDriverAgent's W3C actions send theirs.
static BOOL DLSynthesize(XCSynthesizedEventRecord *record, NSError * _Nullable * _Nullable outError) {
  id device = [XCUIDevice sharedDevice];
  id synthesizer = [device valueForKey:@"eventSynthesizer"];
  if (synthesizer == nil) {
    if (outError) {
      *outError = DLSynthesisError(1, @"XCUIDevice.eventSynthesizer unavailable");
    }
    return NO;
  }

  SEL sel = NSSelectorFromString(@"synthesizeEvent:completion:");
  if (![synthesizer respondsToSelector:sel]) {
    if (outError) {
      *outError = DLSynthesisError(2, @"eventSynthesizer does not support synthesizeEvent:completion:");
    }
    return NO;
  }

  __block BOOL done = NO;
  __block NSError *innerError = nil;
  NSMethodSignature *sig = [synthesizer methodSignatureForSelector:sel];
  NSInvocation *inv = [NSInvocation invocationWithMethodSignature:sig];
  inv.target = synthesizer;
  inv.selector = sel;
  XCSynthesizedEventRecord *recordArg = record;
  [inv setArgument:&recordArg atIndex:2];
  void (^completion)(BOOL, NSError *) = ^(BOOL success, NSError *invokeError) {
    if (invokeError != nil) {
      innerError = invokeError;
    }
    done = YES;
  };
  [inv setArgument:&completion atIndex:3];
  [inv invoke];

  // Spin the run loop until the completion handler fires.
  NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:30.0];
  while (!done && [deadline timeIntervalSinceNow] > 0) {
    [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode
                             beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.01]];
  }

  if (!done) {
    if (outError) {
      *outError = DLSynthesisError(3, @"synthesizeEvent timed out");
    }
    return NO;
  }
  if (innerError != nil) {
    if (outError) {
      *outError = innerError;
    }
    return NO;
  }
  return YES;
}

static XCSynthesizedEventRecord *DLNewRecord(NSString *name, long long interfaceOrientation) {
  XCSynthesizedEventRecord *record = [XCSynthesizedEventRecord alloc];
  if ([record respondsToSelector:@selector(initWithName:interfaceOrientation:)]) {
    return [record initWithName:name interfaceOrientation:(UIInterfaceOrientation)interfaceOrientation];
  }
  return [record initWithName:name];
}

BOOL DLSendSyntheticTyping(NSString *text, NSUInteger typingSpeed, NSError * _Nullable * _Nullable outError) {
  if (text.length == 0) {
    return YES;
  }
  if (typingSpeed == 0) {
    typingSpeed = 60; // matches WDA default
  }

  XCSynthesizedEventRecord *record =
    [[XCSynthesizedEventRecord alloc] initWithName:@"DLSyntheticTyping"];
  XCPointerEventPath *path = [[XCPointerEventPath alloc] initForTextInput];
  [path typeText:text atOffset:0.0 typingSpeed:typingSpeed shouldRedact:NO];
  [record addPointerEventPath:path];
  return DLSynthesize(record, outError);
}

BOOL DLSendSyntheticTouch(CGPoint from,
                          CGPoint to,
                          NSTimeInterval hold,
                          NSTimeInterval move,
                          NSTimeInterval rest,
                          long long interfaceOrientation,
                          NSError * _Nullable * _Nullable outError) {
  XCSynthesizedEventRecord *record = DLNewRecord(@"DLSyntheticTouch", interfaceOrientation);
  XCPointerEventPath *path = [[XCPointerEventPath alloc] initForTouchAtPoint:from offset:0.0];
  NSTimeInterval offset = MAX(hold, 0.0);
  if (!CGPointEqualToPoint(from, to)) {
    offset += MAX(move, 0.0);
    [path moveToPoint:to atOffset:offset];
  }
  offset += MAX(rest, 0.0);
  [path liftUpAtOffset:offset];
  [record addPointerEventPath:path];
  return DLSynthesize(record, outError);
}

BOOL DLSendSyntheticTaps(CGPoint point,
                         NSUInteger count,
                         NSTimeInterval press,
                         NSTimeInterval gap,
                         long long interfaceOrientation,
                         NSError * _Nullable * _Nullable outError) {
  XCSynthesizedEventRecord *record = DLNewRecord(@"DLSyntheticTaps", interfaceOrientation);
  NSTimeInterval offset = 0.0;
  for (NSUInteger i = 0; i < MAX(count, (NSUInteger)1); i++) {
    XCPointerEventPath *path = [[XCPointerEventPath alloc] initForTouchAtPoint:point offset:offset];
    [path liftUpAtOffset:offset + press];
    [record addPointerEventPath:path];
    offset += press + gap;
  }
  return DLSynthesize(record, outError);
}
