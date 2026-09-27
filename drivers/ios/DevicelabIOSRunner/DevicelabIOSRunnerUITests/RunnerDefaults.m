#import "RunnerDefaults.h"
#import <XCTest/XCTest.h>
#import <dlfcn.h>
#import <objc/runtime.h>

// Keyboard preferences live in the private TextInput framework, reached the
// way WDA does (FBConfiguration configureDefaultKeyboardPreferences).
static void DLConfigureKeyboardPreferences(void)
{
  void *handle = dlopen("/System/Library/PrivateFrameworks/TextInput.framework/TextInput", RTLD_LAZY);
  Class controllerClass = NSClassFromString(@"TIPreferencesController");
  id controller = [controllerClass respondsToSelector:@selector(sharedPreferencesController)]
    ? [controllerClass performSelector:@selector(sharedPreferencesController)]
    : nil;
  SEL setPref = NSSelectorFromString(@"setValue:forPreferenceKey:");
  if (controller != nil && [controller respondsToSelector:setPref]) {
    void (*set)(id, SEL, id, NSString *) = (void (*)(id, SEL, id, NSString *))[controller methodForSelector:setPref];
    set(controller, setPref, @NO, @"KeyboardAutocorrection");
    set(controller, setPref, @NO, @"KeyboardPrediction");
    set(controller, setPref, @YES, @"DidShowGestureKeyboardIntroduction");
    set(controller, setPref, @YES, @"DidShowContinuousPathIntroduction");
    SEL sync = NSSelectorFromString(@"synchronizePreferences");
    if ([controller respondsToSelector:sync]) {
      ((void (*)(id, SEL))[controller methodForSelector:sync])(controller, sync);
    }
  }
  if (handle != NULL) {
    dlclose(handle);
  }
}

// XCTest's own interruption handling taps "Allow"/"OK" on system alerts at
// moments of its choosing; the runner handles alerts itself (WDA does the
// same: XCUIApplication+FBUIInterruptions).
static void DLDisableUIInterruptionsHandling(void)
{
  Method m = class_getInstanceMethod(XCUIApplication.class, NSSelectorFromString(@"doesNotHandleUIInterruptions"));
  if (m != NULL) {
    method_setImplementation(m, imp_implementationWithBlock(^BOOL(__unused id app) { return YES; }));
  }
}

void DLApplyRunnerDefaults(void)
{
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    if (NSProcessInfo.processInfo.environment[@"DL_KEEP_XCTEST_DEFAULTS"] != nil) {
      return;
    }
    NSUserDefaults *defaults = NSUserDefaults.standardUserDefaults;
    [defaults setBool:YES forKey:@"DisableScreenshots"];
    [defaults setBool:YES forKey:@"DisableDiagnosticScreenRecordings"];
    [defaults setBool:YES forKey:@"XCTDisableRemoteQueryEvaluation"];
    DLConfigureKeyboardPreferences();
    DLDisableUIInterruptionsHandling();
    NSLog(@"DL_RUNNER_DEFAULTS applied");
  });
}
