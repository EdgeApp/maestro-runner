//go:build !windows

package devicelab_ios

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on path, creating it, and blocks until it
// is free. It is how parallel runs on simulators of one iOS version share a
// build cache: one builds, the others wait and reuse it.
func lockFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
