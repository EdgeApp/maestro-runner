#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/// Applies WDA's runner-process defaults once, before any test runs:
/// XCTest screenshots and diagnostic screen recordings off, local query
/// evaluation, keyboard autocorrect/prediction off with intros dismissed,
/// and XCTest's UI-interruption handling off. Set DL_KEEP_XCTEST_DEFAULTS=1
/// in the runner environment to skip it.
void DLApplyRunnerDefaults(void);

NS_ASSUME_NONNULL_END
